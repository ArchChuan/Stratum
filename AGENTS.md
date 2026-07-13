# stratum

**默认原则**：正确 > 清晰 > 速度。有疑问先问，不默默猜测。

---

## WHAT — 技术栈与目录

### 后端（Go 1.22+）

| 层 | 路径 | 职责 |
|----|------|------|
| 入口 | `cmd/server/main.go` | 由 `api/wiring.BuildContainer` 构图，启停 Harness（≤30 行） |
| 路由 | `api/http/router.go` | Gin 路由组，从 `Container` 装配 handler |
| Handler | `api/http/handler/` | 每域一个文件，只做请求解析 + 响应组装 |
| DTO | `api/http/dto/` | Request/Response 结构体，无业务逻辑 |
| 中间件 | `api/http/middleware/` | ErrorHandler · MetricsMiddleware · Auth · Trace |
| 业务 | `internal/<ctx>/{domain,application,infrastructure}` | 8 个 bounded context（见下方架构分层） |
| 基础设施 | `pkg/{storage,messaging,httpclient,observability,...}` | 数据库/消息/HTTP/日志等无业务抽象 |

关键依赖版本：Gin v1.9 · NATS JetStream v1.31 · Milvus SDK v2.4.2 · pgx v5 · go-redis v9 · JWT RS256（golang-jwt v5）· OTEL v1.21 · Viper v1.18

### 前端（`web/`）

React 18 · Vite 4 · Ant Design 5.2 · React Router 6 · Axios · Moment.js

| 目录 | 职责 |
|------|------|
| `components/` | 共享 UI 组件 |
| `hooks/` | 自定义 Hook（`use*` 命名） |
| `pages/` | 路由页面组件（`*Page.jsx`） |
| `services/` | API 调用层（唯一 axios 实例） |
| `utils/` | 纯函数工具 |
| `contexts/` | React Context |

---

## WHY — 架构决策

| 决策 | 原因 |
|------|------|
| 多租户 PostgreSQL schema 隔离 | `SET LOCAL search_path` 切换租户，`pkg/tenantdb` 封装 |
| JWT RS256（非 HS256） | 非对称签名，网关可验证无需共享密钥 |
| NATS JetStream（非 Kafka） | 轻量、Go 原生、支持持久化 subject 格式 `domain.action` |
| Milvus v2.4.2（非 pgvector） | GraphRAG 需要高维向量检索，pgvector 性能不达标 |
| Harness 生命周期管理 | 组件顺序启动 → 逆序停止，避免依赖竞争 |
| LLMGateway 统一抽象 | 屏蔽 OpenAI/Anthropic/Ollama 差异，切换不改业务代码 |
| No AI control logic | 路由/重试/状态机必须硬编码，AI 只做语言任务 |

### 多租户 DDL 放置规则（踩坑总结）

- 编号迁移（`internal/migration/sql/NNN_*.sql`）只操作 **public schema**，禁止引用 tenant-only 表（如 `chat_conversations`、`memory_entries`、`entities`）
- 引用 tenant-only 表的 DDL 必须放 `pkg/storage/postgres/tenant_schema.sql`，由 `ProvisionAllTenantSchemas` 幂等应用到每个租户 schema
- 新增 tenant DDL 后需同步检查 `internal/migration/sql/tenant_schema.sql`（migration baseline）是否也需更新
- INSERT 语句必须与目标表 DDL 逐列核对，尤其 NOT NULL 无 DEFAULT 列（反例：outbox 漏 `message_id` 导致全量回滚）
- 向 `tenant_schema.sql` 的 `CREATE TABLE` 新增列后，必须紧跟 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` 做 backfill，否则已有租户的旧表不含该列，后续 INDEX / 查询会报 `column does not exist`（反例：entities.user_id 漏 backfill）
- 所有数据 schema 变更必须兼容历史租户数据：新增表/索引用 `IF NOT EXISTS`，新增列用 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`；新增 `NOT NULL` 列必须带安全 `DEFAULT` 或先 nullable → 回填 → 加约束；任何依赖新列的 INDEX / CONSTRAINT / 查询必须排在 backfill 之后，并用 schema 顺序测试覆盖（反例：先建 `idx_agent_exec_trace` 再补 `trace_id` 导致旧租户启动失败）
- `golang-migrate` dirty 状态修复：`force <version>` 将指定版本标记为 clean，再次 `Up()` 从下一版本继续；勿直接手改 `schema_migrations` 表

### 架构分层（DDD bounded context）

- 目录：`api/{http/{handler,dto,middleware},wiring}` · `internal/<ctx>/{domain/{,port/},application,infrastructure}` · `pkg/storage/{postgres,redis,milvus,tenantnaming}` · `pkg/{messaging/nats,httpclient,observability,crypto,constants,migration,textchunk}`
- 8 个 bounded context：`agent · memory · knowledge · skill · mcp · iam · llmgateway · platform`；跨域路由层（如 `capgateway`）作为 ACL，必要时下沉进消费上下文
- 依赖方向：`handler → application → domain/port`；`infrastructure` 实现 port，由 `api/wiring/Container` 集中装配；Shutdown 逆序释放
- 跨 context 调用走「消费者侧」port（接口放消费方 `domain/port/`），禁止 import 兄弟上下文的 `application` / `infrastructure`
- 单向底线：`pkg/` 不 import `internal/`；`domain/` 零第三方依赖（仅 stdlib + `pkg/constants`）；`application/` 不 import `pgx`/`redis`/`nats`/`gin`；`handler` 不 import `internal/*/infrastructure` 与 `pgx`/`redis`/`milvus`
- 错误分层：domain 定义 `Err*` → infrastructure 翻译 → application 编排 → middleware 映射 HTTP；响应体 `{"error":"..."}` 冻结
- API 向后兼容由 `api/http/contract_test.go` + `testdata/contracts/*.golden.json` 守护；CI 用 `go-arch-lint` + `depguard` 固化分层

### 各层职责速查

| 层 | 该做的事 | 不能做 |
|----|---------|--------|
| `dto/` | 结构 + binding tag | 业务规则、白名单、计算 |
| `handler/` | bind → 取 tenant → call service → render（≤15 行/方法），错误用 `c.Error(err)` 交给 ErrorHandler | import `pgx*`/`redis*`/`milvus*`/`internal/*/infrastructure`；散写白名单/SQL/编排 |
| `middleware/` | 横切：auth · trace · metrics · `domain.Err* → HTTP` 映射 | 任何业务编排 |
| `wiring/` | 组合根：构造 application + infrastructure，塞 Container，反向 Shutdown；跨 ctx ACL（≤30 行 thin adapter） | 处理 HTTP/业务规则 |
| `application/` | 用例编排 · 事务/Saga 边界 · DTO↔聚合 · 鉴权检查 · 发领域事件 | SQL、HTTP、序列化、业务不变量校验（让 domain 自检） |
| `domain/` | 实体、值对象、聚合根、不变量、领域算法（评分/状态机/切块策略）；`domain/port/` 出向接口契约 | 任何第三方依赖（`pkg/constants` 除外）；存在「贫血结构体」（无方法纯字段） |
| `infrastructure/<adapter>/` | 唯一职责：实现 `domain/port/` 接口；DB/MQ/HTTP IO；错误翻译（`pgconn.PgError → domain.ErrXxx`） | 业务规则、跨 ctx import 兄弟 `domain`/`infrastructure`、port 接口定义 |

### 消费者侧接口与运行时解析

- 跨 ctx：消费方在自己 `domain/port/` 定接口 → provider 在自己 `infrastructure/` 实现 → `api/wiring/` thin adapter 转接
- 跨租户：通过 `Resolver` 函数类型请求时延迟解析，例 `type EmbedServiceResolver func(ctx, tenantID) EmbedClient`，由 wiring 注入
- 接口最小化：消费者只声明需要的方法集；接口被 ≥2 个消费者复用时仍放消费方包，不去被依赖方暴露
- 单元测试必须能 mock port，不允许 build tag / init 替换实现

---

## HOW — 规范与命令

### 开发命令

```bash
go vet && go test -short ./...           # 每次改动后
go test -v -race -timeout 30s ./...      # PR 前完整跑
npm run lint && npm run build            # 前端 PR 前
```

### 端到端开发验证

涉及任何功能开发、Bug 修复、前后端联调、数据库链路、Agent/Skill/MCP/Memory/Knowledge/IAM 能力改动时，必须使用项目 skill `stratum-e2e-development`。完成标准不是代码写完或单测通过，而是根据需求目标完成真实 API、前端操作、后端服务、测试数据库链路的端到端验证；验证不符合目标时继续定位和修改，直到闭环。不得打印 token、密钥或原始 API key；临时脚本和自启动进程必须在完成前清理或明确说明。

### 常量规范（A 类：业务/配置数字）

**后端（Go）**

| 作用域 | 存放位置 | 命名 |
|--------|----------|------|
| 跨包共享（超时/TTL/分页/重试/Pool） | `pkg/constants/<domain>.go` | `Default*` / `Max*` / `Min*` / `*Timeout` / `*TTL` |
| 包内共享（≥2 个文件使用） | `internal/<pkg>/defaults.go` | 同上，包级 unexported 即可 |
| 单文件内使用 | 原文件 `const` 块 | 同上 |

规则：

- `pkg/constants/` 禁止 import `internal/`（单向依赖）
- 禁止在函数签名 / 结构体字面量中直接写魔法数字（timeouts、TTL、pageSize、topK、chunkSize、poolSize、retries）
- 纯 UI 样式数字（spacing、border-radius 等）不在此范围

**前端（JS/JSX）**

所有行为常量集中在 `web/src/constants/index.js`，按前缀分组：

```js
// API / 网络
API_DEFAULT_TIMEOUT_MS   AGENT_EXEC_TIMEOUT_MS

// 分页
DEFAULT_PAGE_SIZE   COMPACT_PAGE_SIZE   PAGE_SIZE_OPTIONS

// MCP
MCP_DEFAULT_TIMEOUT_SEC   MCP_MAX_TIMEOUT_SEC

// Skill
SKILL_DEFAULT_TEMPERATURE   SKILL_DEFAULT_MAX_TOKENS   SKILL_DEFAULT_TIMEOUT_SEC

// Memory
MEMORY_SEARCH_LIMIT
```

规则：

- 所有页面通过 `import { ... } from '../constants'` 引用，禁止页面内直接硬编码上述数字
- 常量名全大写下划线，值加单位后缀（`_MS` / `_SEC` / `_SIZE`）

---

### Go 规范

- 行宽 ≤120 · import 顺序：stdlib → third-party → internal · 圈复杂度 ≤10
- 日志：Zap only，禁止 `fmt.Print`；详见下方日志规范

### 日志规范（参考阿里/字节/腾讯标准）

**初始化**：`observability.NewLogger(env)` — production → JSON，其余 → console+color；固定字段 `app/env/host` 在 init 时注入。
·
**字段分层**

| 层 | 字段 | 注入位置 |
|----|------|----------|
| 链路 | `request_id` `trace_id` `tenant_id` `user_id` | TraceMiddleware per-request |
| LLM | `model` `provider` `prompt_tokens` `completion_tokens` `latency_ms` | `llm.complete` 事件 |
| ReAct | `trace_id` `tenant_id` `model` `step` `tokens` `tool_name` `latency_ms` | `react.llm` / `react.tool` 事件 |
| 访问 | `method` `path` `status` `latency_ms` `client_ip` `ua` | TraceMiddleware after |

**事件命名**：`layer.operation`，如 `llm.complete` · `react.llm` · `react.tool` · `agent execution started`

**级别规则**

| 级别 | 场景 |
|------|------|
| DEBUG | 开发调试，production 不输出 |
| INFO | 正常业务路径（HTTP < 400，LLM 成功，ReAct step） |
| WARN | 可预期异常（HTTP 4xx，重试中） |
| ERROR | 需处理异常（HTTP 5xx，外部调用失败）；自动附加 stacktrace |

**安全红线**：禁止记录 `password / token / api_key / PII`；禁止打印原始 HTTP response body（只记 status code + model）

- 错误：`fmt.Errorf("operation: %w", err)` 逐层包裹；瞬态错误指数退避（base 100ms，上限 10s）；外部依赖加熔断
- Handler 只解析请求 + 调 Service + 组装响应；业务逻辑在 Service 层
- 覆盖率 ≥80%，表驱动测试，mock 所有外部依赖，完整套件开 `-race`

### 前端规范

- 所有 API 调用走 `services/api.js` 的 axios 实例，禁止裸 `fetch`
- 错误统一：`message.error(err.response?.data?.error || '操作失败')`
- 禁止跨 `pages/` 目录导入；页面组件 ≤200 行，超出提取到 hooks/utils
- `useEffect` 依赖必须完整；异步 effect 需要 `let cancelled = false` 清理
- 用 `message` / `Modal.confirm`，禁止 `alert()` / `confirm()`
- 用户可见字符串全部中文；禁止 `console.log` 提交
- Token 禁止存 `localStorage`，用 `httpOnly` cookie 或内存 Context

### PR 格式

`[type](scope): description` — type: feat/fix/refactor/perf/test/docs/chore/ci

PR 描述必须包含：What · Why · HowToTest。CI（lint/test/scan）全绿才合并。

### AI 辅助测试

用 AI 写测试时，**必须提供模板文件**，不要让 AI 自由发挥。
做法：指定一个已有的好测试文件（如 `api/handler/tenant_handler_test.go`），说"按这个模式给 X 写完整测试"。
AI 会复用 mock 构造方式、断言风格、边界用例覆盖，生成质量稳定、可直接运行的测试。

### 测试与代码的主从关系

**代码是主，测试是从。** 测试描述代码应有的行为契约；当两者冲突时，先判断哪个代表正确意图：

- 测试断言有误（与产品需求不符）→ **改测试**
- 实现逻辑有误（违背需求）→ **改代码**

禁止默认"测试不过就改代码凑绿"——那只是让 CI 闭嘴，不是修复问题。

### 安全底线

- 密钥走 Vault/AWS Secrets Manager，禁止入 git
- 禁止修改 `config/prod.yaml`
- 传输 TLS 1.2+，静态数据 AES-256
- 前端禁止在 `.env` 提交任何密钥

---

## PRODUCT — 产品设计原则与规范

**核心**：意图优先（用户目标是"让 AI 完成任务"，不是"配置参数"）；技术参数折叠进高级设置，首屏 ≤3 个决策点。
**信息层级**：列表页 ≤5 列（名称+状态+1 个关键指标）；表单首屏 ≤8 项，基础展开+高级折叠。
**AI 可解释**：执行中流式输出+工具调用步骤可见；执行后步骤折叠面板（工具名+耗时+摘要）；失败必须定位到具体步骤。
**用户分层**：管理员（配置）vs 终端用户（对话）；管理操作必须二次确认；终端用户界面不暴露配置入口。
**实体约束**

- 知识库：`description` 必填（直接影响 AI 检索判断）；`name` 创建后不可改（向量 collection 绑定）
- Agent：max_iterations slider 1-20；知识库绑定展示各自 description
- 技能：temperature 用 Slider+标签；支持独立「测试运行」不经过 Agent
- 记忆：用户侧只读（content+时间+importance）；管理侧额外展示 scope+agent_id
**交互三态**：进行中→按钮 loading / Skeleton；成功→`message.success` ≤2s；失败→`message.error` 不自动消失。
**空状态**：所有列表必须有 Empty + 引导操作；无数据→"X 还是空的"；搜索无结果→"没有找到…"。
**危险操作**：删除/停用/清空用 `Modal.confirm`，描述后果；必填用 `rules` 不用星号；`extra` 说明字段，`tooltip` 补充，不重叠。
**命名约定**：Modal 开关 `createOpen/editOpen`；loading `createLoading/deleteLoading`；服务层函数动词+实体名 `createWorkspace`；Hook 返参直接解构不加 `state` 前缀。

## 12条编码核心原则

| # | 规则 | 要求 |
|---|------|------|
| 1 | 编码前先思考 | 声明所有假设；有歧义列出解读再问，不默猜 |
| 2 | 简单优先 | 最小正确解，不做投机性抽象 |
| 3 | 外科式修改 | 只改任务相关代码，不顺手重构/改名/调整风格 |
| 4 | 验证后才算完成 | 先定成功标准，未测试的代码不提交 |
| 5 | AI 不做控制逻辑 | 路由/重试/状态机必须硬编码 |
| 6 | Token 预算 | 任务 ≤4k，会话 ≤30k；95% 时暂停 → 摘要 → 继续 |
| 7 | 解决冲突 | 选一种方案，删掉另一种，禁止混合妥协 |
| 8 | 先全读 | 改之前读完相关文件/接口/调用链 |
| 9 | 验证业务意图 | 测试验证业务正确性，不只验返回值 |
| 10 | 长操作打检查点 | 每步记录：已完成 + 验证结果 + 剩余任务 |
| 11 | 遵守项目约定 | 跟随现有架构/模式/风格，不擅自替换 |
| 12 | 暴露错误 | 声明所有跳过、不确定、部分失败，不静默容错 |

**元顺序**：能跑 → 对 → 快 → 可扩展

---

## 分层上下文

- Layer 2（项目事实）：[`docs/agent/project.md`](docs/agent/project.md)
- Layer 3（模块规则）：[milvus](docs/agent/milvus.md) · [nats](docs/agent/nats.md) · [api](docs/agent/api.md) · [agent](docs/agent/agent.md) · [observability](docs/agent/observability.md)
