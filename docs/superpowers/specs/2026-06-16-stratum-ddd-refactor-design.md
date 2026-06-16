# Stratum DDD 分层 + 反向依赖整体重构 设计 spec

- 日期：2026-06-16
- 范围：整库（`api/` + `internal/` + `pkg/` + `cmd/server/main.go`）
- 范式：DDD 分层 + bounded context（方案 A）
- 节奏：Big-bang，并行最大化
- 兼容性底线：HTTP API 完全向后兼容（路径 / 请求体 / 响应体 / 错误码冻结）
- 目标：消除 internal 业务包对 `*pgxpool.Pool`、`*redis.Client`、Milvus SDK、`net/http` 的直接依赖；消除 `agent → memory/pipeline`、`config → knowledge` 等反向依赖；提供可在 CI 固化的分层规则。

---

## 1. Bounded Context 划分（顶层骨架）

把现有 `internal/<biz>` 收编成 8 个 bounded context，每个 context 内强制三分（`domain` / `application` / `infrastructure`）。

```
internal/
├── agent/          ← Agent 配置、执行编排、ReAct graph、a2a
├── memory/         ← 记忆管线（pipeline、enricher、recall）+ 长短期记忆
├── knowledge/      ← RAG、文档分块、ingest
├── skill/          ← Skill 定义 + 执行（http/llm/code）+ skillgateway 路由
├── mcp/            ← MCP client manager + skill adapter
├── iam/            ← auth / tenant / user / hermes（合并 internal/auth + internal/hermes）
├── llmgateway/     ← LLM 多供应商抽象（被多个 context 共用）
└── platform/       ← config、harness（跨域基础编排，零业务依赖）
```

### 1.1 与现状的迁移映射

| 现状 | 目标 | 理由 |
|------|------|------|
| `internal/auth` + `internal/hermes` 分散 | 合并到 `internal/iam` | 都是身份/会话职责 |
| `internal/memory` + `internal/memory/pipeline` 平铺 | `memory/{domain,application,infrastructure/pipeline}` | pipeline 本质是 infrastructure |
| `internal/skill` + `internal/skillgateway` 分两包 | 合并到 `internal/skill`（gateway 进 application 层） | gateway 是 skill 的 use case 编排 |
| `internal/document` + `internal/embedding` | document 沉入 `internal/knowledge/infrastructure/`；embedding 沉入 `internal/llmgateway/infrastructure/` | 它们都是某域的 infra |
| `internal/textchunk` | 提到 `pkg/textchunk`（无业务，纯字符串分块工具） | knowledge 与 memory 都依赖它，必须放 pkg |
| `internal/capgateway` | 沉入 `internal/agent/application/capability_router.go` | capgateway 是 agent 编排能力路由，不是平台基础 |
| `internal/config` + `internal/harness` 平铺 | 合并到 `internal/platform/{config,harness}` | 跨域基础编排归一处，platform 不依赖任何 context |
| `internal/migration` | 移到 `pkg/migration` | 纯基础设施，无业务 |

### 1.2 Context Map（依赖关系，禁止反向）

```
api/http
  ↓
agent ── 依赖 → memory.port, knowledge.port, skill.port, mcp.port, llmgateway.port
memory ── 依赖 → llmgateway.port
knowledge ── 依赖 → llmgateway.port
skill ── 依赖 → llmgateway.port, mcp.port
mcp ── 依赖 → (无业务依赖)
iam ── 依赖 → (无业务依赖)
llmgateway ── 依赖 → (无业务依赖)
platform ── 被所有 context 依赖（不依赖任何 context；不含 capgateway）
```

任何反向（如 `memory → agent`、`platform → skill`）一律 CI 拒绝。

### 1.3 关于 capgateway 的归属

capgateway 现在的职责是：把 agent 的"能力调用"路由到 skill 或 mcp。如果继续把它放在 platform，platform 必须 import skill 和 mcp（反向）。所以放在 agent 域：

- `agent/application/capability_router.go` — `CapabilityRouter` 接口在 agent 内部
- `agent/infrastructure/capability/` — 通过 `skill.port.SkillExecutor` + `mcp.port.MCPInvoker` 注入实现

agent 单向依赖 skill / mcp 的 port，无环。

---

## 2. Context 内部三层结构（agent 样板，其他 context 复用）

```
internal/agent/
├── domain/                       ← 纯业务，无任何外部依赖
│   ├── agent.go                    ← Agent 实体
│   ├── execution.go                ← Execution 实体
│   ├── chat.go                     ← ChatMessage / Conversation 实体
│   ├── errors.go                   ← ErrAgentNotFound / ErrInvalidSkill 等
│   └── port/
│       ├── repository.go             ← AgentRepo / ExecutionRepo / ChatRepo
│       ├── memory.go                 ← MemoryRecaller
│       ├── skill.go                  ← SkillExecutor
│       ├── knowledge.go              ← KnowledgeRetriever
│       └── llm.go                    ← LLMCompleter
│
├── application/                  ← use case 编排，只依赖 domain + port
│   ├── agent_service.go            ← Create/Update/Delete/Get
│   ├── execution_service.go        ← 执行 Agent（编排 ReAct + memory + skill）
│   ├── chat_service.go             ← 对话流式输出
│   └── react/
│       └── graph.go                  ← 原 internal/agent/graph
│
└── infrastructure/               ← 实现 port，唯一可以 import 第三方 SDK 的地方
    ├── persistence/
    │   ├── agent_repo_pg.go          ← AgentRepo 的 pgx 实现（原 registry.go）
    │   ├── execution_repo_pg.go      ← 原 execution_store.go
    │   └── chat_repo_pg.go           ← 原 chat_store.go
    ├── memory_adapter.go             ← MemoryRecaller 实现，包装 memory.application.RecallService
    ├── skill_adapter.go
    ├── knowledge_adapter.go
    └── llm_adapter.go
```

### 2.1 关键纪律

1. **port 放在消费方**（agent 的 port 在 `agent/domain/port/`）。这是 DIP 的核心：消费者声明它要什么，提供者来实现。
2. **跨 context 调用走 adapter，不直接 import 对方 application**。
3. **domain 包零外部依赖**：不 import `pgx` / `redis` / `nats` / `gin` / `zap` / 任何 internal 兄弟 context。只能 import stdlib + `pkg/constants`。日志在 application 层做。
4. **port 接口最小方法集**。例：`MemoryRecaller` 只暴露 `Recall(ctx, query, k) ([]string, error)`，不是 CRUD 全家桶。
5. **infrastructure 是反向依赖的唯一聚集点**：所有"上层接口 ← 下层实现"的胶水都在这里。

### 2.2 现有反向依赖修复对照

| 现状反向依赖 | 修复方式 |
|-------------|---------|
| `agent → memory/pipeline` | `agent.domain.port.MemoryRecaller`，由 memory 的 RecallService 实现，wiring 注入 |
| `config → knowledge` | config 拉成 `platform.config`，knowledge 单向依赖 platform |
| `agent → capgateway`（具体类型） | 改成 `agent.domain.port.CapabilityRouter`（capgateway 隐式满足） |
| `RAGService` 持 `*VectorStore` / `*EmbeddingService` | 改成 `knowledge.domain.port.{VectorIndex,Embedder}` |

---

## 3. 基础设施抽象与 pkg 重组

### 3.1 pkg 目录重组

```
pkg/
├── storage/
│   ├── postgres/
│   │   ├── pool.go              ← 现 pkg/postgres/postgres.go
│   │   ├── querier.go           ← Querier / Execer / TxBeginner 接口
│   │   └── tenant.go            ← 现 pkg/tenantdb/{context,postgres,schema}.go 合并
│   ├── redis/
│   │   ├── client.go            ← 现 pkg/redis/redis.go
│   │   └── store.go             ← KVStore / SessionStore 接口
│   ├── milvus/
│   │   ├── client.go            ← 现 pkg/vector/vector_store.go
│   │   └── index.go             ← VectorIndex 接口
│   └── tenantnaming/            ← 现 pkg/tenantdb/{milvus,nats,neo4j}.go：纯命名 DSL
├── messaging/
│   └── nats/
│       ├── conn.go
│       └── publisher.go
├── httpclient/                  ← 新增
│   ├── client.go
│   └── transport.go             ← 含 SSRF-safe transport
├── textchunk/                   ← 现 internal/textchunk 移过来（无业务，knowledge + memory 共用）
├── observability/               ← 不变
├── crypto/                      ← 不变
├── constants/                   ← 不变
└── migration/                   ← 从 internal/migration 移过来
```

### 3.2 关键接口

`pkg/storage/postgres/querier.go`：

```go
type Querier interface {
    QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
    Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
    Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type TxBeginner interface {
    Querier
    Begin(ctx context.Context) (pgx.Tx, error)
}

var _ TxBeginner = (*pgxpool.Pool)(nil)

type TenantExecer interface {
    ExecTenant(ctx context.Context, tenantID string, fn func(ctx context.Context, tx pgx.Tx) error) error
}
```

`pkg/storage/redis/store.go`：

```go
type KVStore interface {
    Get(ctx context.Context, key string) (string, error)
    Set(ctx context.Context, key string, value string, ttl time.Duration) error
    Del(ctx context.Context, keys ...string) error
}
```

`pkg/storage/milvus/index.go`：

```go
type VectorIndex interface {
    EnsureCollection(ctx context.Context, name string, dim int) error
    Upsert(ctx context.Context, name string, docs []Document) error
    Search(ctx context.Context, name string, vec []float32, topK int, filter string) ([]SearchHit, error)
    Drop(ctx context.Context, name string) error
}
```

`pkg/httpclient/client.go`：

```go
type Doer interface {
    Do(req *http.Request) (*http.Response, error)
}

func New(opts ...Option) *http.Client       // 带超时/重试/UA
func NewSSRFSafe(opts ...Option) *http.Client  // 含 SSRF 防护
```

### 3.3 业务侧改造效果

| 现状 | 目标 |
|------|------|
| `internal/agent/registry.go: pool *pgxpool.Pool` | `infrastructure/persistence/agent_repo_pg.go: db storage.TxBeginner` |
| `internal/memory/manager.go: pool *pgxpool.Pool` | `memory/infrastructure/persistence: db storage.TxBeginner` |
| `internal/auth/token_store.go: rdb *redis.Client` | `iam/infrastructure: kv storage.KVStore` |
| `internal/memory/pipeline/vector_adapter.go: vs *vector.VectorStore` | `memory/infrastructure/pipeline: idx storage.VectorIndex` |
| 4 处独立 `&http.Client{Timeout: ...}` | 统一 `httpclient.New(...)` |

### 3.4 命名一致性

- `pkg/postgres` → `pkg/storage/postgres`
- `pkg/redis` → `pkg/storage/redis`
- `pkg/vector` → `pkg/storage/milvus`
- `pkg/tenantdb` → 拆：SQL 模板 → `pkg/storage/postgres/tenant.go`；纯命名 → `pkg/storage/tenantnaming/`
- `internal/migration` → `pkg/migration`
- `internal/textchunk` → `pkg/textchunk`

---

## 4. Composition Root（wiring）与 API 层

### 4.1 api 层目录

```
api/
├── http/
│   ├── handler/
│   │   ├── agent_handler.go             ← 仅 DTO ↔ application.AgentService
│   │   ├── memory_handler.go
│   │   ├── knowledge_handler.go
│   │   ├── skill_handler.go
│   │   ├── mcp_handler.go
│   │   ├── iam/
│   │   │   ├── auth_handler.go
│   │   │   ├── tenant_handler.go
│   │   │   └── admin_handler.go
│   │   └── chat_handler.go
│   ├── middleware/                       ← 不变（auth/trace/error）
│   ├── dto/                              ← 现 api/model 重命名
│   │   ├── agent.go
│   │   ├── memory.go
│   │   └── ...
│   └── router.go                         ← 仅挂 router.Group，≤ 100 行
│
└── wiring/
    ├── wiring.go                        ← BuildContainer(cfg, logger) -> *Container
    ├── storage.go                       ← pgx pool / redis / milvus / nats
    ├── llmgateway.go                    ← Gateway + tenant cache + EmbedResolver
    ├── memory.go
    ├── knowledge.go
    ├── skill.go
    ├── agent.go
    ├── mcp.go
    ├── iam.go
    └── platform.go
```

### 4.2 main.go 形态

```go
func main() {
    cfg := config.Load()
    logger := observability.NewLogger(cfg.Env)
    ctx := context.Background()

    container, err := wiring.BuildContainer(ctx, cfg, logger)
    // ... err handling
    defer container.Shutdown(ctx)

    router := http.NewRouter(container)
    if err := container.Harness.Start(ctx); err != nil { ... }
    if err := router.Run(cfg.HTTPAddr); err != nil { ... }
}
```

### 4.3 wiring 容器

```go
type Container struct {
    AgentSvc      *agent.AgentService
    AgentExecSvc  *agent.ExecutionService
    ChatSvc       *agent.ChatService
    MemorySvc     *memory.MemoryService
    RecallSvc     *memory.RecallService
    KnowledgeSvc  *knowledge.RAGService
    SkillSvc      *skill.SkillService
    MCPSvc        *mcp.MCPService
    AuthSvc       *iam.AuthService
    TenantSvc     *iam.TenantService
    AdminSvc      *iam.AdminService

    Harness  *platform.Harness
    Shutdown func(context.Context) error
}
```

依赖顺序：`storage → llmgateway → platform → mcp → skill → knowledge → memory → agent → iam`。

### 4.4 handler 纪律

- handler 只持 application service 接口（不持具体 struct）。
- handler 不再做"读 settings → 构 gateway → 注 cache"（router.go 现在的 buildEmbedResolver / buildGatewayForTenant），全部下沉到 `wiring/llmgateway.go` 与 `iam/application/tenant_settings_service.go`。
- handler 不感知 zap 字段，trace_id / tenant_id 由 middleware 注入 ctx，application 层 logger 自取。

### 4.5 跨 context 调用链（具象化）

```
api/http/handler/agent_handler
    ↓
agent.application.ExecutionService    持 agent.domain.port.MemoryRecaller
    ↓
agent.infrastructure.memory_adapter   实现 MemoryRecaller，转发到 memory.application.RecallService
    ↓
memory.application.RecallService      持 memory.domain.port.{VectorIndex, Embedder}
    ↓
memory.infrastructure.milvus_index    storage.VectorIndex 实现
    + memory.infrastructure.embed_adapter (llmgateway adapter)
```

### 4.6 API 向后兼容守护

- 路径、方法、请求体、响应体、错误码全部冻结。`api/http/dto/*` 与现 `api/model/*` 字段 JSON tag 一一对应。
- 契约测试 `api/http/contract_test.go` 起 httptest Server，对每个 endpoint 跑 `testdata/contracts/*.golden.json` 重放。
- golden 数据由阶段 0 自动生成：枚举 `gin.Engine.Routes()`，对每个路由跑成功 / 4xx / 5xx 至少 3 条 case。

---

## 5. 错误处理 / 日志 / 测试

### 5.1 错误处理（分层）

**domain**：定义领域错误。

```go
var (
    ErrAgentNotFound  = errors.New("agent not found")
    ErrInvalidSkill   = errors.New("invalid skill binding")
    ErrExecutionLimit = errors.New("execution iteration limit exceeded")
)

type ValidationError struct { Field, Reason string }
func (e ValidationError) Error() string { ... }
```

**infrastructure**：把基础设施错误翻译成 domain 错误。

```go
err := r.db.QueryRow(...).Scan(...)
if errors.Is(err, pgx.ErrNoRows) {
    return nil, domain.ErrAgentNotFound
}
if err != nil {
    return nil, fmt.Errorf("agent_repo: query: %w", err)
}
```

**application**：编排错误，不感知 SQL/HTTP。

```go
ag, err := s.repo.Get(ctx, id)
if errors.Is(err, domain.ErrAgentNotFound) {
    return nil, err
}
if err != nil {
    return nil, fmt.Errorf("agent_service.get: %w", err)
}
```

**api/http/middleware/error.go**：集中翻译 → HTTP。

```go
var errMap = map[error]struct{ Status int; Code string }{
    domain.ErrAgentNotFound: {404, "AGENT_NOT_FOUND"},
    domain.ErrInvalidSkill:  {400, "INVALID_SKILL"},
}
```

JSON 错误体格式 `{"error": "..."}` 不变。

### 5.2 日志规范（沿用 CLAUDE.md，分层注入）

| 层 | 谁打日志 | 字段 |
|----|----------|------|
| api/middleware | TraceMiddleware | trace_id / tenant_id / user_id / method / path / status / latency_ms |
| application | use case 入口 + 关键事件 | + event 名（`agent.execute.start` / `memory.recall.complete`） |
| domain | **不打日志**，只返结构化错误 | — |
| infrastructure | 外部调用前后 | + provider / collection / op / latency_ms |

domain 包零 `zap` import（CI 校验）。现有 `react.llm` / `react.tool` / `llm.complete` 事件名保留。

### 5.3 测试策略

| 层 | 类型 | 覆盖率 | mock | 不依赖 |
|----|------|--------|------|--------|
| domain | 单元 | ≥ 90% | — | 无外部依赖 |
| application | 单元 + 表驱动 | ≥ 80% | mock domain.port | DB/Redis/Milvus/HTTP |
| infrastructure | 集成（build tag `integration`） | ≥ 70% | — | testcontainers |
| api/http | 契约 + 集成 | ≥ 75% | mock application | — |
| 端到端 | 现 CI 集成测试 | — | — | docker-compose |

mock 在 `<context>/<layer>/mocks/` 子包；不同层的 mock 不复用。

**契约测试**：`api/http/contract_test.go` 在 wiring 注入 mock application 启动 gin server；从 `testdata/contracts/*.golden.json` 读请求/响应做 diff。

### 5.4 CI 防回潮

- `go-arch-lint` 配置：
  - `internal/*/domain/**` 禁 import 第三方非 stdlib（白名单 `pkg/constants`）
  - `internal/*/application/**` 禁 import `pgx` / `redis` / `nats` / `gin` / 兄弟 context 的 `infrastructure`
  - `pkg/**` 禁 import `internal/**`
  - 跨 context import 仅允许 `internal/<other>/domain/port`
- `make arch-check` 跑 `go-arch-lint`，CI 必跑。
- PR 模板加勾选："本 PR 是否引入新的反向依赖？"

---

## 6. 实施阶段（Big-bang，并行最大化）

| # | 阶段 | 内容 | 并行性 |
|---|------|------|--------|
| **0** | 录契约 | httptest 枚举 `gin.Engine.Routes()` → `testdata/contracts/*.golden.json`（每路由 ≥3 条 case） | 串行（前置） |
| **1** | pkg 重组 + 接口下沉 | §3 全部 | **可与 0 并行** |
| **2** | wiring 骨架 + 容器 + 契约保护 | 新建 `api/http/`、`api/wiring/`；`cmd/server/main.go` 切到 `BuildContainer`；旧 `SetupRouter` thin shim；contract test 接 CI | 等 1 完成 |
| **3** | bounded context 空骨架 + port 冻结 | 8 个 context 的 `domain/{,port/}/application/infrastructure/`；`domain/port/*.go` 接口签名先全部冻结 | 等 2 完成 |
| **4** | **域内迁移（合一，全部并行）** | platform / iam / llmgateway / mcp / skill / knowledge / memory / agent 各自一个 sub-PR，**8 路并行**。约束：进 wiring 容器、契约不破、不动 SQL/JSON | 8 个 context 同时 |
| **5** | CI 防回潮 + 清桥 | go-arch-lint 全开；depguard 全开；删 pkg 旧 alias、deprecated shim | 串行收尾 |

### 6.1 防回滚

- 每阶段一个 PR，独立合并。
- 阶段 1-3 不动业务代码（接口 / 骨架），回滚成本最低。
- 阶段 4 内 8 个 sub-PR 互不阻塞；任何一个 context 翻车，只回退该 sub-PR。
- 全程 `testdata/contracts/*.golden.json` 不许变。

### 6.2 关键风险与对策

| 风险 | 触发 | 对策 |
|------|-----|------|
| 契约 golden 录不全 | 阶段 0 漏 endpoint | `gin.Engine.Routes()` 枚举所有路由，逐个录；rich case ≥ 3/路由 |
| Tenant 上下文穿透链断裂 | 重构中漏注入 | application 层入口统一 `tenantdb.FromContext` 校验；缺即返 `domain.ErrTenantRequired` |
| ReAct graph 与 agent 解耦失败 | graph 仍依赖具体类型 | graph 只依赖 `agent.domain.port`，所有依赖在 ExecutionService 调用时注入 |
| memory pipeline 后台 worker 重启 | NATS consumer 名变 | 阶段 4 严禁改 consumer name / subject |
| 集成测试时间膨胀 | testcontainers 起 8 个 | 集成测试用 build tag 分组，PR CI 跑 unit + contract，nightly 跑 full integration |

### 6.3 时间预估（并行后）

| 阶段 | 工作日（单人） | 并行后实际墙钟 |
|------|----------------|----------------|
| 0 | 1 | 1 |
| 1 | 2 | 与 0 并行 → 2 |
| 2 | 1 | 1 |
| 3 | 0.5 | 0.5 |
| 4 | 8 context × ~3 天 = 24 | 8 路并行 → 3 |
| 5 | 1 | 1 |
| **合计** | 29.5 工作日 | **~8.5 工作日 ≈ 2 周** |

---

## 7. 验收标准

- [ ] `internal/*/domain/**` 不出现任何第三方 import（stdlib + pkg/constants 除外），go-arch-lint 通过
- [ ] `internal/*/application/**` 不出现 `pgx` / `redis` / `nats` / `gin`，go-arch-lint 通过
- [ ] 跨 context import 仅出现在 `internal/<other>/domain/port`，go-arch-lint 通过
- [ ] `pkg/**` 不出现 `internal/**` import
- [ ] `agent → memory/pipeline`、`config → knowledge` 等反向依赖全部消除
- [ ] 契约测试 `api/http/contract_test.go` 100% 通过，golden 数据 diff = 0
- [ ] `go test -short ./...` 全绿
- [ ] `go test -race -timeout 30s ./...` 全绿
- [ ] 端到端集成测试（NATS + Postgres + Milvus + Redis）全绿
- [ ] `api/http/router.go` ≤ 100 行
- [ ] `cmd/server/main.go` ≤ 30 行
- [ ] domain 层单测覆盖率 ≥ 90%；application 层 ≥ 80%
- [ ] 前端 `web/` 零改动且功能正常
- [ ] Production 环境 staging 灰度 24h 无回归

---

## 8. 不在本 spec 范围（明确排除）

- 数据库 schema 变更（保持现 DDL）
- NATS subject / Milvus collection / Redis key 命名变更
- HTTP API 路径 / 请求体 / 响应体 / 错误码变更
- 前端 `web/` 改动
- 业务功能新增（重构期间冻结）
- 性能优化（除非接口化导致明显劣化才调整）
- 监控指标命名变更
