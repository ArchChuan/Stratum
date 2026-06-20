# SPEC: Stratum

> Reverse-engineered specification — generated 2026-06-20 from branch `feat/ddd-refactor` (HEAD 14fb8b8)
> Scope: backend (`api/`, `internal/`, `pkg/`, `cmd/`) — depth: deep

## 1. Overview

### 1.1 Purpose

Stratum 是一套面向私有化部署的企业级 AI 应用底座。后端 Go + DDD 8 bounded context，前端 React 18 + AntD 5。把 Agent 编排（ReAct）、Skill 网关、记忆系统、GraphRAG、MCP 协议、多租户 IAM 串成统一的运行链路，目标是「让团队自托管 AI Agent 平台，不依赖 SaaS」。

### 1.2 Key Capabilities

- Agent 编排：ReAct StateGraph，工具调用 / SSE 流式 / 中断恢复 / 历史会话回填
- Skill Gateway：code（goja JS 沙箱）· llm · http 三类执行器，命名 `tenant_{id}_{name}` 隔离
- 记忆系统：PostgreSQL 持久化 + 三阶段 NATS 异步流水线（outbox → embedder → enricher）+ Token 预算/摘要压缩
- GraphRAG：Milvus 向量 + Neo4j 图谱，支持 vector / keyword / graph / hybrid（RRF）四种检索模式
- MCP 协议：stdio / http / sse 三种 transport，AuthType none/bearer/api_key/oauth2，自动重试
- 多租户 IAM：PostgreSQL schema 物理隔离 + GitHub OAuth + JWT RS256 + tenant 邀请/角色管理
- LLM 网关：通义千问 / 智谱 AI 双 OpenAI-compat provider，TenantGatewayCache 按租户密钥缓存
- 可观测性：Zap 结构化日志 + OpenTelemetry tracing + Prometheus 指标 + Harness 生命周期

### 1.3 Architecture Style

分层 DDD 单体（Modular Monolith）。`api/http` 接入层 → `api/wiring.Container` 组合根 → 8 个 `internal/<ctx>/{domain,application,infrastructure}` bounded context → `pkg/` 无业务基础设施。跨 context 仅经消费者侧 `domain/port/` 接口 + wiring 层 thin adapter，禁止 import 兄弟 context 的 `application` / `infrastructure`。

---

## 2. Tech Stack

| 层级 | 选型 | 版本 |
|------|------|------|
| 后端语言 | Go | 1.25 |
| Web 框架 | Gin | v1.9 |
| RDB driver | pgx | v5 |
| Cache | go-redis | v9 |
| 消息总线 | NATS JetStream | v1.31 |
| 向量库 | Milvus SDK | v2.4.2 |
| 图数据库 | Neo4j | v5 |
| JWT | golang-jwt | v5（RS256） |
| OAuth | GitHub OAuth | — |
| Migration | golang-migrate | v4 |
| 日志 | Zap | — |
| Tracing/Metrics | OpenTelemetry | v1.21 + Prometheus |
| LLM Provider | Qwen（dashscope）· Zhipu | OpenAI-compat |
| JS 沙箱 | goja | — |
| 配置 | Viper | v1.18 |
| 前端 | React 18 · Vite 4 · AntD 5.2 · React Router 6 · Axios · Moment | — |

---

## 3. Project Structure

```
stratum/
├── api/
│   ├── http/
│   │   ├── handler/            每域一个 handler（agent_crud, memory, tenant, admin, mcp, skill, knowledge, auth）
│   │   ├── dto/                请求/响应 DTO（admin, agent, memory, request, rag）
│   │   ├── middleware/         JWT · Tenant · Trace · Prometheus · ErrorHandler · RequireRole
│   │   └── router.go           Gin 路由组装配
│   └── wiring/
│       ├── container.go        BuildContainer（组合根）+ Shutdown 逆序释放
│       ├── memory.go           memory pipeline 装配
│       ├── tenant_resolver.go  TenantCapabilityResolver（per-tenant gateway 解析）
│       └── *.go                每域一个 wiring 文件
├── internal/                    8 bounded contexts
│   ├── agent/                  ReAct StateGraph · Registry · ExecutionStore · ChatStore · BaseAgent
│   │   └── application/graph/  StateGraph[T]・nodeLLM・nodeTool・条件边
│   ├── memory/                 MemoryManager · MemoryRepo port · pipeline（outbox→embedder→enricher）
│   ├── knowledge/              Workspace 聚合 · RAGService · IngestService · GraphRAG client
│   ├── skill/                  SkillService · code/llm/http executor · CircuitBreaker
│   ├── mcp/                    MCPService · ServerManager · SkillRegistry · 三种 transport
│   ├── iam/                    TenantService · AdminService · OnboardService · JWTService
│   ├── llmgateway/             Gateway · TenantGatewayCache · openai_compat client
│   └── platform/               Viper config · Harness 生命周期
├── pkg/                         无业务基础设施
│   ├── storage/{postgres,redis,milvus,tenantnaming,tenantdb}
│   ├── messaging/nats
│   ├── observability           Logger · Metrics · Tracer · SpanFromContext
│   ├── crypto                  AES-256-GCM
│   ├── constants               跨包业务/超时常量
│   ├── migration               public schema 编号迁移
│   ├── reqctx                  trace_id / tenant_id ctx propagation
│   ├── httpclient · textchunk · ...
├── cmd/server/main.go           ≤30 行，调用 wiring.BuildContainer 启动 Harness
├── web/                         React 前端（src/modules 按域）
├── docs/                        架构、部署、模块文档
├── helm/ k8s/ grafana/          部署与可观测性
└── openspec/                    OpenSpec 规范变更
```

---

## 4. Data Model

### 4.1 Public Schema（跨租户）

| 表 | 关键字段 | 备注 |
|----|---------|------|
| `users` | id (UUID), github_id, github_login, avatar_url, global_role, created_at | OAuth 账号 |
| `tenants` | id, name, slug, plan(free\|pro\|enterprise), status, settings JSONB, is_default, deleted_at | settings 内置加密 `llm_api_keys` |
| `tenant_members` | tenant_id, user_id, role(owner\|admin\|member), UNIQUE(tenant_id,user_id) | |
| `invitations` | id, tenant_id, email, token_hash(sha256), invited_by, expires_at, accepted_at | TTL = `constants.InviteTokenTTL` |
| `refresh_tokens` | id, user_id, token_hash(sha256), expires_at | |
| `tenant_api_keys` | id, tenant_id, key_hash, name, scopes, expires_at | |
| `audit_logs` | id, tenant_id, user_id, action, target, payload JSONB, created_at | |
| `model_providers` | seeded: openai · anthropic · ollama | |
| `models` | seeded: gpt-4o(-mini) · text-embedding-3-small · claude-{opus,sonnet,haiku}-4.x | |

### 4.2 Tenant Schema（per-tenant，`SET LOCAL search_path = tenant_{id}, public`）

| 域 | 表 | 关键字段 / 约束 |
|----|----|----|
| Agent | `agents` | id TEXT PK, name UNIQUE, type DEFAULT react, llm_model, embed_model, max_iterations DEFAULT 10, max_context_tokens DEFAULT 8000 |
| | `agent_mcp_links` | (agent_id, server_id) CASCADE 双删 |
| | `agent_skill_links` | (agent_id, skill_id) CASCADE |
| | `agent_workspaces` | (agent_id, workspace_id) CASCADE |
| | `agent_executions` | id UUID, status CHECK('success','error'), input/output_preview, total_tokens, duration_ms |
| MCP | `mcp_configs` | id TEXT, transport, command/url, args/env/headers/auth_config/retry_config JSONB, timeout_sec DEFAULT 30 |
| Skill | `skills` | id, name UNIQUE, type, config JSONB（type post-create immutable） |
| Memory | `memory_entries` | id UUID v7, conversation_id FK, user_id, agent_id FK, role, content, type DEFAULT short_term, importance FLOAT8, tags TEXT[], keywords TEXT[], token_estimate, expires_at, enriched_at; GIN trgm on content |
| | `entities` | id UUID v7, name, type, properties JSONB, confidence DEFAULT 0.5, user_id, agent_id, occurrence_count; UNIQUE(user_id, COALESCE(agent_id,''), name, type) |
| | `entity_relations` | (from_id, to_id) CASCADE, relation, properties JSONB |
| | `memory_outbox` | id BIGSERIAL, message_id NOT NULL, payload JSONB |
| | `memory_summaries` | conversation_id FK, summary, covered_until, token_count |
| | `memory_token_budgets` | conversation_id PK, accumulated, last_reset_at |
| Chat | `chat_conversations` | id UUID, agent_id FK, user_id, name DEFAULT '新会话', expires_at DEFAULT now()+30d, deleted_at（软删） |
| | `chat_messages` | id UUID v7, conversation_id FK CASCADE, role CHECK('user','agent'), content, steps_json JSONB, is_error |
| Knowledge | `rag_workspaces` | id UUID, name UNIQUE, description, config JSONB |
| | `knowledge_docs` | workspace_id FK CASCADE, title, content, source, metadata JSONB |
| Sessions | `sessions` | id UUID, agent_id FK, user_id, started_at/ended_at |
| Misc | `exec_history` · `llm_api_keys` · `model_presets` · `model_usage` · `model_quotas` · `prompt_templates` · `workflows` · `workflow_runs` · `scheduled_tasks` · `webhooks` · `webhook_deliveries` | 详见 `pkg/storage/postgres/tenant_schema.sql` |

### 4.3 Migration Timeline

001 public baseline · 002 is_default_tenant · 003 global admin owner · 004/005 agent_executions add+move-to-tenant · 006 agent_skill_links · 007 name unique · 008 memory_pipeline · 009 agent_context_tokens · 010 soft_delete_conversations · 011 uuid_v7 func · 012 pgcrypto + recreate gen_uuid_v7 · 013 trgm index · 014 cascade-delete fixes

**多租户 DDL 规则**：编号迁移仅操作 public；引用 tenant-only 表的 DDL 必须放 `pkg/storage/postgres/tenant_schema.sql` 由 `ProvisionAllTenantSchemas` 幂等应用；新增列必须 `ALTER TABLE … ADD COLUMN IF NOT EXISTS` 做 backfill。

### 4.4 Domain Aggregates

- **agent**：`AgentConfig`（聚合根）· `ChatConversation` · `ChatMessage` · `ExecutionRecord` · `Message/Thought/ToolCall/AgentResult/AgentState`（hoisted to domain）
- **memory**：`MemoryEntry`（id, type∈{short_term,long_term,entity,summary}, role, content, importance, vector []float32, tags, expires_at）· `Entity` · `SessionContext{TenantID,UserID,SessionID,AgentID}` · `MemoryFilter`（composable）
- **knowledge**：`Workspace` 聚合 · `WorkspaceConfig{EmbeddingModel,ChunkSize,ChunkOverlap,QueryMode,TopK}` · `MergeUpdate` 强制不可变
- **skill**：`Skill` interface · `BaseSkill` · `SkillExecutor.Execute(ctx, input) → (any, error)`
- **mcp**：`Server` · `Tool{Name,Description,InputSchema}` · `AuthConfig`（含 OAuth2 字段）· `RetryConfig`
- **iam**：`Tenant`（IsDefault, MemberCount）· `Member` · `Invitation`（TokenHash, ExpiresAt）· `UserTenantInfo` · `StoredSession`
- **llmgateway**：`CompletionRequest/Response` · `Message` · `Tool/ToolCall` · `TokenUsage` · `EmbeddingRequest/Response` · `LLMCompleter` port

### 4.5 State Transitions

- **agent_executions.status**: `success` ⊕ `error`（CHECK）
- **chat_messages.role**: `user` ⊕ `agent`（CHECK）
- **chat_conversations**: live → soft-deleted（`deleted_at` 设值），TTL `expires_at`（默认 +30d）
- **memory_entries.type**: `short_term` → `long_term`（pipeline enricher 提升）/ `entity` / `summary`
- **invitations**: pending → accepted（设 `accepted_at`）/ expired（`expires_at` 到期）
- **tenant_members.role**: `owner` ↔ `admin` ↔ `member`（owner-only 改写，禁 self-modify，admin 不能 remove admin）

---

## 5. API Surface

### 5.1 HTTP（全部 `application/json` 除上传 multipart）

> Auth：JWT Bearer（middleware/jwt） · Tenant：`X-Tenant-ID` header（middleware/tenant 解析 + `inject_tenant` 设 `search_path`） · Role：部分接口 `RequireRole(owner/admin)`

| 域 | Method | Path | 说明 |
|----|--------|------|------|
| Auth | GET | `/api/auth/github/login` | OAuth 跳转 |
| | GET | `/api/auth/github/callback` | OAuth 回调，签发 access+refresh |
| | POST | `/api/auth/refresh` | 刷新 access token |
| | POST | `/api/auth/logout` | 撤销 refresh |
| | GET | `/api/auth/me` | 当前用户 |
| Onboard | POST | `/api/onboard/tenant` | 首次创建 tenant |
| | POST | `/api/onboard/join` | 接受邀请加入 tenant |
| Tenant | GET/POST/PATCH/DELETE | `/api/tenants` · `/api/tenants/:id` | 列表/创建/补丁/删除 |
| | GET/POST/PATCH/DELETE | `/api/tenants/:id/members` · `/api/tenants/:id/members/:userId` | 成员管理（role rules 见 §8） |
| | POST | `/api/tenants/:id/members/invite` | 邀请（返回 invitation_url） |
| | GET/PUT | `/api/tenants/:id/settings` | settings + LLM API keys（屏蔽 6+8 bullets） |
| | POST | `/api/tenants/:id/settings/embed-model` | set-once 写入 |
| Admin | GET | `/api/admin/tenants` | 全局视图 |
| | POST/DELETE | `/api/admin/users/:id/global-role` | 全局 admin/owner 管理 |
| Agent | GET/POST/PATCH/DELETE | `/api/agents` · `/api/agents/:id` | CRUD |
| | POST | `/api/agents/:id/execute` | 同步执行 |
| | POST | `/api/agents/:id/execute-stream` | SSE 流式 |
| | GET | `/api/agents/:id/executions` | 分页执行历史 |
| Chat | GET/POST/PATCH/DELETE | `/api/agents/:id/conversations` · `/api/conversations/:cid` | 会话 CRUD（软删） |
| | GET | `/api/conversations/:cid/messages` | 消息列表 |
| Memory | POST | `/api/memory` | 添加 |
| | GET | `/api/memory/:id` | 取单条 |
| | DELETE | `/api/memory/:id` · `/api/memory/sessions/:sid` | 删/清空会话 |
| | POST | `/api/memory/search` | 搜索（filters + min_score + limit） |
| | GET | `/api/memory/stats` · `/api/memory/summary/:sid` | 统计 / 摘要 |
| Knowledge | GET/POST/PATCH/DELETE | `/api/workspaces` · `/api/workspaces/:id` | Workspace CRUD（embedding_model/chunk_* 不可变） |
| | POST | `/api/workspaces/:id/documents` | multipart 上传 |
| | POST | `/api/workspaces/:id/query` | RAG 查询（mode∈{vector,graph,hybrid,keyword}） |
| Skill | GET/POST/PATCH/DELETE | `/api/skills` · `/api/skills/:id` | type post-create immutable |
| | POST | `/api/skills/:id/run` | 仅 type=code 可独立运行 |
| MCP | GET/POST/DELETE | `/api/mcp/servers` · `/api/mcp/servers/:id` | Server 管理 |
| | GET | `/api/mcp/servers/:id/tools` · `/resources` | 工具/资源目录 |
| | POST | `/api/mcp/skills/refresh` | 重建 SkillRegistry |
| | GET | `/api/mcp/status` | Server 连接概览（total/connected/disconnected/error） |
| Health | GET | `/healthz` · `/readyz` · `/metrics` | 不走 auth |

### 5.2 SSE Stream（`/api/agents/:id/execute-stream`）

`event: token` `data: <chunk>` 每 LLM token 增量；`event: done` 结束；`event: error` 异常；客户端可中途断流，`context.WithoutCancel` 隔离 agent 执行不被 HTTP 取消打断。

### 5.3 Error Response

冻结契约：`{"error": "<message>"}`。HTTP 状态由 `api/middleware/error_mapping.go` 中央表映射 domain sentinel：

- 401 `ErrInviterMissing`
- 403 6 个 forbidden sentinels（AdminOrOwner/Owner/SelfModify/OwnerRole/RemoveOwner/AdminRemove）
- 404 NotFound 系列（pgx.ErrNoRows · ErrWorkspace/Member/Tenant/Entry/Session/Skill NotFound · ErrServerNotFound）
- 409 Conflict（Workspace · Agent · MCP · Skill name/Linked）
- 422 `agentapp.ErrInvalidSkill`
- 429 `skilldomain.ErrConcurrencyLimit`
- 400 InvalidSettings · ImmutableXxx · UnsupportedType · CodeAnalysis
- default 500；`HTTPError` wrapper 支持显式 status override

---

## 6. Configuration

> Viper（`internal/platform/config`），yaml + ENV override（双下划线分隔）。生产配置 `config/prod.yaml` 禁修改。

| Key | 必需 | 默认 | 说明 |
|-----|------|------|------|
| `SERVER_ADDR` | × | `:8080` | HTTP 监听 |
| `SERVER_READ_TIMEOUT` / `WRITE_TIMEOUT` | × | 30s / 60s | |
| `POSTGRES_DSN` | ✓ | — | `postgres://...` |
| `REDIS_ADDR` / `PASSWORD` / `DB` | ✓ | — | |
| `NATS_URL` | ✓ | `nats://localhost:4222` | JetStream |
| `MILVUS_ADDR` | ✓ | `localhost:19530` | gRPC |
| `NEO4J_URI` / `USER` / `PASSWORD` | ✓ | — | bolt://… |
| `JWT_PRIVATE_KEY_PATH` / `PUBLIC_KEY_PATH` | ✓ | — | RS256 PEM |
| `JWT_ACCESS_TTL` / `REFRESH_TTL` | × | 1h / 30d | |
| `GITHUB_OAUTH_CLIENT_ID` / `SECRET` / `CALLBACK_URL` | ✓ | — | |
| `FRONTEND_URL` | ✓ | — | invitation_url 拼接 |
| `LLM_PROVIDER_DEFAULT` | × | `qwen` | qwen \| zhipu |
| `QWEN_API_KEY` / `QWEN_BASE_URL` | per-tenant | dashscope endpoint | tenant settings 覆盖 |
| `ZHIPU_API_KEY` / `ZHIPU_BASE_URL` | per-tenant | bigmodel endpoint | |
| `CRYPTO_AES_KEY` | ✓ | — | 32-byte base64，加密 `tenant.settings.llm_api_keys` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | × | — | OTLP collector |
| `LOG_LEVEL` / `LOG_ENV` | × | info / production | production→JSON，其他→console+color |
| `GLOBAL_ADMIN_GITHUB_LOGIN` | × | — | OAuth 登录自动赋 global_role=admin |

---

## 7. External Dependencies

| 服务 | 用途 | 失败影响 |
|------|------|---------|
| PostgreSQL | 主存储（public + per-tenant schema） | 致命：所有写操作不可用 |
| Redis | session / cache / rate-limit | 降级：部分缓存 miss，仍可运行 |
| NATS JetStream | memory pipeline outbox 消费、领域事件 | 异步 enrich 停滞，主路径仍可用 |
| Milvus | 向量检索（GraphRAG vector mode） | RAG vector/hybrid 不可用，graph/keyword 仍工作 |
| Neo4j | 知识图谱 + GraphRAG full-text | RAG graph/hybrid 降级到 vector |
| LLM Provider（Qwen/Zhipu） | LLM 推理 + embedding | Agent 执行不可用 |
| GitHub OAuth | 登录 | 新登录不可用，已登录 session 仍有效 |
| MCP Servers | 外部工具（stdio/http/sse） | 该 server 工具调用失败，其他工具不影响 |
| Vault / AWS Secrets Manager | 密钥管理（生产） | 启动失败 |

---

## 8. Business Rules & Constraints

1. **多租户隔离**：所有 tenant 资源走 `SET LOCAL search_path = tenant_{id}, public`；编号 migration 仅碰 public，per-tenant DDL 必须放 `tenant_schema.sql` 由 `ProvisionAllTenantSchemas` 幂等应用。
2. **AI 不做控制逻辑**：路由 / 重试 / 状态机硬编码在 Go 层；LLM 仅做语言任务（生成、抽取）。
3. **Agent 执行**：`context.WithTimeout(context.WithoutCancel(reqCtx), constants.AgentExecTimeout)` 让 SSE 中途断流不打断 agent；执行记录 fire-and-forget 写入。
4. **Skill type 创建后不可变**；`code` 类型由 goja 沙箱执行，自动注入 `__tenant_id`，禁止裸 `eval`。
5. **Workspace 不可变字段**：`embedding_model` / `chunk_size` / `chunk_overlap` 创建后只读（`MergeUpdate` 强制）。
6. **Embed model**：tenant 级 set-once（`SetEmbedModel` 拒绝二次写入 → `ErrEmbedModelAlreadySet`）；agent 创建时未指定则继承 tenant default。
7. **Tenant 角色规则**：
   - `InviteMember` 需 owner 或 admin；token = sha256(rand 32 bytes)，TTL = `constants.InviteTokenTTL`
   - `UpdateMemberRole` 仅 owner，禁自我修改，目标必须非 owner
   - `RemoveMember` 仅 owner/admin，禁自删，禁删 owner，admin 不能删 admin
   - 默认 tenant `is_default=true` 不可删（`ErrDefaultTenantDelete`）
8. **LLM API keys 加密**：AES-256-GCM 写入 `tenant.settings.llm_api_keys`；GET 返回 `mask = first6 + "••••••••"`；UPDATE 跳过 placeholder bullets，merge 已有 keys；写入后 `TenantGatewayCache.Invalidate(tenantID)`。
9. **JWT**：RS256（拒绝非 RSA 签名方法）；claims `sub/tid/role/global_role/jti/ava/ghl`；refresh token sha256 入库。
10. **Memory 范围**：用户级隔离（`(user_id, COALESCE(agent_id,''), name, type)` UNIQUE）；search 至少传 `tenant_id+user_id`；entries 默认 `type=short_term`，pipeline enricher 升级 `long_term`。
11. **Memory pipeline**：写入 outbox（`message_id NOT NULL`）→ embedder 生成向量 → enricher 抽实体并 set `enriched_at`；token budget 累计到 `memory_token_budgets`，超阈值触发 summary 压缩存 `memory_summaries.covered_until`。
12. **ReAct loop**：节点 `nodeLLM` ↔ `nodeTool` 条件边；LLM 每步 60s 超时 + `RetryFn(DefaultRetry)`；tool 30s 超时；`stratum_search_knowledge` topK clamp 到 `constants.MaxRAGTopK`；skill tool 名 `tenant_{id}_{name}`；`SkillToolIndex` 映射回 UUID。
13. **GraphRAG mode**：`vector` embed→Milvus search · `keyword` Milvus KeywordSearch · `graph` Neo4j FullTextSearch（limit 20）· `hybrid` RRF（VectorStore.HybridSearch）；collection 命名 `tenantdb.WorkspaceCollection`，fallback `{workspace}_kb`。
14. **MCP retry**：`RetryConfig{Enabled, MaxRetries, InitialDelayMs, MaxDelayMs, BackoffFactor}` 指数退避；transport 中 stdio/http/sse 三选一互斥。
15. **Chat**：`role` CHECK ∈ {user, agent}；`steps_json` 存储 ReAct 步骤；`is_error` 标记；conversation `expires_at` 默认 +30 天，`deleted_at` 软删。
16. **Outbox 写入必须包含 `message_id`**（NOT NULL 无 DEFAULT），曾因漏列触发全量回滚。
17. **错误分层**：`domain.Err*` → infrastructure 翻译 `pgconn.PgError` → application 编排 → middleware 中央表映射 HTTP 状态码；响应 `{"error":"..."}` 冻结。
18. **生命周期**：`Harness` 顺序 Start，逆序 Stop；`Register` 在 Start 后锁定；组件实现 `Name/Start/Stop/HealthCheck`。
19. **Logger 红线**：禁记 `password/token/api_key/PII`，禁打印 HTTP body 原文（仅记 status + model）；`fmt.Print` 禁用，强制 Zap。
20. **依赖单向**：`pkg/` 不 import `internal/`；`domain/` 仅 stdlib + `pkg/constants`；`application/` 不 import `pgx/redis/nats/gin`；`handler/` 不 import `internal/*/infrastructure`；CI 用 `go-arch-lint` + `depguard` 固化。

---

## 9. Non-Functional Characteristics

### 9.1 Performance

- **缓存**：`TenantGatewayCache` 按 tenantID 缓存 LLM gateway 实例；redis 用于 session/quota
- **分页**：`ListOptions{Page, PageSize}` 标准化，pageSize 来自 `constants.DefaultPageSize`
- **流式**：SSE token 推送，前端 RAF batch 合并（ChatStreamContext）
- **批量**：embed 服务支持 batch input，文档摄取 chunk pipeline
- **索引**：`idx_chat_msg_conv` · `idx_memory_entries_user_id` · trgm GIN on `memory_entries.content` · 全部 FK 列均有索引
- **超时常量**：`AgentExecTimeout` · `react LLM 60s` · `react tool 30s` · `mcp default 30s` · 通过 `pkg/constants` 集中管理

### 9.2 Security

- **传输**：TLS 1.2+；静态数据 AES-256-GCM
- **JWT**：RS256，公私钥分离，access 1h / refresh 30d；refresh token sha256 hash 入库
- **OAuth**：GitHub，state CSRF 校验
- **Schema 隔离**：`SET LOCAL search_path` 每请求重置，防跨租户读
- **密钥管理**：Vault / AWS Secrets Manager（生产），禁入 git；`CRYPTO_AES_KEY` 加密 `tenant.settings.llm_api_keys`
- **Token 存储**：前端 httpOnly cookie 或内存 Context，禁 localStorage
- **输入校验**：DTO `binding` tag + service 层 domain 不变量自检
- **Skill 沙箱**：goja JS 引擎，禁 `eval`，注入受限 globals
- **审计**：`audit_logs` 记录关键写操作

### 9.3 Error Handling

- 错误传播：`fmt.Errorf("operation: %w", err)` 逐层包裹
- 中央映射：`api/middleware/error_mapping.go` 单点维护 sentinel→HTTP 状态码
- 重试：`RetryFn(DefaultRetry)` 指数退避（base 100ms，上限 10s），用于 LLM 调用与 MCP transport
- 熔断：Skill 层 `CircuitBreaker`；外部依赖失败不击穿主流程
- 异步：execution record fire-and-forget；memory pipeline outbox 解耦写入失败
- 软容错：MemoryManager nil repo nil-safe；MCP 单 server 失败不影响整体

---

## 10. Testing Strategy

| 类型 | 框架 | 覆盖模式 |
|------|------|---------|
| 单元 | Go 内建 `testing` + 表驱动 | application/domain 层 100%，mock port 接口；目标覆盖率 ≥80% |
| Repo 集成 | `pgxmock` / 真实 PG（`-tags=integration`） | infrastructure 层 SQL 翻译 |
| HTTP 契约 | golden file（`api/http/testdata/contracts/*.golden.json`） | `contract_test.go` 守护 API 向后兼容 |
| 前端 | Vitest（推断） | hooks / 组件单测 |
| 端到端 | 缺 | 标记在 §11 Known Gaps |

**约定**：

- `mock` 所有外部依赖（NATS / Milvus / Neo4j / LLM provider）
- `-race` 在完整 PR 跑：`go test -v -race -timeout 30s ./...`
- 短测：`go test -short ./...` 排除集成 tag
- AI 辅助测试必须给模板文件（如 `api/handler/tenant_handler_test.go`）

---

## 11. Known Gaps & Assumptions

- **Skill `llm` / `http` executor 未在本次扫描中读到完整实现**，仅确认 `code` 类型走 goja。
- **前端测试覆盖未量化**（Vitest 框架推断自 Vite，`web/` 内未读 test 文件）。
- **MCP `oauth2` AuthType 实现深度未验证**（domain 字段已定义，token refresh 流程未细查）。
- **没有发现 e2e / smoke 测试套件**（仅契约 golden + 单测）。
- **`internal/agent/application/agent.go` 与 `registry.go` 内部细节未完整读**（BaseAgent.Execute 主体已通过 ReAct graph 间接确认）。
- **OTEL trace 端到端串联**（HTTP→Agent→Skill→LLM）在 README 路线图标注为「待完成」。
- **WASM Sandbox** 替代 goja 在 README 路线图标注为「待完成」。
- **Workflow 引擎**（DAG + 长任务）路线图标注为待开发；表 `workflows` / `workflow_runs` 已建但消费侧逻辑未完整验证。
- **`scheduled_tasks` 表存在但调度器实现未在主流程读到**。
- **多 LLM provider 扩展**（Anthropic / Ollama / 本地）路线图待办；当前仅 Qwen + Zhipu。
- **审计日志写入点未穷举**（`audit_logs` 表存在，service 层调用密度未确认）。
- **Webhook 投递 retry / dead letter** 流程未读到实现细节。
- 假设：所有 `infrastructure/` 适配器都正确翻译 `pgconn.PgError → domain.Err*`（基于 error_mapping.go 反推，未逐文件核对）。

---

## 12. Appendix

### A. Dependency Graph（顶层）

```
api/http/handler ─┐
                  ├─→ api/http/dto
                  └─→ application（per-ctx）
                        ├─→ domain（同 ctx）
                        ├─→ domain/port（同 ctx，消费侧）
                        └─→ pkg/{constants,observability,...}

infrastructure（per-ctx）─→ implements domain/port
                          ├─→ pkg/storage/{postgres,redis,milvus}
                          ├─→ pkg/messaging/nats
                          └─→ third-party drivers

api/wiring ─→ application + infrastructure（组合根，唯一允许跨层装配的位置）
            ├─→ TenantCapabilityResolver（跨租户解析 LLM gateway）
            └─→ Harness.Register（顺序启停）

cmd/server/main.go ─→ wiring.BuildContainer ─→ Harness.Start

pkg/* ─/→ internal/*  （单向，禁止反向 import）
domain/* ─/→ third-party （仅 stdlib + pkg/constants）
```

### B. Environment Setup

```bash
# 1. 拉代码、装依赖
git clone https://github.com/byteBuilderX/stratum.git
cd stratum
cp .env.example .env  # 填入 GitHub OAuth、LLM Key、JWT keys

# 2. 拉本地 infra（Postgres · Redis · NATS · Milvus · Neo4j）
make dev-up

# 3. 运行 migration
make migrate-up

# 4. 启后端
make run        # :8080

# 5. 启前端
make fe-dev     # :3000

# 6. 完整 PR 校验
make be-fmt be-lint be-test
make fe-lint fe-build
```

JWT 密钥生成：

```bash
openssl genrsa -out private.pem 2048
openssl rsa -in private.pem -pubout -out public.pem
```

AES key 生成：

```bash
openssl rand -base64 32
```

### C. Key 常量（`pkg/constants`）

`AgentExecTimeout` · `DefaultInitHistoryWindow` · `MaxRAGTopK` · `InviteTokenTTL` · `DefaultPageSize` · `MaxPageSize` · `EmbedBatchSize` · `MemorySearchLimit` · `MCP*Timeout`

### D. 关键事件命名（Zap）

`llm.request` · `llm.complete` · `react.llm` · `react.tool` · `react.llm.response`（DEBUG）· `react.tool.response`（DEBUG）· `agent created/updated/deleted` · `agent execution started/finished`
