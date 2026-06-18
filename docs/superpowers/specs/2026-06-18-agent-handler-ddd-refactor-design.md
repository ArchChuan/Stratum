# Agent Handler DDD 重构 — 设计文档

**日期**:2026-06-18
**范围**:`api/http/handler/agent_handler.go` · `agent_crud_handler.go` · `agent_exec_handler.go`(共 832 行)+ 配套 wiring/router/test
**目标**:消除 handler 层全部架构违规,把跨租户 LLM Gateway 解析、Capability Gateway 热替、执行记录写入、技能/MCP 工具元数据查询等业务编排下沉到 application/infrastructure。

---

## 1. 背景与问题

### 现状违规清单

| 文件 | 行 | 违规 |
|---|---|---|
| agent_handler.go | 11 | `import internal/llmgateway/infrastructure` |
| agent_handler.go | 15 | `import pkg/tenantdb`(shim,phase 5 待移除) |
| agent_handler.go | 27-29 | 字段 `db PgxPool` · `aesKey []byte` · `gatewayCache *llmgateway.TenantGatewayCache` |
| agent_handler.go | 154-227 | `resolveTenantGateway`:SQL 查 tenant_settings → AES 解密 → 构造 Gateway → TTL 缓存 |
| agent_handler.go | 230-258 | `buildExtraTools`:raw SQL 查 `tenant_xxx.skills` 拿 name/description |
| agent_crud_handler.go | 92-103 | `h.db.QueryRow` 查 tenant settings 继承 embed_model |
| agent_exec_handler.go | 14,16 | `import agent/infrastructure/capability` · `internal/llmgateway/infrastructure` |
| agent_exec_handler.go | 18 | `import pkg/tenantdb` |
| agent_exec_handler.go | 183-219 | `assembleOptions`:整段执行编排(GW 解析 + capGW 热替 + chatStore 注入 + extraTools + RAGFn) |
| agent_exec_handler.go | 236-245 | `swapCapabilityGateway`:接 infra 类型 `*llmgateway.Gateway`,调 `capgateway.NewLLMAdapter` |
| agent_exec_handler.go | 260-289 | `recordExecution`:用 `tenantdb.WithTenant` 重组 ctx 异步写库 |
| router.go | 136-149 | `NewAgentHandler` 12 参,半数是 infra/SQL/AES key |

### 根因

`AgentHandler` 把"如何按租户解析 LLM Gateway"和"如何把它注入到 agent 执行链路"两类编排逻辑直接写在了 transport 层。这两类逻辑都属于 application 用例编排,而不是 HTTP 解析/响应组装。

---

## 2. 目标架构

### 2.1 总体分层

```
api/http/handler/agent_*.go   (transport: bind → svc.* → render,SSE flusher 留这里)
        ↓
internal/agent/application/
  agent_service.go            (新增,聚合 CRUD + Execute + Stream + ListExecutions)
  agent.go / registry.go ...  (现有不动)
        ↓
internal/agent/domain/port/
  tenant_settings.go          (新)消费者侧 port
  tenant_gateway.go           (新)
  capability_factory.go       (新)
  rag_search.go               (新)
  skill_tool_meta.go          (新)
  llm_gateway.go              (新)minimal interface
        ↓
api/wiring/agent.go           (composition root,所有 adapter 在这里)
```

### 2.2 AgentService(`internal/agent/application/agent_service.go`)

```go
type AgentService struct {
    registry   *Registry
    execStore  ExecutionStore
    chatStore  ChatStore
    settings   port.TenantSettingsReader
    gateways   port.TenantGatewayProvider
    capFactory port.CapabilityGatewayFactory
    ragSearch  port.RAGSearchProvider
    mcpTools   port.MCPToolProvider          // 已有
    skillMeta  port.SkillToolMetadata
    metrics    observability.MetricsProvider
    logger     *zap.Logger
}

// CRUD / Lifecycle
func (s *AgentService) ListAgents(ctx context.Context) []AgentView
func (s *AgentService) GetAgent(ctx context.Context, id string) (AgentView, error)
func (s *AgentService) CreateAgent(ctx context.Context, tenantID string, req CreateAgentInput) (AgentView, error)
func (s *AgentService) UpdateAgent(ctx context.Context, id string, req UpdateAgentInput) (AgentView, error)
func (s *AgentService) DeleteAgent(ctx context.Context, id string) error
func (s *AgentService) ListExecutions(ctx context.Context, page, size int) ([]ExecutionView, int, error)

// Execute
type ExecCtx struct {
    TenantID, UserID, TraceID, ConversationID string
}
type ExecOpts struct {
    MaxSteps int
    Timeout  time.Duration
}

func (s *AgentService) Execute(
    ctx context.Context, agentID, query string, opts ExecOpts, ec ExecCtx,
) (*AgentResult, error)

func (s *AgentService) ExecuteStream(
    ctx context.Context, agentID, query string, opts ExecOpts, ec ExecCtx,
    tokenCb func(token string),
) (*AgentResult, error)
```

`Execute / ExecuteStream` 内部完成:`gateways.ResolveForTenant` → `capFactory.Build` 热替 → `attachChatStore` → 装配 `extraTools` → 装配 `RAGFn` → 调用 `agent.Execute` → `execStore.Insert` 异步落库。两条路径共享 `assembleExecutionOptions(...)` 私有方法。

`AgentView` / `ExecutionView` 是 application 输出 DTO,handler 直接映射到 HTTP DTO。

### 2.3 新增 Ports(`internal/agent/domain/port/`)

> 全部消费者侧定义,domain 层零第三方依赖(stdlib only)。

```go
// llm_gateway.go — 消费方仅需的最小方法集
type LLMGateway interface {
    // 实际方法集由 Execute 路径需要的能力决定;
    // *llmgateway.Gateway 自动满足。
    // 占位:具体方法在实现阶段照实际 call site 定义。
}

// tenant_settings.go
type TenantSettings struct {
    LLMAPIKeys map[string]string  // 已解密明文,仅在内存
    EmbedModel string
}
type TenantSettingsReader interface {
    Read(ctx context.Context, tenantID string) (TenantSettings, error)
}

// tenant_gateway.go
type TenantGatewayProvider interface {
    ResolveForTenant(ctx context.Context, tenantID string) (LLMGateway, map[string]string, bool)
    InvalidateTenant(tenantID string)  // tenant API key 更新时用
}

// capability_factory.go
type CapabilityGatewayFactory interface {
    Build(gw LLMGateway) CapabilityGateway   // CapabilityGateway 已有
}

// rag_search.go
type RAGSearchFn func(ctx context.Context, q string, topK int) ([]RAGSnippet, error)
type RAGSearchProvider interface {
    SearchFn(tenantID string) RAGSearchFn
}

// skill_tool_meta.go
type SkillToolDescriptor struct {
    ID, Name, Description string
}
type SkillToolMetadata interface {
    Lookup(ctx context.Context, ids []string) ([]SkillToolDescriptor, error)
}
```

### 2.4 Adapters(`api/wiring/agent.go`)

| Port | Adapter | 内部依赖 |
|---|---|---|
| TenantSettingsReader | `wiringTenantSettings` | `*pgxpool.Pool` + `aesKey` + `pkgcrypto.Decrypt` |
| TenantGatewayProvider | `wiringTenantGateway` | `*llmgateway.TenantGatewayCache` + `TenantSettingsReader` + factory func |
| CapabilityGatewayFactory | `wiringCapFactory` | `port.Adapter`(skill) + `logger` + `capgateway.NewLLMAdapter` |
| RAGSearchProvider | `wiringRAGSearch` | `*knowledge.RAGService`(已有 `NewRAGSearchFn`) |
| SkillToolMetadata | `internal/skill/infrastructure/persistence/skill_tool_meta_repo.go` | `*pgxpool.Pool` + `tenantdb.ExecTenant` |

`wiringTenantGateway.ResolveForTenant` 行为与现 `resolveTenantGateway` 等价:cache → settings.Read → 拼 Gateway → cache.Set。

### 2.5 Handler 退化目标

```go
type AgentHandler struct {
    svc    *agent.AgentService
    logger *zap.Logger
}

func NewAgentHandler(svc *agent.AgentService, logger *zap.Logger) *AgentHandler

// agent_exec_handler.go — SSE 仍由 handler 处理 transport
func (h *AgentHandler) ExecuteAgentStream(c *gin.Context) {
    tenantID, _ := tenantIDFromCtx(c); userID, _ := userIDFromCtx(c)
    var req ExecuteAgentRequest; _ = c.ShouldBindJSON(&req)

    // SSE headers + flusher + heartbeat (transport-level, 留在 handler)
    ...

    tokenCb := func(token string) {
        payload, _ := json.Marshal(map[string]string{"token": token})
        writeEvent(string(payload))
    }
    result, err := h.svc.ExecuteStream(execCtx, id, req.Query,
        toExecOpts(req), agent.ExecCtx{
            TenantID: tenantID, UserID: userID,
            TraceID: middleware.GetTraceID(c),
            ConversationID: req.ConversationID,
        }, tokenCb)
    ...
}
```

### 2.6 行数预估

| 文件 | 现 | 后 |
|---|---|---|
| agent_handler.go | 260 | ~80 |
| agent_crud_handler.go | 283 | ~140 |
| agent_exec_handler.go | 289 | ~150 |
| **新** internal/agent/application/agent_service.go | — | ~280 |
| **新** internal/agent/domain/port/*.go(6 个) | — | ~80 |
| **新** internal/skill/infrastructure/persistence/skill_tool_meta_repo.go | — | ~60 |
| **新** api/wiring/agent.go(adapters) | — | ~200 |

净增 ~100 行;违规归零;handler 可仅 mock `AgentService` 完成单测。

---

## 3. 数据流(Execute 全链路)

```
HTTP POST /agents/:id/execute
  ↓ Gin
handler.ExecuteAgent
  ↓ bind + 取 tenantID/userID/traceID
svc.Execute(ctx, id, query, opts, ExecCtx{...})
  ├── gateways.ResolveForTenant(ctx, tenantID)
  │     ├── cache.Get → hit 直接返回
  │     └── miss: settings.Read → factory 拼 Gateway → cache.Set
  ├── capFactory.Build(gw) → swap 进 agent
  ├── attachChatStore(agent)
  ├── 装配 ExecutionOption: WithMaxSteps · WithLLMAPIKeys · WithTenantID · WithTraceID · WithUserID · WithConversationID · WithExtraTools(mcpTools+skillMeta) · WithRAGSearchFn(rag.SearchFn(tenantID))
  ├── agent.Execute(execCtx, query, options...)
  └── execStore.Insert(异步,fire-and-forget)
  ↓
handler 返回 AgentExecutionResult JSON
```

Stream 路径相同,仅多 `WithTokenCallback(tokenCb)`,且 ctx 派生 `llmgateway.WithGateway(ctx, gw)` 由 service 完成(handler 不感知 infra 类型)。

---

## 4. 错误处理

- Domain `Err*`:`agent.ErrNotFound`(已有)、新增 `agent.ErrInvalidConfig` 用于 Update 校验
- Infrastructure adapter 翻译:`pgconn.PgError → domain.Err*`;tenant_settings 全空 → 返回 `(zero, nil)` 表示"用全局 fallback gateway"
- Handler:`c.Error(err)` 交给 ErrorHandler 中间件统一映射 HTTP code

---

## 5. 测试策略

| 层 | 测试 | mock 目标 |
|---|---|---|
| handler | `agent_handler_test.go` 重写 | `AgentService` 接口(新建 mock) |
| application | 新建 `agent_service_test.go` | 6 个 port 接口(全 mock) |
| infrastructure | `skill_tool_meta_repo_test.go` 用 testdouble PgxPool | — |
| wiring adapter | 表驱动测 `wiringTenantGateway.ResolveForTenant` cache hit/miss | mock TenantSettingsReader |
| 契约 | `api/http/contract_test.go` + golden 文件不动 | — |
| 现 `build_extra_tools_test.go` | 拆为 service-level + skill-meta-repo 两份 | — |

`-race` 必须通过,特别注意 `recordExecution` 异步 goroutine 与 `gatewayCache` 并发。

---

## 6. 兼容性 / 迁移风险

- 路由签名不变,前端 0 改动
- HTTP DTO 字段不变,响应 JSON shape 不变 → 契约 golden 不需更新
- `tenantdb` shim 仍然由 application/infrastructure 内部消费,本次不动它(phase 5 单独处理)
- `*llmgateway.TenantGatewayCache` 仍由 wiring 持有并实现 `TenantGatewayProvider`,IAM 的 `TenantService.cache` 引用不受影响
- `wiring/router.go` 唯一改动:`NewAgentHandler(c.Agent.Service, c.Logger)`(2 参)

---

## 7. 阶段拆解(实施顺序提示,具体 plan 由 writing-plans 产出)

1. 建 ports + LLMGateway minimal interface(domain 零依赖,先编译通过)
2. 建 application service + DTO + 单测 mock
3. 建 wiring adapters,跑通编译
4. router 切换 + 旧 handler 拆解为 transport-only
5. 测试迁移:重写 handler test,拆 build_extra_tools_test
6. golangci-lint(depguard + gofmt)+ `-race` 全绿
7. 删除旧 `resolveTenantGateway` / `swapCapabilityGateway` / `recordExecution` 残留代码

---

## 8. 关键决策(已确认)

- `AgentService` 单一聚合,不拆 Lifecycle/Execution
- `tenantdb.FromContext` 在 application 层允许使用(它是 storage primitive,不在 application 禁运清单);仅 handler 禁用
- `LLMGateway` minimal interface 放 `internal/agent/domain/port/llm_gateway.go`,`*llmgateway.Gateway` 自动满足
- SSE flusher / heartbeat / Header 留在 handler(transport 关心,非业务编排)
- 本次 **不动** `pkg/tenantdb` shim 本身

---

## 9. 不在本次范围

- `auth_handler.go` 的 `pgxpool` 直接依赖(用户在 IDE 打开,但属于 IAM ctx,独立任务处理)
- `pkg/tenantdb` shim 删除(phase 5 单独 PR)
- `agent_crud_handler.go` 的输入校验加固(只搬移现有逻辑,不顺手扩功能)
