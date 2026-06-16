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
- **持久化记忆系统** — PostgreSQL-backed 记忆存储 + 向量语义检索 + 实体图谱 + 异步 Pipeline
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
| 三层记忆 | DB-backed 记忆存储（memory_entries）+ LongTerm（向量）+ Entity + 异步 Pipeline |
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
│   ├── memory/                 # 持久化记忆系统（DB-backed）
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

## 记忆系统（Memory Module）

完全持久化的记忆系统，所有数据存储在 PostgreSQL 租户 schema 中，无内存缓存层。

### 架构

```
┌─────────────────────────────────────────────────────┐
│                  MemoryManager                       │
│  (internal/memory/manager.go)                       │
├─────────────────────────────────────────────────────┤
│  execTenant(ctx, tenantID, fn)                      │
│  SET LOCAL search_path = "tenant_{id}", public      │
├──────────┬──────────────┬───────────────────────────┤
│  DB 层   │  Vector 层   │  Entity 层               │
│ memory_  │  Milvus      │  entities +              │
│ entries  │  VectorMemory│  entity_relations        │
└──────────┴──────────────┴───────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────────────────┐
│           Memory Pipeline (async)                    │
│  memory_outbox → NATS JetStream → enricher          │
│  memory_summaries · memory_token_budgets            │
└─────────────────────────────────────────────────────┘
```

### 核心接口

| 方法 | 说明 |
|------|------|
| `Add(ctx, entry)` | 写入 DB + 可选写入 Milvus 向量 + 可选实体抽取 |
| `Get(ctx, id)` | 按 ID 从 `memory_entries` 查询 |
| `Search(ctx, req)` | DB 关键字 + Milvus 语义搜索混合 |
| `Delete(ctx, id)` | 从 `memory_entries` 删除 |
| `Clear(ctx, sessionCtx)` | 按 session_id 清空 |
| `GetStats(ctx, sessionCtx)` | 聚合统计（条目数、实体数、会话数） |
| `GetSummary(ctx, sessionCtx)` | 从 `memory_summaries` 获取最新摘要 |
| `Cleanup(ctx)` | No-op（过期由 `expires_at` 列在查询时过滤） |

### 配置（MemoryConfig）

| 字段 | 默认值 | 说明 |
|------|--------|------|
| `EnableVectorSearch` | `true` | 启用 Milvus 向量检索 |
| `VectorCollection` | `"memory_vectors"` | Milvus collection 名 |
| `MaxVectorResults` | `5` | 语义搜索最大返回数 |
| `MinRelevanceScore` | `0.7` | 最低相关度阈值 |
| `EnableEntityExtraction` | `true` | 启用实体抽取 |
| `EntityThreshold` | `0.8` | 实体置信度阈值 |
| `EnablePersistence` | `true` | 启用持久化 |
| `PersistenceInterval` | `5min` | 持久化间隔 |
| `MaxMemoryAge` | `30d` | 最大记忆保留时间 |

### 文件结构

```
internal/memory/
├── manager.go          # MemoryManager 主逻辑（execTenant + CRUD）
├── types.go            # MemoryEntry, MemoryConfig, Entity, SearchRequest 等类型
├── defaults.go         # 常量：DefaultPersistInterval, DefaultMaxMemoryAge, DefaultSearchLimit
├── interface.go        # VectorMemory, EntityMemory, Persistence 接口定义
├── manager_test.go     # Manager 单元测试
├── memory_test.go      # 类型与配置测试
└── pipeline/           # 异步 Pipeline（outbox → NATS → enricher）
```

### 写入路径

#### 路径 A：Pipeline 异步写入（主路径）

```
Handler → chatStore.AddMessage()
  ├─ INSERT chat_messages                          [同步]
  └─ INSERT memory_outbox {message_id, conversation_id, tenant_id, user_id, agent_id, role, content}

OutboxPoller（每N秒轮询所有租户 schema）
  SELECT memory_outbox FOR UPDATE SKIP LOCKED
  → js.Publish(MEMORY_RAW.<tenantID>)
  → DELETE FROM memory_outbox WHERE id = ANY(...)

EmbedderWorker（消费 NATS MEMORY_RAW stream）
  embedSvc.EmbedVector(content)
  → vectorDB.Upsert(tenantID, messageID, vector, metadata)  [Milvus]
  → js.Publish(MEMORY_ENRICHED.<tenantID>)

EnricherWorker（消费 NATS MEMORY_ENRICHED stream）
  callEnrichLLM(content) → {entities, importance, keywords, token_estimate}
  → INSERT entities (name, type, user_id, ...)
  → UPDATE memory_entries SET importance/tags/metadata
  → INSERT memory_summaries (当消息数超过 threshold)
```

> `memory_outbox.message_id` 是 NOT NULL，调用方必须显式传入 UUID v7。

#### 路径 B：MemoryManager.Add()（同步直写，REST API）

```
MemoryHandler.AddMemory() → manager.Add(entry)
  ├─ longTerm.AddWithVector(entry, vector)   [仅 EnableVectorSearch=true]
  ├─ entity.ExtractEntities(content)         [仅 EnableEntityExtraction=true]
  └─ INSERT memory_entries (id, type, role, content, session_id, user_id, agent_id, ...)
```

此路径**不写 memory_outbox**，绕过 NATS Pipeline 直接落 DB + Milvus。

### 读取路径

#### 路径 1：MemoryInjector.BuildContext（注入 system prompt）

每次 `BaseAgent.Execute()` 在构建消息前触发。**前提**：`a.MemoryInjector != nil && cfg.ConversationID != ""`（与 `EnableMemory` 无关）。

```
MemoryInjector.BuildContext(InjectionContext{TenantID, UserID, AgentID, ConversationID, Query})
  SET LOCAL search_path = tenant_<id>, public
  ├─ SELECT summary FROM memory_summaries WHERE conversation_id=? ORDER BY created_at DESC LIMIT 1
  ├─ SELECT name FROM entities WHERE user_id=? ORDER BY last_seen DESC LIMIT N
  └─ embedSvc.EmbedVector(query)           ← 先用全局 embedSvc，nil 则 embedResolver(tenantID)
     → vectorDB.Search(collection, vec, topK)  [Milvus 长期记忆检索]
  → 拼装 "[Memory Context]\nSummary:...\nKey Entities:...\nLong-term Memory:..."
  → 经 BuildContextMessages() 注入 ReActState.Messages[0].Content 前缀
```

#### 路径 2：ChatStore.ListMessages（短期对话历史）

每次 `BaseAgent.Execute()` 触发：

```
chatStore.ListMessages(tenantID, conversationID, userID)
  SELECT FROM chat_messages WHERE conversation_id=? AND user_id=? AND deleted_at IS NULL
  → []*ChatMessage（时间升序）
→ BuildContextMessages(systemPrompt, memCtx, history, input, maxTokens, historyWindow)
  → 按 token 预算 + historyWindow 截断后送入 ReActState.Messages
```

#### 路径 3：MemoryManager.Search（主动语义检索，按需）

```
agent.RetrieveMemory(query, limit) / MemoryHandler.SearchMemory()
  → manager.Search(req)
       longTerm.SemanticSearch(query, sessionCtx, limit)  [向量检索]
       → MinScore 过滤 + Limit 截断 → []*MemorySearchResult
```

#### 数据流总览

```
用户消息
  │
  ├─→ chat_messages ──────────────── 路径2（短期历史）──→ ReAct messages[]
  └─→ memory_outbox
         │ OutboxPoller
         ▼
       NATS MEMORY_RAW
         │ EmbedderWorker
         ├─→ Milvus（向量）────────── 路径1&3（语义检索）──→ system prompt / Search API
         └─→ NATS MEMORY_ENRICHED
                │ EnricherWorker
                ├─→ entities ─────── 路径1（实体注入）──→ system prompt
                └─→ memory_summaries 路径1（摘要注入）──→ system prompt
```

### 设计决策

| 决策 | 原因 |
|------|------|
| 移除内存 shortTerm 层 | ChatStore 已是对话历史单一来源，shortTerm 纯属冗余 |
| 全量 DB-backed | 多实例部署无状态，重启无数据丢失 |
| execTenant 事务隔离 | 租户数据零交叉，`SET LOCAL search_path` 在事务内生效 |
| Cleanup no-op | 依赖 `expires_at` 列在查询时自然过滤，避免定时任务开销 |
| Pipeline 异步化 | 摘要生成/向量化不阻塞主请求路径 |

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

## 数据模型架构

采用 PostgreSQL **schema 级多租户隔离**：`public` schema 存全局元数据，每个租户拥有独立的 `tenant_<UUID>` schema 存业务数据。

### Public Schema（全局）

由 `internal/migration/sql/` 编号迁移管理，`golang-migrate` 执行。

| 表 | 主键 | 说明 |
|----|------|------|
| `users` | `UUID` | 用户账号，GitHub OAuth 关联 |
| `tenants` | `UUID` | 租户，`slug UNIQUE`，`is_default` 唯一索引 |
| `tenant_members` | `UUID` | 租户成员关系，`(tenant_id, user_id) UNIQUE` |
| `invitations` | `UUID` | 成员邀请，`token_hash` + `expires_at` |
| `refresh_tokens` | `UUID` | JWT Refresh Token，设备维度 |
| `tenant_api_keys` | `UUID` | 租户 API Key，`scopes TEXT[]` |
| `audit_logs` | `UUID` | 审计日志，`(tenant_id, action, resource)` |
| `model_providers` | `TEXT` | LLM 供应商目录（openai/anthropic/ollama） |
| `models` | `TEXT` | 模型目录，含 context_window / cost / capability |

```
users ──< tenant_members >── tenants
  │                            │
  └──< refresh_tokens          ├──< invitations
  └──< audit_logs              └──< tenant_api_keys

model_providers ──< models
```

### Tenant Schema（per-tenant 隔离）

由 `pkg/tenantdb/schema.go`（`go:embed tenant_schema.sql`）在租户创建时幂等执行。通过 `SET LOCAL search_path = tenant_<UUID>, public` 切换。

#### Agent & Skill

| 表 | 主键 | 说明 |
|----|------|------|
| `agents` | `TEXT` | Agent 配置，`name UNIQUE` |
| `skills` | `TEXT` | Skill 配置，`type` + `config JSONB` |
| `mcp_configs` | `TEXT` | MCP 服务器（stdio/sse），`name UNIQUE` |
| `agent_mcp_links` | `(agent_id, server_id)` | Agent ↔ MCP 联接 |
| `agent_skill_links` | `(agent_id, skill_id)` | Agent ↔ Skill 联接 |
| `agent_workspaces` | `(agent_id, workspace_id)` | Agent ↔ 知识空间联接 |

#### 对话 & 消息

| 表 | 主键 | 说明 |
|----|------|------|
| `chat_conversations` | `UUID` | 对话会话，30 天过期，`idx(agent_id, user_id, expires_at)` |
| `chat_messages` | `UUID` | 消息，`role IN ('user','agent')`，`steps_json JSONB` |

#### 记忆系统

| 表 | 主键 | 说明 |
|----|------|------|
| `sessions` | `UUID` | 记忆/执行会话 |
| `memory_entries` | `UUID` | 记忆条目，含 `conversation_id`、`keywords`、`token_estimate`、`scope_layer`、`enriched_at` |
| `entities` | `UUID` | 实体图节点，`idx(user_id, COALESCE(agent_id,''), name, type)` UNIQUE |
| `entity_relations` | `UUID` | 实体关系边 |

#### Memory Pipeline（异步 outbox → embedder → enricher）

| 表 | 主键 | 说明 |
|----|------|------|
| `memory_outbox` | `BIGSERIAL` | 待处理消息队列，`message_id` + `payload JSONB` |
| `memory_summaries` | `UUID` | 对话摘要，按 `conversation_id` 索引 |
| `memory_token_budgets` | `conversation_id` | 累积 token 计数，触发摘要阈值 |

#### 知识 & RAG

| 表 | 主键 | 说明 |
|----|------|------|
| `rag_workspaces` | `UUID` | 知识空间，`name UNIQUE` |
| `knowledge_docs` | `UUID` | 文档元数据 + 全文 |

#### 执行 & 运维

| 表 | 主键 | 说明 |
|----|------|------|
| `exec_history` | `UUID` | Skill 执行历史 |
| `agent_executions` | `UUID` | Agent 执行记录（tenant 版本） |
| `llm_api_keys` | `UUID` | 加密 LLM API Key |
| `model_presets` | `UUID` | 模型预设配置 |
| `model_usage` | `UUID` | 用量统计 |
| `model_quotas` | `UUID` | 配额管理 |
| `prompt_templates` | `UUID` | 提示词模板 |

#### 自动化

| 表 | 主键 | 说明 |
|----|------|------|
| `workflows` | `UUID` | 工作流定义 |
| `workflow_runs` | `UUID` | 工作流运行记录 |
| `scheduled_tasks` | `UUID` | 定时任务（cron） |
| `webhooks` | `UUID` | Webhook 配置 |
| `webhook_deliveries` | `UUID` | Webhook 投递记录 |

#### Tenant Schema ER 关系

```
agents ──< agent_mcp_links >── mcp_configs
  │    ──< agent_skill_links >── skills
  │    ──< agent_workspaces >── rag_workspaces ──< knowledge_docs
  │    ──< chat_conversations ──< chat_messages
  │    │                      ──< memory_summaries
  │    │                      ──< memory_token_budgets
  │    ──< scheduled_tasks
  │    ──< exec_history ──> skills
  │    ──< agent_executions
  │
sessions ──< memory_entries ──> chat_conversations
entities ──< entity_relations
```

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

## 认证、授权与安全架构

### 整体设计

```
┌──────────────────────────────────────────────────────────────────────────┐
│                          安全分层架构                                      │
├──────────────────────────────────────────────────────────────────────────┤
│  L1 传输层    │ TLS 1.2+ · CORS 白名单（仅 FrontendURL）                  │
│  L2 认证层    │ GitHub OAuth 2.0 → JWT RS256 双令牌体系                    │
│  L3 授权层    │ 全局角色 + 租户角色 双维度 RBAC                             │
│  L4 数据隔离  │ PostgreSQL schema 级租户隔离 · AES-256-GCM 密钥加密         │
│  L5 审计层    │ audit_logs 表 · 结构化日志（Zap）· OTEL 链路追踪            │
└──────────────────────────────────────────────────────────────────────────┘
```

### 认证流程

#### GitHub OAuth 2.0 登录

```
浏览器                     Stratum API                    GitHub
  │                            │                            │
  ├─ GET /auth/github ────────►│                            │
  │                            ├─ 生成 random state         │
  │                            ├─ Set-Cookie: oauth_state   │
  │◄─ 302 → github.com/login/oauth/authorize ──────────────►│
  │                            │                            │
  │◄─────────────── 用户授权 ──────────────────────────────►│
  │                            │                            │
  ├─ GET /auth/github/callback?code=X&state=Y ────────────►│
  │                            ├─ 验证 state == cookie      │
  │                            ├─ ExchangeCode(code) ──────►│
  │                            │◄── access_token ───────────┤
  │                            ├─ GetUser(token) ──────────►│
  │                            │◄── {id, login, avatar} ────┤
  │                            │                            │
  │                            ├─ 判断用户类型：             │
  │                            │  ├─ 已有租户 → issueTokenPair → 302 → /auth/callback?access_token=JWT
  │                            │  ├─ 新用户 → AutoJoinDefaultTenant → issueTokenPair → 302
  │                            │  └─ 无默认租户 → SignOnboarding → 302 → /auth/callback?onboarding_token=OB
  │◄─ 302 + Set-Cookie: refresh_token (httpOnly) ──────────│
  │                            │                            │
```

#### 双令牌体系

| 令牌类型 | 算法 | TTL | 存储位置 | 用途 |
|----------|------|-----|----------|------|
| Access Token | RS256 | 短期（`constants.AccessTokenTTL`） | 前端内存（`AuthContext`） | API 请求 Bearer 认证 |
| Refresh Token | 随机 32 字节 base64 | 长期（`constants.RefreshTokenTTL`） | httpOnly Cookie + PostgreSQL（SHA-256 hash） | 静默续期 |
| Onboarding Token | RS256 | 短期（`constants.OnboardingTTL`） | URL query param | 新用户注册流程 |

#### Access Token Claims（JWT RS256）

```json
{
  "sub": "user-uuid",
  "tid": "tenant-uuid",
  "role": "owner|admin|member",
  "global_role": "global_admin|''",
  "jti": "token-id-prefix",
  "ava": "avatar-url",
  "ghl": "github-login",
  "exp": 1718000000,
  "iat": 1718000000
}
```

#### Refresh Token 生命周期

```
创建：GitHubCallback / Register → TokenStore.Create(userID, tenantID, rawToken, TTL)
      ├─ SHA-256(rawToken) → token_hash
      └─ INSERT refresh_tokens (token_hash, user_id, tenant_id, expires_at)

续期：POST /auth/refresh
      ├─ Cookie 读取 rawRT
      ├─ TokenStore.IsBlacklisted(rawRT) → Redis GET rt:blacklist:<hash>
      ├─ TokenStore.GetActiveClaims(rawRT) → 从 DB 验证未过期未撤销
      ├─ TokenStore.Rotate(oldRT, newRT, TTL) → UPDATE token_hash, 原子替换
      └─ 签发新 Access Token（从 DB 重读 global_role + tenant_role）

撤销：POST /auth/logout
      ├─ TokenStore.Revoke(rawRT)
      │   ├─ UPDATE refresh_tokens SET revoked_at = NOW()
      │   └─ Redis SET rt:blacklist:<hash> "1" EX <remaining-ttl>
      └─ Clear Cookie
```

#### 全局管理员识别

配置项 `GLOBAL_ADMIN_GITHUB_LOGIN` 指定全局管理员 GitHub 用户名。识别逻辑：

1. OAuth Callback 时比对 `ghUser.Login`（不区分大小写）
2. 匹配则标记 `global_role = "global_admin"` 并同步写入 DB
3. DB 已有的 `global_role` 在后续 Refresh / SwitchTenant 时从 DB 重读，保持最新

### 授权模型

#### 双维度角色体系

```
                    ┌─────────────────────┐
                    │    global_role       │
                    │  (users.global_role) │
                    │                     │
                    │  global_admin ──────── 可访问 /admin/* 全局管理
                    │  "" (普通用户) ─────── 仅访问所属租户资源
                    └─────────────────────┘

                    ┌─────────────────────┐
                    │    tenant role       │
                    │ (tenant_members.role)│
                    │                     │
                    │  owner  (3) ────────── 租户所有权，可删除租户
                    │  admin  (2) ────────── 可管理成员/设置/知识库
                    │  member (1) ────────── 可使用 Agent/Skill/对话
                    └─────────────────────┘
```

#### 路由权限矩阵

| 路由组 | 中间件链 | 最低权限 |
|--------|----------|----------|
| `/auth/*` | 无鉴权（公开） | 无 |
| `/admin/*` | `JWTMiddleware` → `RequireGlobalAdmin` | `global_admin` |
| `/tenant/*` | `JWTMiddleware` → `InjectTenantContext` → `RequireTenantRole("member")` | tenant `member` |
| 写操作（POST/PUT/DELETE agents, skills 等） | 上述 + `RequireActiveTenant` | tenant `member` + 租户 active |
| 租户设置 PATCH | 上述链 | tenant `admin`（由 handler 内部检查） |

#### 中间件执行顺序

```
请求 → gin.Recovery → ErrorHandler → TraceMiddleware → CORSMiddleware → MetricsMiddleware
                                                                              │
        ┌─────────────────────────────────────────────────────────────────────┘
        │
        ├─ /auth/* → AuthHandler（无需 JWT）
        │
        ├─ /admin/* → JWTMiddleware → RequireGlobalAdmin → AdminHandler
        │
        └─ /tenant/* → JWTMiddleware → InjectTenantContext → RequireActiveTenant
                            │                    │                    │
                            │                    │                    └─ 查 public.tenants.status
                            │                    └─ 将 auth.{tenant_id,sub,role} 注入 TenantContext
                            └─ 验证 Bearer Token → 设置 context keys
                                 RequireTenantRole("member") → 检查 rank[role] ≥ rank[minRole]
```

#### RequireTenantRole 层级比较

```go
rank := map[string]int{"member": 1, "admin": 2, "owner": 3}
// rank[currentRole] >= rank[minRole] → 通过
```

### 安全机制

#### 密钥管理

| 密钥类型 | 加密方式 | 存储 |
|----------|----------|------|
| JWT 签名密钥 | RSA 2048+ 私钥 PEM | 环境变量 `JWT_PRIVATE_KEY_PEM` |
| 租户 LLM API 密钥 | AES-256-GCM | DB `tenant_settings.llm_api_keys_encrypted` |
| Refresh Token | SHA-256 hash | DB `refresh_tokens.token_hash` |
| OAuth State | crypto/rand 32 字节 | httpOnly Cookie（5 分钟 TTL）|
| GitHub Client Secret | 明文 | 环境变量（禁止入 git）|

#### AES-256-GCM 密钥派生

```go
// pkg/crypto/aes.go
func DeriveAESKey(jwtPrivateKeyPEM string) [32]byte {
    return sha256.Sum256([]byte(jwtPrivateKeyPEM))  // SHA-256 → 32 字节 AES key
}

func Encrypt(key [32]byte, plaintext string) (string, error)  // → base64(nonce‖ciphertext‖tag)
func Decrypt(key [32]byte, encoded string) (string, error)    // ← base64 解码 → GCM Open
```

租户 LLM 密钥加解密流程：`DeriveAESKey(JWT_PRIVATE_KEY_PEM)` → AES key → `Encrypt/Decrypt`。AES key 仅在应用进程内存中存在，不持久化。

#### Cookie 安全策略

| 属性 | 开发环境 | 生产环境 |
|------|----------|----------|
| `HttpOnly` | `true` | `true` |
| `Secure` | `false` | `true` |
| `SameSite` | `Lax` | `None`（跨域 frontend:3002 ↔ backend:8080）|
| `Path` | `/` | `/` |

#### Token 撤销与黑名单

- **即时撤销**：Revoke 写 Redis `rt:blacklist:<hash>` + DB `revoked_at`
- **双重校验**：Refresh 时先查 Redis 黑名单（O(1)），再查 DB 状态
- **TTL 自动清理**：Redis key 过期时间 = token 剩余有效期，无需手动清理

#### CORS 策略

仅允许配置的 `FRONTEND_URL` 跨域访问，默认 `http://localhost:3002`（开发）。

#### 租户隔离保障

| 层面 | 隔离方式 |
|------|----------|
| 数据库 | `SET LOCAL search_path = tenant_<UUID>, public`（事务级） |
| API 路由 | JWT `tid` claim → `InjectTenantContext` → 所有查询限定当前 schema |
| 租户切换 | `SwitchTenant` 验证 `IsMember(userID, targetTenantID)` 后签发新 JWT |
| LLM 密钥 | 每租户独立加密存储，5 分钟缓存 TTL，互不可见 |
| 向量存储 | Milvus collection 按 `memory_<tenant_uuid_underscored>` 隔离 |

### 用户入驻流程

```
新 GitHub 用户首次登录
  │
  ├─[有默认租户] → AutoJoinDefaultTenant
  │   ├─ UPSERT users (github_id, login, avatar)
  │   ├─ SELECT id FROM tenants WHERE is_default = true
  │   ├─ INSERT tenant_members (user_id, tenant_id, role='member')
  │   │   └─ 若 login == GLOBAL_ADMIN_GITHUB_LOGIN → role='owner'
  │   └─ issueTokenPair → 登录完成
  │
  └─[无默认租户] → Onboarding 流程
      ├─ 签发 OnboardingToken（短期 JWT，含 github_id/login/avatar）
      ├─ 302 → 前端 /auth/callback?onboarding_token=OB
      └─ 前端调用 POST /auth/register {action:"create", tenant_name:"..."}
          ├─ VerifyOnboarding(token)
          ├─ CreateTenant(input) → UPSERT user + INSERT tenant + INSERT member(owner)
          ├─ ProvisionTenantSchema(tenantID) → 创建 tenant_<UUID> schema
          └─ issueTokenPair → 注册完成

已有用户登录
  │
  ├─ GetUserTenants(githubID) → 查所有租户
  ├─ 选择目标租户（优先非默认租户，fallback 到默认租户）
  ├─ issueTokenPair(userID, targetTenantID, tenantRole, globalRole)
  └─ 302 → 前端 /auth/callback?access_token=JWT
```

### 租户切换

```
POST /auth/switch-tenant {tenant_id: "target-uuid"}
  ├─ Verify current JWT
  ├─ IsMember(userID, targetTenantID) → 403 if not member
  ├─ GetGlobalRole(userID) → 从 DB 读最新全局角色
  ├─ GetTenantRole(userID, targetTenantID) → 从 DB 读目标租户角色
  ├─ issueTokenPair → 新 Access Token（scoped to target tenant）
  └─ Rotate Refresh Cookie
```

### 设计决策

| 决策 | 原因 |
|------|------|
| RS256 非对称签名 | 网关/微服务可用公钥验证，无需共享私钥 |
| Refresh Token 存 DB + Redis 双层 | DB 持久化审计，Redis 加速黑名单查询 |
| SHA-256 hash 存储 Refresh Token | 数据库泄露不暴露原始令牌 |
| AES key 从 JWT PEM 派生 | 减少密钥管理复杂度，单一密钥源 |
| 全局角色从 DB 重读（非仅 JWT） | 角色变更即时生效，无需等 token 过期 |
| 默认租户自动加入 | 降低新用户入驻摩擦，无需管理员手动分配 |
| Cookie SameSite=None（生产） | 支持前后端跨域部署架构 |
| OAuth state 存 httpOnly Cookie | 防 CSRF，5 分钟过期自动清理 |

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
