# stratum

**默认原则**：正确 > 清晰 > 速度。有疑问先问，不默猜。

---

## WHAT — 技术栈与目录

### 后端（Go 1.22+）

| 层 | 路径 | 职责 |
|----|------|------|
| 入口 | `cmd/server/main.go` | `api/wiring.BuildContainer` 构图，Harness 启停 |
| 路由 | `api/http/router.go` | Gin 路由组，从 Container 装配 handler |
| Handler | `api/http/handler/` | 每域一个文件，请求解析 + 响应组装 |
| DTO | `api/http/dto/` | Request/Response 结构体，无业务逻辑 |
| 中间件 | `api/http/middleware/` | ErrorHandler · MetricsMiddleware · Auth · Trace |
| 业务 | `internal/<ctx>/{domain,application,infrastructure}` | 8 个 bounded context |
| 基础设施 | `pkg/{storage,messaging,httpclient,observability,...}` | 无业务依赖抽象 |

依赖版本：Gin v1.9 · NATS JetStream v1.31 · Milvus SDK v2.4.2 · pgx v5 · go-redis v9 · JWT RS256（golang-jwt v5）· OTEL v1.21 · Viper v1.18

### 前端（`web/`）

React 18 · Vite 4 · Ant Design 5.2 · React Router 6 · Axios · Moment.js

| 目录 | 职责 |
|------|------|
| `components/` | 共享 UI 组件 |
| `hooks/` | 自定义 Hook（`use*`） |
| `pages/` | 路由页面（`*Page.jsx`，≤200 行） |
| `services/` | API 调用（唯一 axios 实例） |
| `utils/` | 纯函数 |
| `contexts/` | React Context |

---

## WHY — 架构决策

| 决策 | 原因 |
|------|------|
| 多租户 PostgreSQL schema 隔离 | `SET LOCAL search_path` 切换，`pkg/tenantdb` 封装 |
| JWT RS256 | 非对称签名，网关可验证无需共享密钥 |
| NATS JetStream | 轻量 Go 原生，持久化 subject `domain.action` |
| Milvus v2.4.2 | GraphRAG 高维检索，pgvector 性能不达标 |
| Harness 生命周期 | 顺序启动 → 逆序停止，避免依赖竞争 |
| LLMGateway 抽象 | 屏蔽 OpenAI/Anthropic/Ollama，切换不改业务 |
| No AI control logic | 路由/重试/状态机硬编码，AI 只做语言任务 |
| 删除策略 | 业务数据（facts/entries/conversations）硬删；audit log 软删 |

### 多租户 DDL 放置规则（踩坑总结）

- 编号迁移（`internal/migration/sql/NNN_*.sql`）只操作 public schema，**禁止**引用 tenant-only 表（`chat_conversations`、`memory_entries`、`entities`）
- Tenant-only DDL 必须放 `pkg/tenantdb/tenant_schema.sql`，由 `ProvisionAllTenantSchemas` 幂等应用
- 新增 tenant DDL 后同步检查 `internal/migration/sql/tenant_schema.sql` baseline 是否需更新
- INSERT 必须与目标 DDL 逐列核对，尤其 NOT NULL 无 DEFAULT 列
- `CREATE TABLE` 新增列后必须紧跟 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` backfill，否则已有租户旧表缺列（反例：entities.user_id 漏 backfill）
- dirty 状态修复：`force <version>` 标为 clean → `Up()` 从下一版本继续；勿手改 `schema_migrations`
- 所有操作 tenant-scoped 表的 repository struct，每个方法必须通过 `execTenant(ctx, tenantID, fn)` 执行，禁止直接调 `r.pool.Exec/Query`（反例：EntityRepo 全量绕过 → SQLSTATE 42P01 relation does not exist）
- port 接口中操作 tenant-scoped 表的方法签名必须含 `tenantID string`；缺失则调用层无法传入，tenant 路由永远无法实现
- 删除 tenant 向量数据：调用 MilvusClient.Delete by filter（不是 DropCollection）；collection 命名模式见 pkg/milvus/collections.go
- **废弃表清理**：功能迭代后旧表不会自动消失；每次新功能替代旧存储后，必须同时删除旧表的 DDL（tenant_schema.sql）和所有 Go 引用，并在租户schema定义中追加 DROP 语句清理存量租户。判断标准：`grep -r "table_name" --include="*.go"` 零引用即可删除（反例：sessions/exec_history/workflows/webhooks 等13张表在代码中零引用，却在 schema 中残存数月）
- **连接池 search_path 清理**：任何在 `execTenant` 之外执行 `SET search_path` 的函数，必须在 `conn.Release()` 前执行 `conn.Exec(ctx, "RESET search_path")`，否则脏连接回池会导致后续调用者解析错误 schema（反例：`ProvisionTenantSchema` 不 RESET → 启动时 `column "is_default" does not exist`）
- **启动路径 SQL 必须 schema 限定**：在 `execTenant` 之外运行的初始化 SQL（如 `EnsureDefaultTenant`）必须使用 `public.table_name` 全限定名，禁止依赖 `search_path` 解析 `public` 表

### DDD 架构约束

8 bounded context：`agent · memory · knowledge · skill · mcp · iam · llmgateway · platform`

依赖方向：`handler → application → domain/port`；infrastructure 实现 port；Container 集中装配；Shutdown 逆序

**单向底线**：

- `pkg/` 不 import `internal/`
- `domain/` 零第三方依赖（仅 stdlib + `pkg/constants`）
- `application/` 不 import `pgx`/`redis`/`nats`/`gin`
- `handler` 不 import `internal/*/infrastructure` 与 `pgx`/`redis`/`milvus`

跨 ctx：消费方在自己 `domain/port/` 定接口；禁止 import 兄弟 ctx 的 `application`/`infrastructure`

错误分层：domain `Err*` → infrastructure 翻译 → application 编排 → middleware 映射 HTTP；响应体 `{"error":"..."}` 冻结

API 兼容由 `api/http/contract_test.go` + `testdata/contracts/*.golden.json` 守护；CI 用 `go-arch-lint` + `depguard`

### 各层职责速查

| 层 | 该做 | 禁止 |
|----|------|------|
| `dto/` | 结构 + binding tag | 业务规则、计算 |
| `handler/` | bind → tenant → service → render（≤15 行/方法），`c.Error(err)` | import pgx/redis/milvus/infrastructure；散写 SQL/编排 |
| `middleware/` | auth · trace · metrics · Err→HTTP | 业务编排 |
| `wiring/` | 构造 app+infra，塞 Container，逆序 Shutdown，跨 ctx ACL（尽量精简） | HTTP/业务规则 |
| `application/` | 用例编排 · 事务 · DTO↔聚合 · 鉴权 · 领域事件 | SQL/HTTP/序列化/不变量校验 |
| `domain/` | 实体/值对象/聚合根/不变量/领域算法；`port/` 出向接口 | 第三方依赖；贫血结构体 |
| `infrastructure/` | 实现 port；DB/MQ IO；错误翻译 | 业务规则；跨 ctx import |

跨租户解析：`type EmbedServiceResolver func(ctx, tenantID) EmbedClient`，wiring 注入；接口最小化，只声明消费方所需方法

---

## HOW — 规范与命令

### 开发命令

```bash
make infra-up                            # 启动依赖（PostgreSQL / Redis / Milvus / NATS）
make be-test                             # go test -race ./...
make be-lint                             # golangci-lint
go vet && go test -short ./...           # 每次改动后快速验证
make fe-lint && make fe-build            # 前端 PR 前
```

### 常量规范

魔法数字（timeout/TTL/pageSize/topK/chunkSize/poolSize/retries）**禁止内联**

| 作用域 | 位置 | 命名 |
|--------|------|------|
| 跨包共享 | `pkg/constants/<domain>.go` | `Default*`/`Max*`/`*Timeout`/`*TTL` |
| 包内共享（≥2 文件） | `internal/<pkg>/defaults.go` | 同上，unexported |
| 单文件 | 原文件 `const` 块 | 同上 |

前端：`web/src/constants/index.js`，全大写下划线+单位后缀（`_MS`/`_SEC`/`_SIZE`）

**超时分层**：agent 子操作使用 `pkg/constants/timeouts.go` 分级常量——DB读写 5s / 记忆注入 10s / RAG·Recall 15s / LLM 非流式 60s（flat cap）；LLM 流式**禁止** flat timeout，改用 transport `ResponseHeaderTimeout`(30s) + 空闲看门狗 `LLMStreamIdleTimeout`(30s/token 间隔)，outer `AgentExecTimeout`(90s) 兜底。

### 日志（Zap only，禁止 `fmt.Print`）

初始化：`observability.NewLogger(env)`；事件命名：`layer.operation`（`llm.complete`、`react.llm`、`react.tool`）

安全红线：禁止记录 `password/token/api_key/PII`；禁止打印原始 HTTP response body

详细字段/级别规范：`docs/agent/observability.md`

### Go 规范

- `fmt.Errorf("op: %w", err)` 逐层包裹；瞬态错误指数退避（base 100ms，上限 10s）+ 熔断
- 覆盖率 ≥80%；表驱动测试；mock 所有外部依赖；全套开 `-race`
- 写测试前先读同域已有测试文件，复用其 mock 构造方式和断言风格
- 修改 port 接口后立即执行 `grep -r "接口名" --include="*_test.go"` 找出所有 mock/stub 并同步新方法；漏掉任一实现者将导致整包编译失败（反例：DeleteAllByUser 扩展后 MockEntityRepo + stubEntityRepo 在3个包9处报错）
- 实现"删除实体 X 全量数据"前，先枚举所有关联存储：facts · entries · entities · conversations · messages · vectors，逐一确认清除范围（反例：ClearUserMemories/ClearAgentMemories 多次遗漏 memory_entries/entities/conversations，反复修补）
- 新建 repository struct：(1) 每个方法必须用 execTenant；(2) port 接口签名含 tenantID；(3) 立即写mock，参考同context 已有 mock文件
- **pgx v5 JSONB 编码**：向 JSONB 列写入自定义 Go struct 时，必须先 `json.Marshal` 得到 `[]byte`，再作为 `string(b)` 传参（pgx v5 `JSONCodec` 通过 `encodePlanJSONCodecEitherFormatString` 处理 string）；禁止直接传 struct 或 `pgtype.JSONB{}`（后者需 OID 解析，池模式下 OID=0 报 `cannot find encode plan`）
- **并发 & Context 红线**（踩坑：MCP performHealthCheck，2026-06-25）
  - `WithTimeout` 禁止循环外创建：每次迭代独立 `ctx, cancel := context.WithTimeout(...)`，否则串行共用同一 budget
  - 重连/替换有状态对象：`NewXxx()` 创建新实例 + `map[key] = fresh` 写回，禁止在快照副本上 `obj.Reconnect()` 不回写
  - N 个独立 IO/网络操作：`wg.Add(1) / go func()` 并发，禁止 `for` 串行阻塞（单个慢连接卡住全部）
  - **共享指针 TOCTOU（反例：VectorStore.client 所有调用方，2026-06-27）**：`ensureConnected` 释放锁后到 `client.Method()` 之间存在窗口，`Close()` 可将指针置 nil → nil panic。修复：将"ensureConnected + 在读锁下捕获指针"封装为 `getClient(ctx)`，所有调用方用 `c := getClient(ctx)` 后操作 `c`，绝不直接访问 `vs.client`
  - **goroutine 生命周期必须用 WaitGroup 跟踪（反例：TenantWatcher，2026-06-27）**：`Stop()` 调用 `cancel()` + `wk.Stop()` 后立即返回，worker goroutine 仍在运行，Harness Shutdown 序保证被破坏。修复：spawning 时 `wg.Add(1)`，goroutine 内 `defer wg.Done()`，`stopAll` 释放锁后调 `wg.Wait()`
  - **带缓冲 channel + ctx 超时必须排水（反例：doConnect，2026-06-27）**：`ctx.Done()` 分支返回后后台 goroutine 若成功连接会写入 buffered channel，无人读取，gRPC client 永不 Close。修复：`go func() { if res := <-resultCh; res.err == nil { res.client.Close() } }()`
  - **早期错误路径必须 drain WaitGroup（反例：Pipeline.Start，2026-06-27）**：cancel() 后直接 return error，已启动的 goroutine 未 drain；调用方无从 wait。修复：错误路径在 cancel() 后紧跟 `p.wg.Wait()`

### 前端规范

- API 调用走 `services/api.js` axios 实例，禁止裸 `fetch`
- 错误：`message.error(err.response?.data?.error || '操作失败')`
- Token 禁止存 `localStorage`，用 `httpOnly` cookie 或内存 Context
- `useEffect` 依赖完整；异步 effect 需 `let cancelled = false`
- 禁止 `alert()`/`confirm()`/`console.log` 提交；用户字符串全中文

### 非显然编码规则

| 规则 | 要求 |
|------|------|
| AI 不做控制逻辑 | 路由/重试/状态机必须硬编码 |
| 冲突择一 | 选一种方案，删掉另一种，禁止混合妥协 |
| 检查点 | 长操作每步记录：已完成 + 验证结果 + 剩余任务 |
| 暴露错误 | 声明所有跳过/不确定/部分失败，不静默容错 |

### 产品规范（前端任务适用）

- 列表页 ≤5 列；表单首屏 ≤8 项（基础展开 + 高级折叠）
- 知识库 `description` 必填；`name` 创建后不可改（向量 collection 绑定）
- 交互三态：进行中 loading/Skeleton；成功 `message.success` ≤2s；失败 `message.error` 不自动消失
- 空状态：所有列表必须有 Empty + 引导操作
- 危险操作用 `Modal.confirm` 并描述后果
- 命名：Modal 开关 `createOpen/editOpen`；loading `createLoading/deleteLoading`

### PR 格式

`[type](scope): description`（feat/fix/refactor/perf/test/docs/chore/ci）

描述必须包含：What · Why · HowToTest。CI 全绿才合并。**禁止修改 `config/prod.yaml`**。

---

## 分层上下文

按需阅读，不要全量加载（最后更新：2026-06-24）：

**Agent / 核心流程**

- `docs/agent/project.md` — 项目事实、bounded context 边界
- `docs/agent/agent.md` · `docs/agent/api.md` — Agent 模块规则、API 规范
- `docs/agent/agent-chat-flow.md` — 端到端会话流程（SSE → ReAct → LLM → 持久化）

**基础设施**

- `docs/agent/milvus.md` — Milvus v2.4 操作规则、collection 命名、向量删除
- `docs/agent/nats.md` — JetStream subject 规范、发布/消费模式
- `docs/DATA_PERSISTENCE.md` — Repository 模式、事务边界、多租户持久化

**集成**

- `docs/LLM_INTEGRATION.md` — LLMGateway provider 配置、token 计费
- `docs/mcp-implementation-summary.md` — MCP bounded context 实现总结

**可观测性 / 架构**

- `docs/agent/observability.md` — 日志/追踪完整规范
- `docs/architecture/EVOLUTION.md` — 架构演化历史与编码规范（2026-06-22）
