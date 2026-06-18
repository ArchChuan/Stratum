# Stratum HTTP Handler 全量 DDD 重构 — 设计文档

**日期**:2026-06-18
**范围**:`api/http/handler/` 全部 21 个非测试文件(共 4 827 行)+ 配套 `api/wiring/` adapter + `router.go` + 全部 `*_test.go` 同步迁移
**目标**:消除 handler 层全部架构违规(`internal/*/infrastructure` import、`pgxpool` 直接持有、raw SQL、AES 加解密、业务编排、`tenantdb` 直引、context.Background 创建、跨域 application 拼装),把所有用例编排下沉到对应 bounded context 的 `application` 层,handler 退化为纯 transport(bind → svc → render)。

---

## 1. 背景与全集扫描

### 1.1 扫描方法

`api/http/handler/` 内 21 个非测试文件全部扫描:

- import 块(查 `internal/*/infrastructure`、`pgx*`、`milvus*`、`go-redis`、`pkg/tenantdb`)
- 字段声明(查 `pgxpool.Pool` / `*pgxpool.Pool` / 加密 key / TTL cache)
- 方法体(查 `QueryRow` / `Query(` / `Exec(` / `Decrypt` / `Encrypt` / `context.Background`)
- 错误响应(查内联 `c.JSON(http.StatusXxx, dto.ErrorResponse{...})` 与 `_ = c.Error(middleware.NewHTTPError(...))` 共存)
- 方法长度(`func (h *Handler) Xxx` 块 > 30 行视为编排过重)

### 1.2 全 handler 违规矩阵

| # | 文件 | 行 | 方法数 | 违规类别 | 关键证据 |
|---|---|---|---|---|---|
| 1 | `admin_handler.go` | 130 | 5 | **直引 `pgxpool`/`pgx`/`pgconn`**;为他人定义 `PgxPool` interface | L17,L24-31 接口 + `var _ PgxPool = (*pgxpool.Pool)(nil)` |
| 2 | `agent_handler.go` | 172 | 1 + helpers | **持 `db PgxPool`**;raw SQL 查 tenant `skills`;**直引 `pkg/tenantdb`**;`buildExtraTools` 业务编排 | L155-158 `h.db.QueryRow`;L156 `tenantdb.FromContext` |
| 3 | `agent_crud_handler.go` | 283 | 6 | **`h.db.QueryRow` 查 tenant_settings 继承 embed_model**;CreateAgent 73 行编排 | L92-103 |
| 4 | `agent_exec_handler.go` | 282 | 5 | **直引 `agent/infrastructure/capability`** + **`pkg/tenantdb`**;`assembleOptions` 99 行整段编排;`recordExecution` 用 `tenantdb.WithTenant(context.Background(), tc)` 异步落库 | L14,L18,L43,L260,L276,L754 |
| 5 | `auth_handler.go` | 84 | 0 公开方法(纯 helper + Deps 容器) | `AuthHandlerDeps` 持 `iamport.RefreshTokenStore` `JWTService` `OnboardSvc` `SchemaProvisioner` `GitHubClient` 等 9 项 | L22-33 — Deps 是 application + domain/port 混合,字段过粗 |
| 6 | `auth_oauth_handler.go` | 153 | 2 | **`GitHubCallback` 119 行**:含 OAuth state 校验、code 交换、用户查询、auto-join、global-admin 判定、token 签发 6 类业务分支 | L1316:119 |
| 7 | `auth_register_handler.go` | 105 | 1 | `Register` 89 行;**直引 `pkg/tenantdb`**(commit history,本次 head 已无,但 deps 仍透传 `SchemaProvisioner` port,可保留) | L1451:89 |
| 8 | `auth_session_handler.go` | 123 | 3 | `Refresh` 64 行 — token 黑名单查询、claims 校验、role 同步、新对签发全在 handler | L1556:64 |
| 9 | `auth_tenant_handler.go` | 114 | 2 | `SwitchTenant` 50 行 + `CreateUserTenant` 45 行 — JWT 解析、成员校验、provision schema、token 签发全混 | L1678/L1732 |
| 10 | `chat_handler.go` | 233 | 6 | `AddMessage` 47 行:消息持久化 + 触发 agent 执行 — 跨 ctx 编排;handler 直接拼请求字段 | L2074:47 |
| 11 | `mcp_handler.go` | 182 | 12 | 比较干净;但 `ConnectServer` 直接接 `*mcpdomain.ServerConfig` 全字段,handler 做了字段合法性默认填充 | L155 |
| 12 | `memory_handler.go` | 76 | 1 | 仅作 `MemoryHandler` 容器(无方法)+ 包级 `errUnauthorized` | L20 |
| 13 | `memory_entity_handler.go` | 104 | 2 | `ExtractEntities` 50 行 — 校验 + 限流 + 调 service + 拼响应 | L2879:50 |
| 14 | `memory_message_handler.go` | 200 | 4 | `SearchMemory` 75 行;`AddMemory` 57 行 — 输入归一化 + 可选字段填充全在 handler | L3101:75 |
| 15 | `memory_session_handler.go` | 145 | 3 | `CreateSession` 54 行 + `GetStats` 39 行 + `ClearSession` 33 行 | L3220 |
| 16 | `memory_summary_handler.go` | 54 | 1 | `GetSummary` 40 行;**直引 `internal/memory/domain`**(用于 errors.Is) — domain 引用 OK,但保持注意 | L3364 |
| 17 | `model_handler.go` | 25 | 1 | 干净 ✓ | — |
| 18 | `rag_handler.go` | 291 | 7 | `Query` 59 行;`UploadDocument` 33 行;**直引 `internal/skill/domain`** 仅取常量 `DefaultTopK`(可,但其实属于 knowledge ctx) | L113 |
| 19 | `skill_handler.go` | 137 | 5 | 已迁移 c.Error 干净 ✓ — 现状基本合规 | — |
| 20 | `tenant.go` | 44 | 0 | helper 文件,**直引 `pkg/tenantdb`**:`tenantdb.FromContext`;同时定义 `tenantIDFromCtx` / `respondMissingTenant` 给所有 handler 用 | L8,L13 |
| 21 | `tenant_handler.go` | 260 | 8 | `ListMembers` 32 行 + `InviteMember` 36 行 + `UpdateMemberRole` 45 行 — 编排适度,但内联校验逻辑可下沉 | L4356,L4390,L4428 |

### 1.3 八大违规模式归纳

| 模式 | 出现文件 |
|---|---|
| **A. handler 持 `pgxpool`/`PgxPool`** | admin, agent_crud, agent_handler |
| **B. handler 直引 `internal/*/infrastructure`** | agent_exec(capability), 历史上 agent_handler/agent_exec(llmgateway, 已部分清理) |
| **C. handler 直引 `pkg/tenantdb`** | tenant.go, agent_handler, agent_exec |
| **D. handler 内 raw SQL** | admin(部分), agent_crud, agent_handler |
| **E. handler 内 AES 加解密 / 密钥持有** | 已被 agent ctx 重构清掉,**不复发**(规范化时禁用) |
| **F. 跨 ctx application 直拼** | chat_handler(agent + memory), agent_exec(agent + knowledge), auth_tenant_handler(iam + provisioning) |
| **G. handler 内 `context.Background()` 创建脱钩 ctx** | agent_exec(L43, L276) |
| **H. handler 内单方法 > 30 行的业务编排** | auth_oauth(119), auth_register(89), memory_search(75), agent_crud_create(73), agent_exec_stream(99/54), auth_session_refresh(64) — 共 16 处 |

### 1.4 现存合规正例(规范基线)

- `model_handler.go` 25 行 / 1 方法:`bind → svc.Catalogue → c.JSON` — **黄金参考**
- `skill_handler.go` 137 行 / 5 方法:全部 `c.Error(svc.Xxx(...))` 走 ErrorHandler 中间件,无内联 `c.JSON(http.StatusXxx, dto.ErrorResponse{...})`

---

## 2. 目标架构

### 2.1 总体分层契约(本次重构后强制)

```
api/http/handler/*.go                      transport-only:
                                           bind → svc.* → render(c.JSON 或 c.Error)
                                           SSE flusher / heartbeat / Header 仍在 handler
        │
        ▼
internal/<ctx>/application/*.go            用例编排(已存在,本次扩充)
        │
        ▼
internal/<ctx>/domain/port/*.go            消费者侧接口
        │
        ▼
api/wiring/*.go                            adapter(infrastructure 实现 port,装配进 Container)
```

**handler 强制黑名单**(本次起 depguard 锁死):

```
× internal/*/infrastructure/...
× internal/*/infrastructure/<adapter>/...
× pkg/tenantdb
× pkg/storage/postgres
× github.com/jackc/pgx/...
× github.com/jackc/pgx/v5/pgxpool
× github.com/jackc/pgx/v5/pgconn
× github.com/redis/go-redis/...
× github.com/milvus-io/milvus-sdk-go/...
× github.com/byteBuilderX/stratum/pkg/crypto  // AES 加解密禁用
```

**handler 强制白名单**:

```
✓ api/http/dto
✓ api/middleware
✓ internal/<ctx>/application
✓ internal/<ctx>/domain                     // 仅类型/sentinel error,禁止类型方法导致的隐性依赖
✓ internal/<ctx>/domain/port                // 仅当 handler 真的要持有 port(罕见,优先经 application service)
✓ pkg/constants
✓ pkg/observability                         // 仅作为 logger / metrics 的类型注解
✓ github.com/gin-gonic/gin
✓ go.uber.org/zap
✓ github.com/google/uuid                    // 仅 ID 生成
✓ stdlib
```

### 2.2 按 bounded context 的重构动作

#### 2.2.1 agent context(本次主战场)

延用上一版 spec 的设计:

- **新增** `internal/agent/application/agent_service.go`:`AgentService` 聚合 CRUD + Execute + Stream + ListExecutions
- **新增 ports**(`internal/agent/domain/port/`):`tenant_settings.go` · `tenant_gateway.go` · `capability_factory.go` · `rag_search.go` · `skill_tool_meta.go` · `llm_gateway.go`(minimal interface)
- **handler 退化**:`agent_handler.go` ~80 / `agent_crud_handler.go` ~140 / `agent_exec_handler.go` ~150
- **wiring**:`api/wiring/agent.go` 收 `wiringTenantSettings` / `wiringTenantGateway` / `wiringCapFactory` / `wiringRAGSearch`
- **新 infra**:`internal/skill/infrastructure/persistence/skill_tool_meta_repo.go`

详见 spec 上一版 §2.2-2.5;此处不重复。

#### 2.2.2 iam context(auth + admin + tenant)

| handler | 现状违规 | 目标动作 |
|---|---|---|
| `admin_handler.go` | 直引 `pgx/pgxpool/pgconn`,自定义 `PgxPool` interface | 移除 `pgx*` import;`PgxPool` interface 整体迁到 `internal/iam/domain/port/admin_db.go` 或干脆删除(已有 `iamapp.AdminService`,handler 仅调 service);`tenantToDTO` 留 handler |
| `auth_handler.go` | `AuthHandlerDeps` 9 字段混杂 application + domain/port | 重构为 `AuthService`(已部分存在,需扩):`AuthService` 暴露 `IssueTokenPair / SetRefreshCookie` 业务能力;handler 持单一 `*application.AuthService` + helper |
| `auth_oauth_handler.go` | `GitHubCallback` 119 行 6 类业务分支 | **整段下沉**到 `iamapp.OAuthService.HandleGitHubCallback(ctx, code, state) (TokenPair, RedirectInfo, error)`;handler 仅 SetCookie + Redirect |
| `auth_register_handler.go` | `Register` 89 行 | 整段下沉到 `iamapp.OnboardService.RegisterFromOnboardingToken(ctx, input) (TokenPair, TenantID, error)`;handler 仅 bind + render |
| `auth_session_handler.go` | `Refresh` 64 行 | 下沉到 `iamapp.SessionService.Refresh(ctx, rawRT) (TokenPair, error)` |
| `auth_tenant_handler.go` | `SwitchTenant` / `CreateUserTenant` 各 ~50 行 | 下沉到 `iamapp.TenantSwitchService` / `iamapp.UserTenantService` |
| `tenant.go` | 直引 `pkg/tenantdb`(`tenantIDFromCtx` helper) | helper 改为依赖 `api/middleware.GetTenantID(c)`(已有);删除 `pkg/tenantdb` 引用;`respondMissingTenant` 改用 `c.Error(middleware.NewHTTPError(401, errMissingTenant))` |
| `tenant_handler.go` | 内联校验 36/45 行 | `iamapp.TenantMemberService.{List,Invite,UpdateRole}` 进一步收口字段校验 |

#### 2.2.3 memory context

| handler | 现状 | 目标动作 |
|---|---|---|
| `memory_handler.go` | 干净(容器) | 不动 |
| `memory_message_handler.go` | `SearchMemory` 75 行 / `AddMemory` 57 行 | 把"输入归一化 + 可选字段填充"挪进 `memoryapp.MemoryService.AddEntry / SearchWithDefaults`;handler 仅 bind + render |
| `memory_session_handler.go` | `CreateSession` 54 行 / `GetStats` 39 行 | 进一步收口到 `memoryapp.SessionService` |
| `memory_entity_handler.go` | `ExtractEntities` 50 行 | 限流 + 业务在 service;handler 简化 |
| `memory_summary_handler.go` | `errors.Is(memdomain.ErrSessionNotFound)` | 保留 — domain sentinel 比较合规;不需要重构 |

#### 2.2.4 chat context

| handler | 现状 | 目标动作 |
|---|---|---|
| `chat_handler.go` | `AddMessage` 47 行,跨 agent + memory ctx 编排 | 引入 `chatapp.ConversationService.AppendUserMessage(ctx, in) (Message, error)` 内部调 agent service + memory port;handler 仅 bind + render |

> 当前 `chat_handler.go` import `internal/agent/application` — 跨 ctx 调用 application 是**不允许**的(应通过消费者侧 port + chat ctx 自己的 service)。本次新建 `internal/chat` ctx 或将 chat 合并到 memory ctx — 决策见 §8。

#### 2.2.5 mcp / rag / skill / model context

| handler | 动作 |
|---|---|
| `mcp_handler.go` | `ConnectServer` 字段默认填充挪到 `mcpapp.ServerService.Connect`;handler 不再接 `*mcpdomain.ServerConfig` 全字段,改接 `mcpapp.ConnectInput` |
| `rag_handler.go` | 移除 `internal/skill/domain` import(`DefaultTopK` 改成 `pkg/constants.DefaultRAGTopK`);`Query` 59 行内联收口到 `knowledge.RAGService.Query` |
| `skill_handler.go` | **不动**(规范基线) |
| `model_handler.go` | **不动**(规范基线) |

### 2.3 跨 ctx 调用规则(强化)

handler **绝不** import 兄弟 ctx 的 `application` 包。当 chat handler 需要触发 agent 执行时:

```
chat_handler.go          → chatapp.ConversationService
chatapp.ConversationService → port.AgentExecutor (定义在 internal/chat/domain/port/)
api/wiring/chat.go       → wiringAgentExecutor 实现 port,内部调 internal/agent/application.AgentService
```

handler 看到的只是单一 ctx 的 service。

---

## 3. 全量 ports 总览

### 3.1 新增 ports

| Ctx | Port 文件 | 接口 | Adapter 位置 |
|---|---|---|---|
| agent | `domain/port/tenant_settings.go` | `TenantSettingsReader` | `api/wiring/agent.go` |
| agent | `domain/port/tenant_gateway.go` | `TenantGatewayProvider` | `api/wiring/agent.go` |
| agent | `domain/port/capability_factory.go` | `CapabilityGatewayFactory` | `api/wiring/agent.go` |
| agent | `domain/port/rag_search.go` | `RAGSearchProvider` | `api/wiring/agent.go` |
| agent | `domain/port/skill_tool_meta.go` | `SkillToolMetadata` | `internal/skill/infrastructure/persistence/` |
| agent | `domain/port/llm_gateway.go` | `LLMGateway` minimal | (consumer-side,实现自动满足) |
| chat | `domain/port/agent_executor.go` | `AgentExecutor.Execute(ctx, agentID, query, ec) (Result, error)` | `api/wiring/chat.go` |
| chat | `domain/port/message_store.go` | 同 memory port,但消费方独立声明 | `api/wiring/chat.go`(thin adapter to memory) |
| iam | `domain/port/oauth_callback.go` | `OAuthCallbackHandler.HandleGitHubCallback(...)` | `internal/iam/application/oauth_service.go` 实现 |

### 3.2 应删除/迁移的 handler-local 类型

- `api/http/handler/admin_handler.go` 中的 `PgxPool` interface → 迁出 handler 包(若仍需,放 `internal/iam/domain/port/`,否则直接删,因为 `iamapp.AdminService` 已经把 SQL 封装了)

---

## 4. 数据流(全 handler 退化后的统一形态)

```
HTTP 请求
  ↓ Gin 路由
handler(≤ 30 行/方法,纯 transport)
  ├── bind & validate(struct tag + ShouldBindJSON)
  ├── 取 tenantID/userID/traceID(from middleware.GetTenantID/GetUserID/GetTraceID)
  ├── svc.Method(ctx, input)
  ├── 错误 → c.Error(err) 交 ErrorHandler 中间件
  └── 成功 → c.JSON(http.StatusXxx, response)
        ↓
internal/<ctx>/application/*.go(用例编排,事务边界,DTO↔聚合)
        ↓
internal/<ctx>/domain/port/*.go(出向接口)
        ↓
infrastructure adapter(SQL / Redis / Milvus / NATS / HTTP)
```

SSE 路径例外:transport-level 的 flusher / heartbeat / Header / token 写入仍在 handler;业务 callback 通过 `tokenCb func(string)` 传给 service。

---

## 5. 错误处理统一规约

- domain 定义 `Err*`(已有 + 新增)
- infrastructure adapter 翻译外部错误 → domain `Err*`
- application 透传 / 包裹 `fmt.Errorf("xxx: %w", err)`
- handler 一律 `c.Error(err)`,**禁用** `c.JSON(http.StatusXxx, dto.ErrorResponse{...})` 内联响应
- middleware `error_mapping.go` 集中映射 `domain.Err* → HTTP code`(已建立,本次扩充覆盖率)

---

## 6. 静态守护(本次必须落地)

`.golangci.yml` 现有 `handler-no-infra` 规则扩展为:

```yaml
handler-no-infra:
  files: ['**/api/http/handler/**']
  deny:
    - pkg: 'github.com/byteBuilderX/stratum/internal/*/infrastructure'
    - pkg: 'github.com/byteBuilderX/stratum/internal/*/infrastructure/*'
    - pkg: 'github.com/byteBuilderX/stratum/pkg/tenantdb'
    - pkg: 'github.com/byteBuilderX/stratum/pkg/storage/postgres'
    - pkg: 'github.com/byteBuilderX/stratum/pkg/crypto'
    - pkg: 'github.com/jackc/pgx/v5'
    - pkg: 'github.com/jackc/pgx/v5/pgxpool'
    - pkg: 'github.com/jackc/pgx/v5/pgconn'
    - pkg: 'github.com/redis/go-redis/v9'
    - pkg: 'github.com/milvus-io/milvus-sdk-go/v2'
```

新增自定义 lint(可选,先不强制):函数行数 > 30 行 + receiver 是 `*XxxHandler` → warn。

---

## 7. 行数预估(全集)

| 文件 / 包 | 现 | 后 | Δ |
|---|---:|---:|---:|
| admin_handler.go | 130 | 70 | -60 |
| agent_handler.go | 172 | 80 | -92 |
| agent_crud_handler.go | 283 | 140 | -143 |
| agent_exec_handler.go | 282 | 150 | -132 |
| auth_handler.go | 84 | 50 | -34 |
| auth_oauth_handler.go | 153 | 50 | -103 |
| auth_register_handler.go | 105 | 40 | -65 |
| auth_session_handler.go | 123 | 50 | -73 |
| auth_tenant_handler.go | 114 | 60 | -54 |
| chat_handler.go | 233 | 130 | -103 |
| mcp_handler.go | 182 | 160 | -22 |
| memory_message_handler.go | 200 | 130 | -70 |
| memory_session_handler.go | 145 | 100 | -45 |
| memory_entity_handler.go | 104 | 70 | -34 |
| memory_summary_handler.go | 54 | 50 | -4 |
| memory_handler.go | 76 | 76 | 0 |
| model_handler.go | 25 | 25 | 0 |
| rag_handler.go | 291 | 200 | -91 |
| skill_handler.go | 137 | 137 | 0 |
| tenant.go | 44 | 30 | -14 |
| tenant_handler.go | 260 | 200 | -60 |
| **handler 小计** | **3 197** | **1 998** | **-1 199** |
| 新 application service / ports / adapters | — | ~1 100 | +1 100 |
| **总净变化** | | | **-99** |

handler 层瘦身 ~38%;违规归零;application + wiring 增加 ~1 100 行(其中 ~600 已有逻辑搬移,~500 是适配器)。

---

## 8. 关键决策点

1. **chat 是否独立成 ctx**? — **是**。新建 `internal/chat/{domain,application}`,把 conversation/message 模型从 memory 拆出。memory 仅做语义记忆条目;chat 做对话流。本次预留目录 + service 框架,具体语义切分留下一阶段。
2. **`AuthHandlerDeps` 是否拆解**? — **是**。改为 `AuthService` + `OAuthService` + `OnboardService` 三个 application service,handler 持有这三个 service 引用,`AuthHandlerDeps` 仅余 `Logger / SecureCookies / FrontendURL` 等纯 transport 配置。
3. **tenantdb 引用** — handler 全部清掉;application/infrastructure 内允许使用(它是 storage primitive,不在 application 禁运清单)。
4. **`*_test.go` 同步迁移** — 全部 handler 测试改为 mock 单一 service interface;现有 `agent_handler_test.go` / `chat_handler_test.go` / `auth_handler_test.go` 等模式全部统一。
5. **DTO 字段不动** — HTTP 响应 shape 不变,`api/http/contract_test.go` + golden 文件**零更新**。

---

## 9. 实施阶段(8 个 PR,每 PR ≤ 600 行 diff)

| 阶段 | PR | 内容 | 验收 |
|---|---|---|---|
| P1 | depguard 锁链 | `.golangci.yml` 加完整黑名单(暂时 warn 级,不 fail) | `rtk golangci-lint` 跑出违规列表与本 spec §1.2 矩阵一致 |
| P2 | agent ctx 重构 | 上一版 spec §7 全部内容 | 三 agent handler ≤ §7 行数;`-race` 全绿 |
| P3 | iam ctx 重构 | auth_oauth/register/session/tenant + admin + tenant.go 全清 | 5 handler ≤ §7 行数;contract golden 不变 |
| P4 | chat ctx 拆分 | 新建 `internal/chat/`;chat_handler 切到 chat service | chat_handler ≤ 130 |
| P5 | memory handler 收口 | message/session/entity 三 handler 业务挪到 service | 各 handler ≤ §7 行数 |
| P6 | mcp/rag handler 收口 | mcp 输入 DTO 化;rag 移除 skill domain 引用 | 引用清单达标 |
| P7 | depguard 升级 fail | 把 §6 的 deny 从 warn 改为 fail | CI 红绿门 |
| P8 | tenantdb shim 删除 | `pkg/tenantdb` 整包删除,全部改 `pkg/storage/postgres` | grep 无残留 |

每阶段提交前必跑:`rtk go test -race ./...` + `rtk golangci-lint run` + `npm run build`(前端契约校验)。

---

## 10. 不在本次范围

- 前端任何改动(handler 退化保证响应 shape 不变)
- `internal/<ctx>/infrastructure/` 内部实现重构(只做翻译错误 + port 适配)
- `pkg/storage/postgres` 自身 API 变更
- 新增 bounded context 的完整建模(chat ctx 仅做最小拆分)
- `auth_handler.go` 的 GitHub OAuth client 直引(已是 port,合规)

---

## 11. 实施记录

### 2026-06-18: P1 + P2 完成(agent ctx 全清)

**8 个 task / 7 commits on `feat/ddd-refactor`**:

| Task | 提交 | 内容 |
|---|---|---|
| 0 | `8492652` | snapshot pre-existing P1+P2 WIP across 8 contexts(stabilize HEAD 可编译) |
| 2 | `d56d02a` | `AgentService` 聚合层(CRUD + Execute/ExecuteStream/ListExecutions) |
| 3 | `b554060` | `Execute/ExecuteStream/ListExecutions` 下沉至 application |
| 4 | `39027cb` | wiring adapter — `Container.Agent.Service` + `ragSearchAdapter` |
| 5 | `f39c87f` | handler degrades to transport-only(svc + logger 两字段) |
| 6 | `d31668d` | depguard locks(`handler-no-rawdb` + agent/infra deny);handler 改用 `reqctx` |
| 7 | `c74333a` | split `agent_dto.go` from `agent_handler.go`(handler.go 26 行) |

**handler 行数(原 → 现)**:

- `agent_handler.go`: 174 → **26 行**(struct + constructor only,远低于 §7 目标 80)
- `agent_dto.go`: NEW **97 行**(5 个 wire DTO + dtoToResponse)
- `agent_crud_handler.go`: 282 → **167 行**(超 §7 目标 140 共 27 行 — 13 字段 DTO 拷贝是契约最小量)
- `agent_exec_handler.go`: 273 → **202 行**(超 §7 目标 150 共 52 行 — SSE heartbeat + clientCtx watcher + tokenCb closure 不可压缩)

**架构验证**:

- `grep -E "tenantdb|pgxpool|internal/.*/infrastructure" api/http/handler/agent*.go` → 全部无输出
- `golangci-lint run ./...` → No issues found(`handler-no-infra` + `handler-no-rawdb` 双重 fence)
- `go test -race ./...` → 500 passed in 70 packages
- `go test -run TestContract ./api/http/...` → 60 passed(JSON 响应 shape 零漂移)

**关键决策**:

- 跨 ctx 调用走消费者侧 port(`agentport.RAGSearchProvider`),provider 实现在 `api/wiring/ragSearchAdapter`,避免 `internal/agent` import `internal/knowledge`
- fire-and-forget goroutine 用 `context.WithoutCancel(reqCtx)` 替代旧的 `tenantdb.WithTenant(context.Background(), tc)` 模式,保留 trace 链路
- handler 改 `reqctx.TenantIDFromContext` 读租户;中间件并行注入 `tenantdb.TenantContext`(为 infrastructure 适配)+ `reqctx.TenantID`(为 handler/application);`pkg/tenantdb` 整包删除挪到 P8

**后续**:P3-P8 按 spec §9 顺序推进。
