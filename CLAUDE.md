# ClawHermes-AI-Go

**默认原则**：正确 > 清晰 > 速度。有疑问先问，不默默猜测。

---

## WHAT — 技术栈与目录

### 后端（Go 1.22+）

| 层 | 路径 | 职责 |
|----|------|------|
| 入口 | `cmd/server/main.go` | 初始化 Harness，注册所有组件 |
| 路由 | `api/router.go` | Gin 路由组，所有端点集中定义 |
| Handler | `api/handler/` | 每域一个文件，只做请求解析 + 响应组装 |
| DTO | `api/model/` | Request/Response 结构体，无业务逻辑 |
| 中间件 | `api/middleware/` | ErrorHandler · MetricsMiddleware · Auth · Trace |
| 业务 | `internal/` | Agent · Memory · Skill · LLMGateway · Knowledge |
| 基础设施 | `pkg/` | Zap · OTEL · pgxpool · go-redis · tenantdb · Milvus |

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

---

## HOW — 规范与命令

### 开发命令

```bash
go vet && go test -short ./...           # 每次改动后
go test -v -race -timeout 30s ./...      # PR 前完整跑
npm run lint && npm run build            # 前端 PR 前
```

### Go 规范

- 行宽 ≤120 · import 顺序：stdlib → third-party → internal · 圈复杂度 ≤10
- 日志：Zap only，结构化字段 `request_id / user_id / tenant_id / operation`，禁止 `fmt.Print`，禁止记录密码/token/PII
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

## Karpathy 12 Rules（速查）

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
