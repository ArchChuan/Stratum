# API Development Rules

## Route Registration

所有路由集中注册于 `api/http/router.go`，按域拆分为独立私有函数，禁止在 handler 文件中散落注册。

```go
// router.go 中注册顺序
registerAuth(r, c, requireActive)
registerHealth(r, c)
registerSkills(r, c, requireActive)
registerEvaluations(r, c, requireActive)
registerAgents(r, c, requireActive)
registerKnowledge(r, c, requireActive)
registerMCP(r, c, requireActive)
registerMemory(r, c, requireActive)
```

## Complete Route List

### 无需认证

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/health` | 服务健康检查，返回 `{"status":"ok","service":"Stratum"}` |
| GET | `/metrics` | Prometheus scrape 端点 |
| GET | `/models` | 列出所有可用 LLM 模型 |

### Auth（GitHub OAuth，需配置 `GITHUB_CLIENT_ID`）

| 方法 | 路径 | Handler |
|------|------|---------|
| GET | `/auth/github` | GitHubLogin |
| GET | `/auth/github/callback` | GitHubCallback |
| POST | `/auth/register` | Register（邮箱注册） |
| POST | `/auth/guest` | GuestLogin（临时访客） |
| POST | `/auth/refresh` | Refresh（刷新 JWT） |
| POST | `/auth/logout` | Logout |
| GET | `/auth/me` | Me（当前用户信息） |
| POST | `/auth/switch-tenant` | SwitchTenant（切换租户，重新签发 JWT） |
| POST | `/auth/create-tenant` | CreateUserTenant（用户创建自己的租户） |

### Admin（JWT + global_admin 角色）

| 方法 | 路径 | Handler |
|------|------|---------|
| GET | `/admin/tenants` | ListTenants |
| POST | `/admin/tenants` | CreateTenant |
| GET | `/admin/tenants/:id` | GetTenant |
| PATCH | `/admin/tenants/:id` | UpdateTenant |
| DELETE | `/admin/tenants/:id` | DeleteTenant |

### Tenant（JWT + member 角色）

| 方法 | 路径 | Handler | 额外权限 |
|------|------|---------|---------|
| GET | `/tenant/members` | ListMembers | — |
| PATCH | `/tenant/members/:user_id/role` | UpdateMemberRole | — |
| DELETE | `/tenant/members/:user_id` | RemoveMember | — |
| GET | `/tenant/settings` | GetSettings | — |
| PATCH | `/tenant/settings` | UpdateSettings | requireActive |
| PATCH | `/tenant/embed-model` | SetEmbedModel | requireActive |
| DELETE | `/tenant` | DeleteSelf | owner |
| GET | `/tenant/list` | ListUserTenants | — (仅 JWT) |

### Skill（JWT + tenant context）

| 方法 | 路径 | Handler | 额外权限 |
|------|------|---------|---------|
| GET | `/skills` | GetAllSkills | — |
| POST | `/skills/test-draft` | ExecuteDraftSkill | requireActive |
| POST | `/skills` | CreateSkill | admin + requireActive |
| GET | `/skills/:id` | GetSkill | — |
| PUT | `/skills/:id` | UpdateSkill | admin + requireActive |
| DELETE | `/skills/:id` | DeleteSkill | admin + requireActive |
| POST | `/skills/:id/test` | ExecuteSkill（沙箱测试）| requireActive |
| GET | `/skills/:id/workspace` | GetSkillWorkspace（版本草稿工作区）| — |
| PATCH | `/skills/:id/draft/capability` | UpdateDraftCapability | admin + requireActive |
| PATCH | `/skills/:id/draft/contract` | UpdateDraftContract | admin + requireActive |
| PATCH | `/skills/:id/draft/implementation` | UpdateDraftImplementation | admin + requireActive |
| POST | `/skills/:id/publish` | PublishSkill（草稿→已发布版本）| admin + requireActive |

### Agent（JWT + tenant context）

| 方法 | 路径 | Handler | 额外权限 |
|------|------|---------|---------|
| GET | `/agents` | GetAllAgents | — |
| POST | `/agents` | CreateAgent | admin + requireActive |
| GET | `/agents/executions` | ListExecutions | — |
| GET | `/agents/executions/:traceID/tool-traces` | ListExecutionToolTraces | — |
| GET | `/agents/executions/:traceID/trace-events` | ListExecutionTraceEvents | — |
| GET | `/agents/:id` | GetAgent | — |
| POST | `/agents/:id/execute` | ExecuteAgent | requireActive + rate limit |
| POST | `/agents/:id/execute/stream` | ExecuteAgentStream（SSE）| requireActive + rate limit |
| PUT | `/agents/:id` | UpdateAgent | admin + requireActive |
| DELETE | `/agents/:id` | DeleteAgent | admin + requireActive |
| POST | `/agents/:id/conversations` | CreateConversation | — |
| GET | `/agents/:id/conversations` | ListConversations | — |

### Evaluation（JWT + tenant context）

| 方法 | 路径 | Handler | 额外权限 |
|------|------|---------|---------|
| POST | `/evaluations/suites` | CreateSuite | admin + requireActive |
| POST | `/evaluations/suites/:id/publish` | PublishSuite | admin + requireActive |
| POST | `/evaluations/runs` | EnqueueRun | admin + requireActive |
| GET | `/evaluations/runs/:id` | GetRun | admin |
| GET | `/evaluations/jobs/:id` | GetJob | admin |
| POST | `/evaluations/optimizations` | GenerateOptimization | admin + requireActive |
| POST | `/evaluations/experiments` | CreateExperiment | admin + requireActive |
| POST | `/evaluations/experiments/:id/evaluate` | EvaluateExperiment | admin + requireActive |
| POST | `/evaluations/feedback` | RecordFeedback | member + requireActive |

### Conversations（JWT + tenant context）

| 方法 | 路径 | Handler |
|------|------|---------|
| PATCH | `/conversations/:convID` | RenameConversation |
| DELETE | `/conversations/:convID` | DeleteConversation |
| GET | `/conversations/:convID/messages` | ListMessages |
| POST | `/conversations/:convID/messages` | AddMessage |

### Knowledge / RAG（JWT + tenant context + member 角色）

| 方法 | 路径 | Handler | 额外权限 |
|------|------|---------|---------|
| GET | `/knowledge/workspaces` | ListWorkspaces | — |
| GET | `/knowledge/workspaces/:name/stats` | GetWorkspaceStats | — |
| GET | `/knowledge/workspaces/:name/documents` | ListDocuments | — |
| POST | `/knowledge/query` | Query | requireActive |
| POST | `/knowledge/workspaces` | CreateWorkspace | admin + requireActive |
| PATCH | `/knowledge/workspaces/:name` | UpdateWorkspace | admin + requireActive |
| DELETE | `/knowledge/workspaces/:name` | DeleteWorkspace | admin + requireActive |
| POST | `/knowledge/ingest` | UploadDocument | admin + requireActive |

### Memory（JWT + tenant context + requireActive）

| 方法 | 路径 | Handler | 额外权限 |
|------|------|---------|---------|
| DELETE | `/memory/clear` | ClearMemories | — |
| POST | `/memory` | AddMemory | — |
| GET | `/memory/:id` | GetMemory | — |
| POST | `/memory/sessions` | ListSessions | — |
| GET | `/memory/stats` | GetStats | — |
| GET | `/memory/summary/:session_id` | GetSummary | — |
| DELETE | `/memory/:id` | DeleteMemory | — |
| DELETE | `/memory/session/:session_id` | ClearSession | — |

### MCP（JWT + tenant context，由 `MCPHandler.RegisterRoutes` 动态注册）

MCP 路由在 `api/http/handler/mcp_handler.go` 中的 `RegisterRoutes` 方法定义。所有路由至少需要 member；tool execute 追加 requireActive；server connect/update/disconnect/delete config/reconnect 与 skill refresh 需要 admin + requireActive。

主要路径：`/mcp/servers`、`/mcp/servers/:id`、`/mcp/servers/:id/tools`、`/resources`、`/config`、`/reconnect`、`/mcp/tools/:toolId/execute`、`/mcp/skills`、`/mcp/skills/refresh`、`/mcp/status`。

## Handler Writing Standards

### File Locations

```
api/http/handler/   ← handler 实现（每域一个文件）
api/http/dto/       ← Request/Response 结构体（无业务逻辑）
```

### Struct Pattern

```go
type AgentHandler struct {
    svc    *application.AgentService
    logger *zap.Logger
}

func NewAgentHandler(svc *application.AgentService, logger *zap.Logger) *AgentHandler {
    return &AgentHandler{svc: svc, logger: logger}
}
```

### Request/Response

- DTO 定义于 `api/http/dto/`，与 handler 分离
- 绑定：`c.ShouldBindJSON(&req)`，失败 `c.Error(err)` → ErrorHandler 返回 400
- 错误必须通过 `c.Error(err)` 传给 ErrorHandler，**不要** 在 handler 内直接 `c.JSON` 错误
- 成功：`c.JSON(http.StatusOK, resp)` 或 `c.JSON(http.StatusCreated, resp)`

### HTTP 状态码约定

| HTTP 状态 | 场景 |
|-----------|------|
| 200 | 查询/更新/执行成功 |
| 201 | 创建成功 |
| 400 | 请求参数非法 |
| 401 | 未认证 |
| 403 | 无权限（RequireGlobalAdmin / RequireTenantRole 拒绝）|
| 404 | 资源不存在（domain.ErrNotFound） |
| 409 | 资源冲突（domain.ErrNameConflict） |
| 423 | 租户未激活（RequireActiveTenant 拒绝）|
| 500 | 内部错误 |

## Middleware

注册顺序（`NewRouter` 中）：

```
gin.Recovery → BodyLimit → ErrorHandler → otelgin.Middleware → TraceMiddleware → SecurityHeaders → CORSMiddleware → MetricsMiddleware → Routes
```

| 文件 | 功能 |
|------|------|
| `middleware.go` | `ErrorHandler`（domain error → HTTP）、`CORSMiddleware` |
| `trace.go` | `TraceMiddleware`：OTEL Span 注入，输出结构化访问日志 |
| `metrics.go` | `MetricsMiddleware`：Prometheus HTTP 指标收集 |
| `jwt.go` | `JWTMiddleware`：RS256 验证，Claims 注入 context |
| `inject_tenant.go` | `InjectTenantContext`：从 Claims 提取 tenant_id，切换 pg schema |
| `require_role.go` | `RequireGlobalAdmin()` / `RequireTenantRole(role)` |
| `require_active_tenant.go` | `RequireActiveTenant`：租户状态激活检查 |
| `error_mapping.go` | domain sentinel → HTTP status code 映射表 |

## New Endpoint Checklist

1. 在 `api/http/dto/` 中定义 Request/Response 结构体（加 binding tag）
2. 在 `api/http/handler/` 对应文件中实现 handler 方法（≤15 行/方法）
3. 在 `api/http/router.go` 对应 `registerXxx` 函数中注册路由（指定正确 middleware 链）
4. domain sentinel 错误在 `api/middleware/error_mapping.go` 中添加映射规则
5. 运行 `go build ./...` 验证编译
6. 按 `api/http/handler/tenant_handler_test.go` 模式编写 handler 测试
7. 若 API 对外，更新 `api/http/testdata/contracts/*.golden.json`
