# SPEC: Stratum

> Reverse-engineered specification — refreshed 2026-07-17 from the current worktree
> Scope: backend, frontend, deployment assets, configuration, migrations, and tests — depth: deep

## 1. Overview

### 1.1 Purpose

Stratum 是一套面向私有化部署的企业级 AI 应用底座。后端 Go + DDD 9 bounded context，前端 React 18 + AntD 5。把 Agent 编排（ReAct）、instruction Skill、评估优化、记忆系统、GraphRAG、MCP 协议、多租户 IAM 串成统一的运行链路，目标是「让团队自托管 AI Agent 平台，不依赖 SaaS」。

### 1.2 Key Capabilities

- Agent 编排：ReAct StateGraph，工具调用 / SSE 流式 / 中断恢复 / 历史会话回填
- Skill capability：版本化 capability/activation/instructions/requirements，发布后由 Agent Loop 激活并约束 MCP、知识和记忆能力
- Evaluation：suite/revision、异步评估 job、优化候选、实验阶段与反馈闭环
- 记忆系统：PostgreSQL 持久化 + 三阶段 NATS 异步流水线（outbox → embedder → enricher）+ Token 预算/摘要压缩
- GraphRAG：Milvus 向量检索，支持 vector / keyword / hybrid（RRF）三种检索模式
- MCP 协议：stdio / http / streamable-http transport，AuthType none/bearer/api_key/oauth2，工具风险策略与 Agent approval/resume
- 多租户 IAM：PostgreSQL schema 物理隔离 + GitHub OAuth + JWT RS256 + tenant 邀请/角色管理
- LLM 网关：通义千问 / 智谱 AI 双 OpenAI-compat provider，TenantGatewayCache 按租户密钥缓存
- 可观测性：Zap 结构化日志 + OpenTelemetry tracing + Prometheus 指标 + Harness 生命周期

### 1.3 Architecture Style

分层 DDD 单体（Modular Monolith）。`api/http` 接入层 → `api/wiring.Container` 组合根 → 9 个 `internal/<ctx>/{domain,application,infrastructure}` bounded context → `pkg/` 无业务基础设施。跨 context 仅经消费者侧 `domain/port/` 接口 + wiring 层 thin adapter，禁止 import 兄弟 context 的 `application` / `infrastructure`。

---

## 2. Tech Stack

| 层级 | 选型 | 版本 |
|------|------|------|
| 后端语言 | Go | 1.25 |
| Web 框架 | Gin | v1.9 |
| RDB driver | pgx | v5 |
| Cache | go-redis | v9 |
| 消息总线 | NATS JetStream | v1.51 |
| 向量库 | Milvus SDK | v2.4.2 |
| JWT | golang-jwt | v5（RS256） |
| OAuth | GitHub OAuth | — |
| Migration | golang-migrate | v4 |
| 日志 | Zap | — |
| Tracing/Metrics | OpenTelemetry | v1.22 + Prometheus |
| LLM Provider | Qwen（dashscope）· Zhipu | OpenAI-compat |
| 配置 | `config/config.go` 环境变量读取 | stdlib `os` |
| 前端 | React 18.3 · Vite 5.4 · AntD 5.20 · React Router 6.26 · Axios 1.7 · TanStack Query v5 · Zustand v5 · Zod · Recharts | — |

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
│       ├── wiring.go           Container / BuildContainer（组合根）+ Shutdown 逆序释放
│       ├── memory.go           memory pipeline 装配
│       ├── tenant_resolver.go  TenantCapabilityResolver（per-tenant gateway 解析）
│       └── *.go                每域一个 wiring 文件
├── internal/                    9 bounded contexts
│   ├── agent/                  ReAct StateGraph · Registry · ExecutionStore · ChatStore · BaseAgent
│   │   └── application/graph/  StateGraph[T]・nodeLLM・nodeTool・条件边
│   ├── memory/                 MemoryManager · MemoryRepo port · pipeline（outbox→embedder→enricher）
│   ├── knowledge/              Workspace 聚合 · RAGService · IngestService · GraphRAG client
│   ├── skill/                  VersionService · SkillRevision · VersionRepo
│   ├── mcp/                    MCPService · ServerManager · MCPToolRegistry · tool policy
│   ├── iam/                    TenantService · AdminService · OnboardService · JWTService
│   ├── llmgateway/             Gateway · TenantGatewayCache · openai_compat client
│   ├── evaluation/             suite/run/job · optimization · experiment · feedback
│   └── platform/               Harness 生命周期与 runtime 启动编排
├── pkg/                         无业务基础设施
│   ├── storage/{postgres,redis,milvus,tenantnaming,tenantdb}
│   ├── messaging/nats
│   ├── observability           Logger · Metrics · Tracer · SpanFromContext
│   ├── crypto                  AES-256-GCM
│   ├── constants               跨包业务/超时常量
│   ├── migration               public schema 编号迁移
│   ├── reqctx                  trace_id / tenant_id ctx propagation
│   ├── httpclient · textchunk · ...
├── cmd/server/main.go           调用 wiring.BuildContainer 启动 Harness
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
| `users` | id (UUID), github_id, github_login, avatar_url, global_role, is_guest, expires_at, created_at | OAuth 与临时访客账号 |
| `tenants` | id, name, slug, plan(free\|pro\|enterprise), status, settings JSONB, is_default, deleted_at | settings 内置加密 `llm_api_keys` |
| `tenant_members` | tenant_id, user_id, role(owner\|admin\|member), UNIQUE(tenant_id,user_id) | |
| `refresh_tokens` | id, user_id, token_hash(sha256), expires_at | |

`invitations`、`tenant_api_keys`、`audit_logs`、`model_providers` 和 `models` 已由 migration 018 删除。当前公开模型目录来自 `internal/llmgateway/infrastructure/static_catalog.go`。

### 4.2 Tenant Schema（per-tenant，`SET LOCAL search_path = tenant_{id}, public`）

| 域 | 表 | 关键字段 / 约束 |
|----|----|----|
| Agent | `agents` | id TEXT PK, name UNIQUE, type DEFAULT react, llm_model, embed_model, max_iterations DEFAULT 10, max_context_tokens DEFAULT 8000 |
| | `agent_mcp_links` | (agent_id, server_id) CASCADE 双删 |
| | `agent_skill_links` | (agent_id, skill_id) CASCADE |
| | `agent_workspaces` | (agent_id, workspace_id) CASCADE |
| | `agent_executions` | id UUID, status CHECK('success','error'), input/output_preview, total_tokens, duration_ms |
| MCP | `mcp_configs` | id TEXT, transport, command/url, args/env/headers/auth_config/retry_config JSONB, timeout_sec DEFAULT 30 |
| Skill | `skills` | id, name UNIQUE, description, status, active_revision_id / draft_revision_id |
| | `skill_revisions` | parent/revision/status/source、content_hash、capability、activation_contract、instructions、requirements、publish_checks |
| Evaluation | `eval_suites` · `eval_suite_revisions` · `eval_cases` | 评估集、冻结修订和用例 |
| | `eval_runs` · `eval_case_results` · `evaluation_jobs` | 异步运行、逐例结果与租约 job |
| | `optimization_jobs` · `optimization_candidates` | 优化任务与候选版本 |
| | `evaluation_experiments` · `evaluation_deployments` · `evaluation_feedback` | 实验阶段、部署决策与反馈 |
| Memory | `memory_entries` | id UUID v7, conversation_id FK, user_id, agent_id FK, role, content, type DEFAULT short_term, importance FLOAT8, tags TEXT[], keywords TEXT[], token_estimate, expires_at, enriched_at; GIN trgm on content |
| | `memory_outbox` | id BIGSERIAL, message_id NOT NULL, payload JSONB |
| | `memory_summaries` | conversation_id FK, summary, covered_until, token_count |
| | `memory_facts` · `memory_entities` · `memory_extraction_queue` | Memory v2 事实、实体与提取队列 |
| Chat | `chat_conversations` | id UUID, agent_id FK, user_id, name DEFAULT '新会话', expires_at DEFAULT now()+30d, deleted_at（软删） |
| | `chat_messages` | id UUID v7, conversation_id FK CASCADE, role CHECK('user','assistant'), content, steps_json JSONB, is_error |
| Knowledge | `rag_workspaces` | id UUID, name UNIQUE, description, config JSONB |
| | `knowledge_docs` | workspace_id FK CASCADE, title, content, source, metadata JSONB |
| Knowledge | `knowledge_chunks` · `knowledge_parent_chunks` | 文档分块、父块与全文检索字段 |

`sessions`、`entities`、`entity_relations`、`memory_token_budgets`、`exec_history`、`llm_api_keys`、模型预设/配额、prompt/workflow/scheduled task/webhook 等旧表会在 tenant schema provisioning 时幂等删除。

### 4.3 Migration Timeline

001 public baseline · 002 is_default_tenant · 003 global admin owner · 004/005 agent_executions add+move-to-tenant · 006 agent_skill_links · 007 name unique · 008 memory_pipeline · 009 agent_context_tokens · 010 soft_delete_conversations · 011 uuid_v7 func · 012 pgcrypto · 013 trgm index · 014 cascade-delete fixes · 015 memory v2 marker · 016 agent memory enabled · 017 memory facts hard delete · 018 obsolete public tables cleanup · 019 guest accounts

**多租户 DDL 规则**：编号迁移仅操作 public；引用 tenant-only 表的 DDL 必须放 `pkg/storage/postgres/tenant_schema.sql` 由 `ProvisionAllTenantSchemas` 幂等应用；新增列必须 `ALTER TABLE … ADD COLUMN IF NOT EXISTS` 做 backfill。

### 4.4 Domain Aggregates

- **agent**：`AgentConfig`（聚合根）· `ChatConversation` · `ChatMessage` · `ExecutionRecord` · `Message/Thought/ToolCall/AgentResult/AgentState`（hoisted to domain）
- **memory**：`MemoryEntry`（id, type∈{short_term,long_term,entity,summary}, role, content, importance, vector []float32, tags, expires_at）· `Entity` · `SessionContext{TenantID,UserID,SessionID,AgentID}` · `MemoryFilter`（composable）
- **knowledge**：`Workspace` 聚合 · `WorkspaceConfig{EmbeddingModel,ChunkSize,ChunkOverlap,QueryMode,TopK}` · `MergeUpdate` 强制不可变
- **skill**：`SkillRevision` · `Capability` · `ActivationContract` · `Requirements` · `VersionRepo`
- **mcp**：`Server` · `Tool{Name,Description,InputSchema}` · `ToolPolicy` · `AuthConfig`（含 OAuth2 字段）· `RetryConfig`
- **iam**：`Tenant`（IsDefault, MemberCount）· `Member` · `Invitation`（TokenHash, ExpiresAt）· `UserTenantInfo` · `StoredSession`
- **llmgateway**：`CompletionRequest/Response` · `Message` · `Tool/ToolCall` · `TokenUsage` · `EmbeddingRequest/Response` · `LLMCompleter` port

### 4.5 State Transitions

- **agent_executions.status**: `success` ⊕ `error`（CHECK）
- **chat_messages.role**: `user` ⊕ `assistant`（CHECK）
- **chat_conversations**: 用户删除与 Agent 清理走硬删除；`deleted_at` 兼容历史软删记录，清理任务同时回收过期和历史软删会话
- **memory_entries.type**: `short_term` → `long_term`（pipeline enricher 提升）/ `entity` / `summary`
- **tenant_members.role**: `owner` ↔ `admin` ↔ `member`（owner-only 改写，禁 self-modify，admin 不能 remove admin）
- **users guest lifecycle**: `is_guest=true` 且 `expires_at` 到期后由回收流程清理

---

## 5. API Surface

### 5.1 HTTP（全部 `application/json`，文档上传除外）

路由没有统一 `/api` 前缀。公开端点为 `GET /health`、`GET /metrics`、`GET /models`。Auth 路由只有在 GitHub client ID 和 JWT service 可用时注册。

| 域 | 主要路径 | 权限摘要 |
|----|----------|----------|
| Auth | `/auth/github` · `/auth/github/callback` · `/auth/register` · `/auth/guest` · `/auth/refresh` · `/auth/logout` · `/auth/me` · `/auth/switch-tenant` · `/auth/create-tenant` | OAuth/register/guest 有限流；部分操作签发或刷新 JWT |
| Admin | `/admin/tenants` · `/admin/tenants/:id` | global admin |
| Tenant | `/tenant/list` · `/tenant/members` · `/tenant/members/:user_id/role` · `/tenant/settings` · `/tenant/embed-model` · `DELETE /tenant` | member 底线；设置写入需 active；删除 tenant 需 owner |
| Agent | `/agents` · `/agents/:id` · `/agents/:id/execute` · `/agents/:id/execute/stream` · `/agents/executions` · execution trace 子路由 | member 可读/执行，admin 写；执行需 active + rate limit |
| Chat | `/agents/:id/conversations` · `/conversations/:convID` · `/conversations/:convID/messages` | JWT + tenant context |
| Skill | `/skills` · `/skills/:id` · `/skills/:id/workspace` · draft capability/activation/instructions · publish | member 可读，active admin 写；无直接执行/测试路由 |
| Evaluation | `/evaluations/suites` · publish · runs/jobs · optimizations · experiments/evaluate · feedback | admin 创建/查询评估控制面；active member 可提交 feedback |
| Knowledge | `/knowledge/workspaces` · workspace stats/documents · `/knowledge/ingest` · `/knowledge/query` | member 读/查，admin 管理/摄取 |
| MCP | `/mcp/servers` · server tools/resources/config/reconnect · `/mcp/tool-policies` · `/mcp/status` | member 读取，admin 管理 server/policy；写操作需 active；工具仅由 Agent 内部执行 |
| Memory | `/memory` · `/memory/:id` · `/memory/clear` · `/memory/sessions` · `/memory/session/:session_id` · `/memory/stats` · `/memory/summary/:session_id` | JWT + tenant context + active tenant |

完整方法与角色矩阵见 `docs/agent/api.md` 和 `api/http/router.go`。

### 5.2 SSE Stream（`POST /agents/:id/execute/stream`）

响应是 `text/event-stream`。handler 先写 heartbeat 注释帧，再用 `data: {"token":"..."}` 发送增量，最终发送含 `done=true`、output、steps 和 tokensUsed 的 JSON。客户端断开会取消执行；`context.WithoutCancel` 用于隔离内部 Stream/Memory 调用链的错误回流，而不是让已断开的执行无限后台运行。

### 5.3 Error Response

冻结契约：`{"error": "<message>"}`。HTTP 状态由 `api/middleware/error_mapping.go` 中央表映射 domain sentinel：

- 401 `ErrInviterMissing`
- 403 6 个 forbidden sentinels（AdminOrOwner/Owner/SelfModify/OwnerRole/RemoveOwner/AdminRemove）
- 404 NotFound 系列（pgx.ErrNoRows · ErrWorkspace/Member/Tenant/Entry/Session/Skill NotFound · ErrServerNotFound）
- 409 Conflict（Workspace · Agent · MCP · Skill name/Linked）
- 422 `agentapp.ErrInvalidSkill`
- 400 InvalidSettings · ImmutableXxx · SkillNotPublishable
- default 500；`HTTPError` wrapper 支持显式 status override

---

## 6. Configuration

> `config/config.go` 直接读取环境变量并应用默认值；当前没有 Viper/yaml 配置装载链。生产配置 `config/prod.yaml` 仍禁止修改。

| Key | 必需 | 默认 | 说明 |
|-----|------|------|------|
| `PORT` | × | `8080` | HTTP 监听端口 |
| `POSTGRES_URL` | × | 本地 Compose DSN | PostgreSQL 连接串 |
| `REDIS_URL` | × | `redis://localhost:6379` | Redis 连接串 |
| `NATS_URL` | × | `nats://localhost:4222` | JetStream；Memory pipeline 复用 |
| `MILVUS_HOST` / `MILVUS_PORT` | × | `localhost` / `19530` | Milvus gRPC |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | × | `http://localhost:4317` | OTLP collector |
| `GITHUB_CLIENT_ID` / `GITHUB_CLIENT_SECRET` | Auth 必需 | 空 | GitHub OAuth；client ID 为空时 auth routes 不注册 |
| `JWT_PRIVATE_KEY_PEM` | Auth 必需 | 空 | RS256 PEM，同时作为 tenant LLM key 加密的密钥来源 |
| `GITHUB_CALLBACK_URL` | × | `http://localhost:8080/auth/github/callback` | OAuth callback |
| `FRONTEND_URL` | × | `http://localhost:3002` | CORS 与登录跳转 |
| `GLOBAL_ADMIN_GITHUB_LOGIN` | × | `ArchChuan` | 全局管理员登录名 |
| `SECURE_COOKIES` | × | false | 仅字符串 `true` 开启 |
| `GLOBAL_AGENT_SYSTEM_PROMPT` | × | 空 | 全局 Agent system prompt |
| `MEMORY_PIPELINE_ENABLED` | × | false | 启用异步记忆流水线 |
| `MEMORY_ENRICH_MODEL` / `MEMORY_SUMMARY_MODEL` | × | `qwen-turbo` / `qwen-plus` | Memory pipeline 模型 |

---

## 7. External Dependencies

| 服务 | 用途 | 失败影响 |
|------|------|---------|
| PostgreSQL | 主存储（public + per-tenant schema） | 致命：所有写操作不可用 |
| Redis | session / cache / rate-limit | 降级：部分缓存 miss，仍可运行 |
| NATS JetStream | memory pipeline outbox 消费、领域事件 | 异步 enrich 停滞，主路径仍可用 |
| Milvus | 向量检索（GraphRAG vector mode） | RAG vector/hybrid 不可用，keyword 仍工作 |
| LLM Provider（Qwen/Zhipu） | LLM 推理 + embedding | Agent 执行不可用 |
| GitHub OAuth | 登录 | 新登录不可用，已登录 session 仍有效 |
| MCP Servers | 外部工具（stdio/http/sse） | 该 server 工具调用失败，其他工具不影响 |
| Vault / AWS Secrets Manager | 密钥管理（生产） | 启动失败 |

---

## 8. Business Rules & Constraints

1. **多租户隔离**：所有 tenant 资源走 `SET LOCAL search_path = tenant_{id}, public`；编号 migration 仅碰 public，per-tenant DDL 必须放 `tenant_schema.sql` 由 `ProvisionAllTenantSchemas` 幂等应用。
2. **AI 不做控制逻辑**：路由 / 重试 / 状态机硬编码在 Go 层；LLM 仅做语言任务（生成、抽取）。
3. **Agent 执行**：`ExecuteStream` 构造独立的限时 execution context；SSE handler 监听 client disconnect 并显式 cancel。执行记录写入 `agent_executions`。
4. **Skill instruction capability package**：draft 可编辑 capability、activation 和 instruction bundle；publish 冻结版本；Agent 正常执行只激活 published instruction bundle。当前没有直接执行或草稿测试 HTTP 路由。
5. **Workspace 不可变字段**：`embedding_model` / `chunk_size` / `chunk_overlap` 创建后只读（`MergeUpdate` 强制）。
6. **Embed model**：tenant 级 set-once（`SetEmbedModel` 拒绝二次写入 → `ErrEmbedModelAlreadySet`）；agent 创建时未指定则继承 tenant default。
7. **Tenant 角色规则**：
   - `UpdateMemberRole` 仅 owner，禁自我修改，目标必须非 owner
   - `RemoveMember` 仅 owner/admin，禁自删，禁删 owner，admin 不能删 admin
   - 默认 tenant `is_default=true` 不可删（`ErrDefaultTenantDelete`）
8. **LLM API keys 加密**：AES-256-GCM 写入 `tenant.settings.llm_api_keys`；GET 返回 `mask = first6 + "••••••••"`；UPDATE 跳过 placeholder bullets，merge 已有 keys；写入后 `TenantGatewayCache.Invalidate(tenantID)`。
9. **JWT**：RS256（拒绝非 RSA 签名方法）；claims `sub/tid/role/global_role/jti/ava/ghl`；refresh token sha256 入库。
10. **Memory 范围**：entries/facts/entities 都按 tenant + user 隔离，并可进一步按 agent/scope 过滤；Memory v2 对 facts 使用硬删除语义。
11. **Memory pipeline**：写入 outbox（`message_id NOT NULL`）→ embedder 生成向量 → enricher 写 `memory_facts` / `memory_entities` 并更新 entry；摘要写 `memory_summaries`。
12. **ReAct loop**：节点 `nodeLLM` ↔ `nodeTool` 条件边；内置 knowledge/memory/continue-reasoning 工具硬编码处理；published Skill revision 作为 instruction activation 注入 system context，并将工具/知识范围收窄到 requirements 与 Agent 配置的交集。循环消息达到预算安全阈值时按完整 tool-call/tool-result 组压缩副本。
13. **GraphRAG mode**：`vector` embed→Milvus search · `keyword` Milvus KeywordSearch · `hybrid` RRF（VectorStore.HybridSearch）；collection 命名 `tenantdb.WorkspaceCollection`，fallback `{workspace}_kb`。
14. **MCP transport/policy**：`RetryConfig{Enabled, MaxRetries, InitialDelayMs, MaxDelayMs, BackoffFactor}` 描述重连退避；transport 支持 stdio/http/streamable-http。危险或未分类工具需 approval，HTTP 不提供通用工具执行端点。
15. **Chat**：`role` CHECK ∈ {user, assistant}；`steps_json` 存储 ReAct 步骤；`is_error` 标记；conversation `expires_at` 默认 +30 天；当前删除实现为硬删，`deleted_at` 仅兼容历史记录与清理。
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
- **密钥管理**：Vault / AWS Secrets Manager（生产），禁入 git；当前 tenant LLM key 加密密钥从 JWT private key material 派生
- **Token 存储**：前端 httpOnly cookie 或内存 Context，禁 localStorage
- **输入校验**：DTO `binding` tag + service 层 domain 不变量自检
- **Skill 发布**：activation name、object schema、instructions 与 requirements 在领域层校验；发布 revision 以内容哈希冻结
- **审计/追踪**：Agent 工具调用与执行事件写入 tenant-scoped `agent_tool_traces` / `agent_trace_events`

### 9.3 Error Handling

- 错误传播：`fmt.Errorf("operation: %w", err)` 逐层包裹
- 中央映射：`api/middleware/error_mapping.go` 单点维护 sentinel→HTTP 状态码
- 重试：Agent graph 的 `RetryFn` 与 MCP 配置重连退避均受次数和 context 约束
- 降级：循环历史摘要器未注入或失败时使用省略标记并继续主流程
- 异步：execution record fire-and-forget；memory pipeline outbox 解耦写入失败
- 软容错：MemoryManager nil repo nil-safe；MCP 单 server 失败不影响整体

---

## 10. Testing Strategy

| 类型 | 框架 | 覆盖模式 |
|------|------|---------|
| 单元 | Go 内建 `testing` + 表驱动 | application/domain 层 100%，mock port 接口；目标覆盖率 ≥80% |
| Repo 集成 | `pgxmock` / 真实 PG（`-tags=integration`） | infrastructure 层 SQL 翻译 |
| HTTP 契约 | golden file（`api/http/testdata/contracts/*.golden.json`） | `contract_test.go` 守护 API 向后兼容 |
| 前端 | Vitest + Testing Library | hooks / 组件回归测试 |
| 端到端 | Go e2e + Playwright | `test/e2e/` Memory 真实链路；`web/e2e/` 响应式用户流 |

**约定**：

- `mock` 所有外部依赖（NATS / Milvus / LLM provider）
- `-race` 在完整 PR 跑：`go test -v -race -timeout 30s ./...`
- 短测：`go test -short ./...` 排除集成 tag
- AI 辅助测试必须给模板文件（如 `api/http/handler/tenant_handler_test.go`）

---

## 11. Known Gaps & Assumptions

- **Skill 直接执行器已移除**：当前只有 instruction revision 激活路径；历史计划中的 code/llm/http executor 和 Skill Gateway 不属于现行实现。
- **前端覆盖率未设置强制阈值**；当前已有 Vitest/Testing Library 与 Playwright 用例。
- **MCP `oauth2` AuthType 实现深度未验证**（domain 字段已定义，token refresh 流程未细查）。
- **e2e 覆盖尚非全域**：已有 Memory lifecycle 和前端响应式用户流，但 Agent/Skill/MCP/Knowledge/IAM 尚未都有独立的真实环境套件。
- **`internal/agent/application/agent.go` 与 `registry.go` 内部细节未完整读**（BaseAgent.Execute 主体已通过 ReAct graph 间接确认）。
- **OTEL trace 端到端串联**（HTTP→Agent→Skill→LLM）在 README 路线图标注为「待完成」。
- **Workflow / scheduled task / webhook** 仍是路线图方向；对应旧 tenant tables 已由 provisioning 删除，当前没有可用运行时。
- **多 LLM provider 扩展**（Anthropic / Ollama / 本地）路线图待办；当前仅 Qwen + Zhipu。
- **通用业务审计日志** 尚无独立 `audit_logs` 表；当前可观察重点是 Agent execution/tool/trace 记录与结构化日志。
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
                          ├─→ internal/memory/infrastructure/pipeline (NATS JetStream)
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

# 2. 拉本地 infra（Postgres · Redis · NATS · Milvus）
make infra-up
make infra-wait

# 3. 启后端（启动时自动执行 public migration 与 tenant schema provisioning）
make run        # :8080

# 4. 启前端
make fe-dev     # :3002

# 5. 完整 PR 校验
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
