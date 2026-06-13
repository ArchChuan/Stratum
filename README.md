# Stratum

企业级 AI 原生应用架构底座 | AI Native Application Framework

## 定位

面向企业私有化部署的 AI 应用编排平台，融合：

- **OpenClaw Skill Gateway** — 原子化 Skill 执行，内置熔断器、流水线 DSL、审计日志
- **Hermes 事件总线** — 基于 NATS 的异步事件驱动通信
- **Harness 生命周期管理** — 统一组件注册、有序启停、健康检查
- **MCP 统一协议** — Model Context Protocol 工具/模型适配层
- **GraphRAG 知识增强** — Neo4j 知识图谱 + Milvus 向量检索
- **A2A 多智能体协作** — Agent-to-Agent 发现、协商、编排协议
- **三层记忆系统** — 短期（缓冲/窗口/摘要）+ 长期（向量）+ 实体记忆
- **多租户隔离** — PostgreSQL schema 级租户隔离，GitHub OAuth + JWT 认证

## 技术栈

| 组件 | 技术 | 版本 | 用途 |
|------|------|------|------|
| 语言 | Go | 1.24.1 | 高性能后端 |
| API 网关 | Gin | v1.9.1 | HTTP 服务框架 |
| 事件总线 | NATS JetStream | v1.31.0 | 异步事件驱动 |
| 向量数据库 | Milvus | v2.4.2 | 向量存储与检索 |
| 图数据库 | Neo4j | v5.28.4 | 知识图谱存储 |
| 关系数据库 | PostgreSQL (pgx v5) | v5.7.2 | 租户数据、Agent/Skill 持久化 |
| 缓存 | Redis | go-redis v9.7.3 | Token 存储、会话缓存 |
| 日志 | Uber Zap | v1.27.1 | 结构化日志 |
| 可观测 | OpenTelemetry + Prometheus | v1.21 / v1.23.2 | 链路追踪与指标 |
| 部署 | Kubernetes / Helm | — | 云原生部署 |
| 前端 | React 18 + Vite 4 + Ant Design 5.2 | — | Web 控制台 |

## 架构概览

```
┌─────────────────────────────────────────────────────────────┐
│                    React 前端 (web/)                         │
│  Ant Design 5.2 · React Router 6 · Axios · Vite 4          │
└────────────────────────┬────────────────────────────────────┘
                         │ HTTP/REST + SSE
┌────────────────────────▼────────────────────────────────────┐
│               Gin HTTP API (api/)                            │
│  Auth · TraceMiddleware · ErrorHandler · MetricsMiddleware   │
│  RequireActiveTenant · RequireTenantRole · CORS             │
└──┬──────────┬──────────┬──────────┬──────────┬─────────────┘
   │          │          │          │          │
   ▼          ▼          ▼          ▼          ▼
Agent      Skill      Memory      MCP       Knowledge
Registry   Handler    Manager    Manager    RAG Service
(CRUD +    (CRUD)    (3-layer)  (MCP SDK)  (ingest +
 execute)                                   query)
   │                                           │
   ▼                                           ▼
ReAct Graph                             Milvus + Neo4j
StateGraph[ReActState]                  (vector + graph)
LLM Node ↔ Tool Node
   │
   ▼
LLM Gateway (internal/llmgateway/)
  TenantGatewayCache (5-min TTL)
  ├── 通义千问 (Qwen)    dashscope.aliyuncs.com
  └── 智谱AI  (Zhipu)   open.bigmodel.cn
   │
   ▼
PostgreSQL (per-tenant schema)   Redis (token/session)
NATS JetStream (event bus)       OpenTelemetry + Prometheus
```

## 核心能力

| 能力 | 实现 |
|------|------|
| 多租户隔离 | PostgreSQL `tenant_<UUID>` schema，`SET LOCAL search_path` 切换 |
| 认证授权 | GitHub OAuth + JWT RS256（golang-jwt v5），全局管理员角色分离 |
| 租户状态守卫 | `RequireActiveTenant` 中间件：`status != "active"` → 403 |
| ReAct 智能体 | 自托管 `StateGraph[ReActState]`，LLM 节点 + 工具节点，条件边路由 |
| per-tenant LLM 密钥 | `TenantGatewayCache`：5 分钟 TTL，AES-GCM 解密，注入 `CapabilityRequest` |
| 工具调用 | `CapabilityGateway.Route()` 路由 Skill/LLM 请求；内置 `search_knowledge` RAG 工具 |
| 流式输出 | SSE `POST /agents/:id/execute/stream`，`OnToken` 回调逐 token 推送 |
| 对话持久化 | `chat_conversations` + `chat_messages` 表，会话 30 天过期 |
| Agent ↔ MCP 绑定 | `agent_mcp_links` 联接表，随 Agent CRUD 原子维护 |
| Agent ↔ 知识库绑定 | `agent_workspaces` 联接表，`search_knowledge` 工具按 workspace 检索 |
| GraphRAG | 文档解析 → 分块 → 嵌入 → Milvus（向量）+ Neo4j（图谱） |
| 三层记忆 | ShortTerm（Buffer/Window/Summary）+ LongTerm（向量）+ Entity |
| A2A 协作 | sequential / parallel / hierarchical / pipeline / swarm 五种策略 |
| 可观测 | Zap 结构化日志（`react.llm` / `react.tool` 事件）+ OTEL + Prometheus `/metrics` |

## 目录结构

```
.
├── cmd/server/main.go          # 入口：Harness 生命周期，组件注册
├── api/
│   ├── router.go               # 所有路由集中定义
│   ├── handler/                # 每域一个 handler 文件
│   │   ├── agent_handler.go    # Agent CRUD + execute + stream
│   │   ├── chat_handler.go     # 对话会话 + 消息 CRUD
│   │   ├── skill_handler.go
│   │   ├── rag_handler.go
│   │   ├── memory_handler.go
│   │   ├── mcp_handler.go
│   │   ├── auth_handler.go
│   │   ├── admin_handler.go
│   │   ├── tenant_handler.go
│   │   └── model_handler.go
│   ├── model/                  # Request/Response DTO
│   └── middleware/             # ErrorHandler · Trace · Auth · Metrics · CORS
├── internal/
│   ├── agent/
│   │   ├── agent.go            # AgentConfig, BaseAgent
│   │   ├── registry.go         # CRUD + agent_mcp_links + agent_workspaces
│   │   ├── execution_store.go  # exec_history 持久化
│   │   ├── chat_store.go       # chat_conversations + chat_messages
│   │   └── graph/
│   │       ├── react.go        # BuildReActGraph, ReActState
│   │       ├── state_graph.go  # StateGraph[S], CompiledGraph[S]
│   │       └── retry.go        # RetryFn with exponential backoff
│   ├── auth/                   # GitHub OAuth, JWT RS256, TokenStore
│   ├── capgateway/             # CapabilityGateway interface + LLM adapter
│   ├── llmgateway/
│   │   ├── gateway.go          # LLM Gateway 核心
│   │   ├── tenant_cache.go     # TenantGatewayCache (5-min TTL)
│   │   ├── qwen.go             # 通义千问 client
│   │   └── zhipu.go            # 智谱AI client
│   ├── knowledge/              # GraphRAG: ingest + rag_service
│   ├── memory/                 # 三层记忆系统
│   ├── mcp/                    # MCP client manager + skill registry
│   ├── agent/a2a/              # A2A 多智能体协议
│   └── config/                 # Viper 配置加载
├── pkg/
│   ├── tenantdb/               # schema.go (go:embed), tenant_schema.sql
│   ├── observability/          # Zap logger, Prometheus metrics, OTEL
│   ├── crypto/                 # AES-GCM key derivation
│   └── vector/                 # Milvus vector store wrapper
└── web/                        # React 前端
    └── src/
        ├── pages/              # 路由页面
        ├── components/         # 共享 UI 组件
        ├── hooks/              # 自定义 Hook
        ├── services/api.js     # 唯一 axios 实例
        ├── contexts/           # React Context
        └── utils/              # 纯函数工具
```

## API 端点

### 认证 `/auth`

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/auth/github` | 发起 GitHub OAuth |
| GET | `/auth/github/callback` | OAuth 回调 |
| POST | `/auth/register` | 邮箱注册 |
| POST | `/auth/refresh` | 刷新 JWT |
| POST | `/auth/logout` | 登出 |
| GET | `/auth/me` | 当前用户信息 |
| POST | `/auth/switch-tenant` | 切换租户 |
| POST | `/auth/create-tenant` | 创建租户 |

### Agent `/agents`

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/agents` | 列出当前租户所有 Agent |
| POST | `/agents` | 创建 Agent（需租户 active） |
| GET | `/agents/executions` | 执行历史列表 |
| GET | `/agents/:id` | 获取 Agent 详情（含 MCP/知识库绑定） |
| PUT | `/agents/:id` | 更新 Agent（需租户 active） |
| DELETE | `/agents/:id` | 删除 Agent（需租户 active） |
| POST | `/agents/:id/execute` | 同步执行（JSON 响应） |
| POST | `/agents/:id/execute/stream` | 流式执行（SSE，`data:` 前缀逐 token） |
| POST | `/agents/:id/conversations` | 新建对话会话 |
| GET | `/agents/:id/conversations` | 列出 Agent 的对话会话 |

### 对话会话 `/conversations`

| 方法 | 路径 | 说明 |
|------|------|------|
| PATCH | `/conversations/:convID` | 重命名会话 |
| DELETE | `/conversations/:convID` | 删除会话 |
| GET | `/conversations/:convID/messages` | 获取消息列表 |
| POST | `/conversations/:convID/messages` | 添加消息 |

### Skill `/skills`

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/skills` | 列出 Skill |
| POST | `/skills` | 创建 Skill |
| GET | `/skills/:id` | 获取 Skill |
| PUT | `/skills/:id` | 更新 Skill |
| DELETE | `/skills/:id` | 删除 Skill |

### 知识库 `/knowledge`

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/knowledge/workspaces` | 列出知识空间 |
| POST | `/knowledge/workspaces` | 创建知识空间（admin） |
| PATCH | `/knowledge/workspaces/:name` | 更新知识空间（admin） |
| DELETE | `/knowledge/workspaces/:name` | 删除知识空间（admin） |
| GET | `/knowledge/workspaces/:name/stats` | 空间统计 |
| POST | `/knowledge/query` | RAG 检索查询 |
| POST | `/knowledge/ingest` | 上传文档并入库（admin） |

### 记忆 `/memory`

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/memory/sessions` | 创建记忆会话 |
| POST | `/memory` | 写入记忆条目 |
| GET | `/memory/:id` | 读取记忆 |
| POST | `/memory/search` | 语义搜索记忆 |
| DELETE | `/memory/:id` | 删除记忆 |
| GET | `/memory/stats` | 记忆统计 |
| DELETE | `/memory/session/:session_id` | 清空会话记忆 |
| GET | `/memory/entities` | 实体列表 |
| POST | `/memory/extract-entities` | 从文本抽取实体 |
| GET | `/memory/summary/:session_id` | 会话摘要 |

### 租户 `/tenant`

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/tenant/list` | 当前用户所有租户 |
| GET | `/tenant/members` | 成员列表 |
| POST | `/tenant/members/invite` | 邀请成员 |
| PATCH | `/tenant/members/:user_id/role` | 修改成员角色 |
| DELETE | `/tenant/members/:user_id` | 移除成员 |
| GET | `/tenant/settings` | 获取租户设置（含加密 LLM 密钥 hint） |
| PATCH | `/tenant/settings` | 更新租户设置（需租户 active） |

### 全局管理 `/admin`（需 global_admin 角色）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/admin/tenants` | 所有租户 |
| POST | `/admin/tenants` | 创建租户 |
| GET | `/admin/tenants/:id` | 租户详情 |
| PATCH | `/admin/tenants/:id` | 更新租户（suspend/activate） |
| DELETE | `/admin/tenants/:id` | 删除租户 |

### 其他

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/models` | 可用 LLM 模型列表 |
| GET | `/health` | 健康检查 |
| GET | `/metrics` | Prometheus 指标 |

## Agent 系统

### AgentConfig

```go
type AgentConfig struct {
    ID                    string
    Name                  string
    Type                  AgentType         // "react"
    Description           string
    Persona               string
    SystemPrompt          string
    LLMModel              string
    MaxIterations         int
    AllowedSkills         []string          // Skill ID 列表
    MCPServerIDs          []string          // 关联 MCP 服务器 ID（agent_mcp_links）
    KnowledgeWorkspaceIDs []string          // 关联知识空间 ID（agent_workspaces）
    KnowledgeWorkspaceNames []string        // 冗余名称，只读
}
```

### ReAct 图执行引擎

`internal/agent/graph/` 实现了泛型 `StateGraph[S]`，完全自托管，不依赖任何工作流引擎。

```
BuildReActGraph(capGW, logger)
    ├── AddNode("llm",  makeLLMNode)     # 调用 LLM，处理 tool_calls
    ├── AddNode("tool", makeToolNode)    # 执行 Skill / search_knowledge
    ├── AddConditionalEdge("llm", fn)    # has tool_calls → "tool"，否则 → END
    ├── AddEdge("tool", "llm")           # 工具结果回 LLM
    └── SetEntryPoint("llm")
```

`ReActState` 携带：`TenantID`、`TraceID`、`LLMAPIKeys`、`Model`、`Messages`、`AvailableTools`、`OnToken`（流式回调）、`RAGSearchFn`（知识库检索回调）。

### 内置工具

| 工具名 | 触发方式 | 说明 |
|--------|----------|------|
| `search_knowledge` | LLM tool_call | 按 workspace 名称在 Milvus + Neo4j 中检索，topK ≤ 20 |
| `<skill_id>` | LLM tool_call | 通过 `CapabilityGateway.Route(CapSkill)` 路由执行 |

### 执行日志事件

| 事件 | 级别 | 字段 |
|------|------|------|
| `react.llm` | INFO/ERROR | `trace_id` `tenant_id` `model` `step` `tokens` `total_tokens` `latency_ms` `has_tool_calls` |
| `react.tool` | INFO/ERROR | `trace_id` `tenant_id` `tool_name` `latency_ms` |

## per-tenant LLM 密钥缓存

```go
// internal/llmgateway/tenant_cache.go
type TenantGatewayCache struct { /* sync.Mutex + entries map */ }
type cacheEntry struct {
    gateway   *Gateway
    apiKeys   map[string]string  // provider → plaintext key
    expiresAt time.Time          // 5 分钟 TTL
}
```

`resolveTenantGateway(ctx, db, tenantID, baseGW, aesKey, cache)` 流程：

1. 命中缓存 → 直接返回
2. 未命中 → 查 `tenant_settings.llm_api_keys_encrypted`，AES-GCM 解密
3. 构造带密钥的 `*Gateway`，写入缓存

`CapabilityRequest.LLMAPIKeys` 将密钥注入 LLM 节点，每次调用用租户密钥覆盖全局配置。

## LLM 提供商

| 提供商 | 包 | Base URL |
|--------|----|----------|
| 通义千问（Qwen） | `internal/llmgateway/qwen.go` | `https://dashscope.aliyuncs.com/compatible-mode/v1` |
| 智谱AI（Zhipu） | `internal/llmgateway/zhipu.go` | `https://open.bigmodel.cn/api/paas/v4` |

两者均实现 OpenAI 兼容接口，HTTP 超时 60 秒。

## 数据库 Schema（租户 Schema）

租户 Schema 由 `pkg/tenantdb/schema.go` 通过 `go:embed tenant_schema.sql` 自动在租户创建时执行，幂等。

### 核心业务表

| 表 | 主键 | 说明 |
|----|------|------|
| `agents` | `TEXT` | Agent 配置，`allowed_skills TEXT[]` |
| `skills` | `TEXT` | Skill 配置，`config JSONB` |
| `mcp_configs` | `TEXT` | MCP 服务器配置 |
| `agent_mcp_links` | `(agent_id, server_id)` | Agent ↔ MCP 联接，CASCADE DELETE |
| `rag_workspaces` | `UUID` | 知识空间，`name UNIQUE` |
| `agent_workspaces` | `(agent_id, workspace_id)` | Agent ↔ 知识空间联接，CASCADE DELETE |
| `chat_conversations` | `UUID` | 对话会话，`expires_at = NOW()+30d` |
| `chat_messages` | `UUID` | 消息，`role CHECK('user','agent')`，`steps_json JSONB` |
| `sessions` | `UUID` | 执行会话 |
| `memory_entries` | `UUID` | 三层记忆条目 |
| `exec_history` | `UUID` | Agent 执行历史 |
| `llm_api_keys` | `UUID` | 加密存储的 LLM API Key |
| `model_quotas` | `UUID` | 模型配额管理 |
| `webhooks` / `webhook_deliveries` | `UUID` | Webhook 事件推送 |

## 中间件

| 中间件 | 作用 |
|--------|------|
| `TraceMiddleware` | 注入 `request_id`、`trace_id`、`tenant_id`、`user_id` 到 context 和日志 |
| `ErrorHandler` | 统一错误响应格式 `{"code": N, "message": "..."}` |
| `MetricsMiddleware` | 记录 HTTP 请求延迟和状态码到 Prometheus |
| `CORSMiddleware` | 跨域白名单，仅允许配置的 `FrontendURL` |
| `RequireActiveTenant` | 查 `public.tenants` 表，`status != "active"` → 403 |
| `RequireTenantRole` | 验证 JWT claims 中的租户角色（member/admin） |
| `RequireGlobalAdmin` | 验证 `global_admin` 角色，仅用于 `/admin` 路由 |
| `InjectTenantContext` | 从 JWT 提取 `tenant_id`，注入 `pkg/tenantdb.TenantContext` |

## 前端页面

| 页面 | 路径 | 说明 |
|------|------|------|
| `DashboardPage` | `/` | 仪表盘 |
| `AgentsListPage` | `/agents` | Agent 列表 |
| `CreateAgentPage` | `/agents/create` | 创建 Agent（含 Skill/MCP/知识库绑定） |
| `EditAgentPage` | `/agents/:id/edit` | 编辑 Agent（含 Skill/MCP/知识库绑定） |
| `AgentChatPage` | `/chat` | Agent 对话 |
| `ExecutionHistoryPage` | `/history` | 执行历史 |
| `SkillsListPage` | `/skills` | Skill 列表 |
| `CreateSkillPage` | `/skills/create` | 创建 Skill |
| `KnowledgePage` | `/knowledge` | 知识空间列表 |
| `KnowledgeDetailPage` | `/knowledge/:name` | 知识空间详情（文档、统计） |
| `MemoryPage` | `/memory` | 记忆管理 |
| `MCPServersPage` | `/mcp` | MCP 服务器管理 |
| `MembersPage` | `/tenant/members` | 租户成员管理 |
| `SettingsPage` | `/tenant/settings` | 租户设置（LLM 密钥配置） |
| `TenantsListPage` | `/admin/tenants` | 全局租户管理（global_admin） |

## 开发

### 依赖

- Go 1.24.1
- Node.js 18+
- PostgreSQL 15+
- Redis 7+
- Milvus 2.4.x
- Neo4j 5.x
- NATS 2.x

### 后端

```bash
# 运行测试
go vet && go test -short ./...

# 完整测试（含竞态检测）
go test -v -race -timeout 30s ./...

# 启动服务（需 .env 配置）
go run cmd/server/main.go
```

### 前端

```bash
cd web
npm install
npm run dev    # 开发服务器
npm run build  # 生产构建
npm run lint   # ESLint 检查
```

### 环境变量（关键项）

| 变量 | 说明 |
|------|------|
| `DATABASE_URL` | PostgreSQL 连接串 |
| `REDIS_URL` | Redis 连接串 |
| `NATS_URL` | NATS 连接串 |
| `MILVUS_HOST` / `MILVUS_PORT` | Milvus 地址 |
| `NEO4J_URI` / `NEO4J_USER` / `NEO4J_PASSWORD` | Neo4j 连接 |
| `JWT_PRIVATE_KEY_PEM` | RS256 私钥（PEM 格式）|
| `GITHUB_CLIENT_ID` / `GITHUB_CLIENT_SECRET` | GitHub OAuth |
| `GLOBAL_ADMIN_GITHUB_LOGIN` | 全局管理员 GitHub 用户名 |
| `FRONTEND_URL` | 前端地址（CORS 白名单）|
| `SECURE_COOKIES` | 生产环境设为 `true` |

密钥禁止入 git，通过 Vault / AWS Secrets Manager / `.env`（本地开发）注入。
