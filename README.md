# ClawHermes AI Go

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

| 组件 | 技术 | 用途 |
|------|------|------|
| 语言 | Go 1.22+ | 高性能后端 |
| API 网关 | Gin | HTTP 服务框架 |
| 事件总线 | NATS | 异步事件驱动 |
| 向量数据库 | Milvus | 向量存储与检索 |
| 图数据库 | Neo4j | 知识图谱存储 |
| 关系数据库 | PostgreSQL | 租户数据、Agent/Skill 持久化 |
| 缓存 | Redis | Token 存储、会话缓存 |
| 日志 | Uber Zap | 结构化日志 |
| 配置 | Spf13 Viper | 配置管理 |
| 可观测 | OpenTelemetry + Prometheus | 链路追踪与指标 |
| 部署 | Kubernetes/Helm | 云原生部署 |
| 前端 | React + Vite | Web 控制台 |

## 架构分层

```
┌────────────────────────────────────────────────────┐
│  Portal 接入层 (Gin HTTP API + JWT Auth)            │
│  GET/POST /skills /agents /memory /knowledge /mcp  │
│  /auth /admin /tenant                              │
├────────────────────────────────────────────────────┤
│  Auth & Multi-Tenancy                              │
│  GitHub OAuth → JWT(RS256) → Tenant Schema 隔离    │
├────────────────────────────────────────────────────┤
│  Harness 组件生命周期管理                            │
│  Register → Sequential Start → Reverse Stop        │
├────────────────────────────────────────────────────┤
│  Hermes 事件总线 (NATS)                             │
│  Publish / Subscribe  domain.action subject 命名   │
├────────────────────────────────────────────────────┤
│  Agent Core                                        │
│  ReAct / CoT / Planning / ToolCalling / RAG / Swarm│
│  ┌──────────────┐   ┌───────────────────────────┐  │
│  │ Agent Registry│   │ A2A Protocol              │  │
│  │ (PostgreSQL) │   │ Sequential/Parallel/       │  │
│  └──────────────┘   │ Hierarchical/Pipeline/Swarm│  │
│                     └───────────────────────────┘  │
├────────────────────────────────────────────────────┤
│  Skill Gateway (原子化执行引擎)                      │
│  Provider Registry → Circuit Breaker → Retry       │
│  → Atomic Engine → Pipeline DSL → Audit Log        │
├────────────────────────────────────────────────────┤
│  三层记忆系统                                        │
│  短期(Buffer/Window/Summary) + 长期(Vector) + Entity│
├────────────────────────────────────────────────────┤
│  GraphRAG 知识引擎 (Neo4j + Milvus)                 │
│  文档解析 → 文本分块 → Embedding → 向量检索 + 图查询  │
├────────────────────────────────────────────────────┤
│  LLM Gateway                                       │
│  OpenAI / Anthropic / Ollama  统一 Complete/Embed  │
├────────────────────────────────────────────────────┤
│  MCP (Model Context Protocol)                      │
│  Client Manager → Skill Adapter → Cache            │
├────────────────────────────────────────────────────┤
│  可观测性 (OpenTelemetry + Prometheus + Zap)        │
│  Trace / Metrics / Structured Logging              │
└────────────────────────────────────────────────────┘
```

## 快速启动

### 前置要求

- Go 1.22+
- Docker & Docker Compose
- Kubernetes (kubectl) — 用于云原生部署
- Helm — 用于包管理

### 1. 克隆项目

```bash
git clone https://github.com/byteBuilderX/ClawHermes-AI-Go.git
cd ClawHermes-AI-Go
```

### 2. 配置环境

```bash
cp .env.example .env
# 编辑 .env，填写 API Key 和各服务地址
```

### 3. 本地开发启动

```bash
./start.sh
```

或手动启动：

```bash
make build
make run
```

### 4. 云原生部署 (Kubernetes)

```bash
# 构建 Docker 镜像
make docker-build

# 部署依赖服务（NATS、Milvus、Neo4j、PostgreSQL、Redis）
kubectl apply -f k8s/dependencies.yaml

# 等待依赖就绪
kubectl wait --for=condition=ready pod -l app=nats --timeout=120s
kubectl wait --for=condition=ready pod -l app=milvus --timeout=120s

# 部署主应用
kubectl apply -f k8s/deployment.yaml
```

#### 使用 Helm

```bash
make docker-build
make helm-install
```

### 5. 验证健康状态

```bash
curl http://localhost:8080/health
# {"status":"ok","service":"ClawHermes AI Go"}
```

## API 端点

### 认证 (Auth)

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/auth/github` | 发起 GitHub OAuth 登录 |
| GET | `/auth/github/callback` | GitHub OAuth 回调 |
| POST | `/auth/register` | 通过邀请 Token 完成注册 |
| POST | `/auth/refresh` | 刷新 JWT |
| POST | `/auth/logout` | 退出登录 |
| GET | `/auth/me` | 获取当前用户信息 |
| POST | `/auth/switch-tenant` | 切换当前租户 |
| POST | `/auth/create-tenant` | 创建新租户 |
| GET | `/tenant/list` | 列出当前用户所属租户 |

> 认证路由仅在配置了 `GITHUB_CLIENT_ID` 时启用。

### 管理员 (Admin)，需 `global_admin` 角色

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/admin/tenants` | 列出所有租户 |
| POST | `/admin/tenants` | 创建租户 |
| GET | `/admin/tenants/:id` | 获取租户详情 |
| PATCH | `/admin/tenants/:id` | 更新租户信息 |
| DELETE | `/admin/tenants/:id` | 删除租户 |

### 租户管理 (Tenant)，需 `member` 角色

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/tenant/members` | 列出成员（分页） |
| POST | `/tenant/members/invite` | 邀请成员（需 admin/owner） |
| PATCH | `/tenant/members/:user_id/role` | 更新成员角色 |
| DELETE | `/tenant/members/:user_id` | 移除成员 |
| GET | `/tenant/settings` | 获取租户设置 |
| PATCH | `/tenant/settings` | 更新租户设置 |

### Skill

写操作需要租户处于 `active` 状态。

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/skills` | 列出所有 Skill |
| POST | `/skills` | 创建 Skill |
| GET | `/skills/:id` | 获取 Skill 详情 |
| PUT | `/skills/:id` | 更新 Skill |
| DELETE | `/skills/:id` | 删除 Skill |

### Agent

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/agents` | 列出所有 Agent |
| POST | `/agents` | 创建 Agent（租户 active 时） |
| GET | `/agents/:id` | 获取 Agent 详情 |
| POST | `/agents/:id/execute` | 执行 Agent（租户 active 时） |
| GET | `/agents/executions` | 列出执行历史 |
| DELETE | `/agents/:id` | 删除 Agent |

### 知识库 (Knowledge / RAG)

需要 JWT + 租户上下文，写操作需 `admin` 角色且租户 `active`。

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/knowledge/workspaces` | 列出知识库工作区 |
| GET | `/knowledge/workspaces/:name/stats` | 获取工作区统计 |
| POST | `/knowledge/workspaces` | 创建工作区（admin） |
| PATCH | `/knowledge/workspaces/:name` | 更新工作区（admin） |
| DELETE | `/knowledge/workspaces/:name` | 删除工作区（admin） |
| POST | `/knowledge/ingest` | 上传并摄入文档（admin） |
| POST | `/knowledge/query` | RAG 查询 |

### 记忆 (Memory)

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/memory/sessions` | 创建记忆会话 |
| POST | `/memory` | 添加记忆条目 |
| GET | `/memory/:id` | 获取记忆条目 |
| POST | `/memory/search` | 语义搜索记忆 |
| DELETE | `/memory/:id` | 删除记忆条目 |
| GET | `/memory/stats` | 获取记忆统计 |
| DELETE | `/memory/session/:session_id` | 清空会话记忆 |
| GET | `/memory/entities` | 获取实体记忆 |
| POST | `/memory/extract-entities` | 提取实体 |
| GET | `/memory/summary/:session_id` | 获取会话摘要 |

### MCP

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/mcp/servers` | 列出所有 MCP 服务器 |
| GET | `/api/v1/mcp/servers/:id` | 获取服务器详情 |
| GET | `/api/v1/mcp/servers/:id/tools` | 列出服务器工具 |
| GET | `/api/v1/mcp/servers/:id/resources` | 列出服务器资源 |
| POST | `/api/v1/mcp/servers` | 连接 MCP 服务器（需 active） |
| DELETE | `/api/v1/mcp/servers/:id` | 断开服务器（需 active） |
| POST | `/api/v1/mcp/tools/:toolId/execute` | 执行工具（需 active） |
| GET | `/api/v1/mcp/skills` | 列出 MCP Skills |
| GET | `/api/v1/mcp/skills/:id` | 获取 Skill 详情 |
| POST | `/api/v1/mcp/skills/refresh` | 刷新 Skills（需 active） |
| GET | `/api/v1/mcp/status` | 服务器连接状态统计 |

MCP 服务器配置持久化至 PostgreSQL，服务启动时自动恢复连接。

### 其他

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/health` | 健康检查 |
| GET | `/metrics` | Prometheus 指标 |

## 核心概念

### Skill Gateway

Skill 是 ClawHermes 的原子化能力单元，支持三种类型：

- **Registry Skill**: 通过 `orchestrator.Registry` 注册的内置 Skill
- **Code Skill**: 代码执行能力（Python、JavaScript 等）
- **LLM Skill**: 大模型调用能力（OpenAI、Claude、Ollama）
- **MCP Skill**: 通过 MCP 协议接入的外部工具

SkillGateway 执行链：

```
SkillRequest
  → ProviderRegistry.Resolve(skillID)
  → CircuitBreaker.Allow(skillID)   // 熔断保护
  → AtomicEngine.execute()          // 带超时 + 指数退避重试
  → Auditor.log()                   // 审计日志
  → PipelineEngine.execute()        // 多步骤编排
```

**Pipeline DSL 示例：**

```go
pipeline := skillgateway.NewPipelineBuilder("my-pipeline").
    Step("fetch", "fetch-skill", input).
    If("need-translate", func(ctx StepContext) bool {
        return ctx["$steps.fetch.output"].(string) != ""
    }).
    Then(NewPipelineBuilder("").Step("translate", "translate-skill", nil)).
    EndIf().
    Parallel("enrich",
        NewPipelineBuilder("").Step("tag", "tag-skill", nil),
        NewPipelineBuilder("").Step("classify", "classify-skill", nil),
    ).
    Build()
```

### Agent 系统

```go
config := &agent.AgentConfig{
    ID:            "agent-001",
    Name:          "My Agent",
    Type:          agent.ReActAgent,  // react/cot/planning/tool_calling/rag/swarm
    SystemPrompt:  "You are a helpful assistant.",
    MaxIterations: 10,
}
a := agent.NewBaseAgent(config, logger)
registry.Register(ctx, a)

result, err := a.Execute(ctx, "用户输入",
    agent.WithMaxSteps(10),
    agent.WithMemory(true),
    agent.WithTemperature(0.7),
)
```

### A2A 多智能体协作

`internal/agent/a2a/` 实现 Agent-to-Agent 协议：

- **Discovery**: 能力注册与发现
- **Negotiation**: 协作条件协商
- **Orchestrator**: 创建执行计划，支持 5 种策略：`sequential` / `parallel` / `hierarchical` / `pipeline` / `swarm`
- **Messages**: 通过 Inbox/Outbox 异步处理

```go
orch := a2a.NewOrchestrator(logger)
plan, _ := orch.CreatePlan(ctx, collaborationID, "分析任务",
    a2a.StrategyParallel, participants)
```

### 三层记忆系统

```
短期记忆 (ShortTerm)
├── ConversationBufferMemory   — 无限缓冲
├── ConversationWindowMemory   — 滑动窗口（ShortTermWindowSize 条）
└── ConversationSummaryMemory  — 自动摘要压缩

长期记忆 (LongTerm)  → Milvus 向量检索，支持语义搜索 + 混合检索
实体记忆  (Entity)   → 提取并持久化命名实体关系
```

```go
memManager := memory.NewMemoryManager(config, logger, vectorMemory, entityMemory, persistence, pool)
// Agent 自动注入：agent.SetMemoryManager(memManager)
```

### LLM Gateway

```go
gateway := llmgateway.NewGateway()
gateway.RegisterClient(llmgateway.ProviderQwen, qwenClient)
gateway.RegisterClient(llmgateway.ProviderZhipu, zhipuClient)
gateway.RegisterEmbeddingClient(llmgateway.ProviderQwen, embedClient)
gateway.SetDefault(llmgateway.ProviderQwen)

resp, err := gateway.Complete(ctx, &llmgateway.CompletionRequest{
    Model: "qwen-plus",
    Messages: []llmgateway.Message{{Role: "user", Content: "Hello"}},
})
```

**支持的提供商：** 通义千问 (Qwen) · 智谱 AI (Zhipu)

### GraphRAG 知识引擎

```
文档上传 → Parser 解析 → Chunker 分块
→ EmbeddingService 向量化
→ VectorStore (Milvus) 存储
→ GraphRAG (Neo4j) 知识图谱关联
→ RAGService.Query() 混合检索召回
```

### 多租户隔离

- 每个租户在 PostgreSQL 中拥有独立 schema：`tenant_<tenant_id>`（UUID，含连字符，schema 名双引号转义）
- JWT Claims 携带 `tenant_id` + `role`，中间件自动设置 `search_path`
- 角色体系：`global_admin` > `owner` > `admin` > `member`
- 租户状态：`active` 正常 / `suspended` 禁用 — suspended 状态下所有写操作和 Agent 执行被 `RequireActiveTenant` 中间件拦截（HTTP 403），读接口不受影响
- 用户可属于多个租户，通过 `POST /auth/switch-tenant` 切换

### Harness 生命周期

```go
harness := harnesspkg.New(logger)
harness.Register(hermesComponent)
harness.Register(otherComponent)
harness.Start(ctx)   // 顺序启动
defer harness.Stop(ctx) // 逆序停止
```

## 可观测性

### Prometheus 指标

```
http_requests_total{method, path, status}
http_request_duration_seconds{method, path}
skill_executions_total{skill_id, status}
skill_circuit_breaker_state{skill_id}       // 0=closed 1=open 2=half_open
agent_executions_total{agent_id, type, status}
llm_requests_total{model, provider, status}
llm_request_duration_seconds{model, provider}
llm_token_usage_total{model, token_type}
knowledge_queries_total{type, status}
hermes_events_total{type, status}
```

- Prometheus: `http://localhost:9090`
- Grafana: `http://localhost:3000` (admin/admin)
- Jaeger: `http://localhost:16686`
- Metrics 端点: `GET /metrics`

### OpenTelemetry 追踪

每个 Handler 通过 `middleware/trace.go` 自动创建 Span；内部关键操作手动创建子 Span，命名格式：`{component}.{operation}`。

## 环境配置

```env
# 服务
PORT=8080

# PostgreSQL
POSTGRES_URL=postgres://user:password@localhost:5432/clawhermes?sslmode=disable

# Redis
REDIS_URL=redis://localhost:6379

# NATS
NATS_URL=nats://localhost:4222

# Milvus
MILVUS_HOST=localhost
MILVUS_PORT=19530

# Neo4j
NEO4J_URI=bolt://localhost:7687
NEO4J_USER=neo4j
NEO4J_PASSWORD=password

# GitHub OAuth + JWT
GITHUB_CLIENT_ID=your-client-id
GITHUB_CLIENT_SECRET=your-client-secret
JWT_PRIVATE_KEY_PEM=-----BEGIN RSA PRIVATE KEY-----...
GLOBAL_ADMIN_GITHUB_LOGIN=your-github-login

# LLM（通义千问 / 智谱 AI）
QWEN_API_KEY=sk-...
ZHIPU_API_KEY=...

# OpenTelemetry
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
```

## 常用命令

```bash
make build           # 编译
make run             # 运行
make test            # 单元测试
make test-coverage   # 测试覆盖率
make fmt             # 代码格式化
make vet             # 静态检查
make lint            # Lint 检查
make docker-build    # 构建 Docker 镜像
make k8s-deploy      # 部署到 Kubernetes
make k8s-delete      # 从 Kubernetes 删除
make helm-install    # Helm 安装
make helm-uninstall  # Helm 卸载
make clean           # 清理构建产物
```

## 开发指南

### 添加新 Skill

1. 在 `internal/skill/` 中实现 `Skill` 接口（继承 `BaseSkill`）
2. 如需执行，实现 `SkillExecutor` 接口的 `Execute(input interface{}) (interface{}, error)`
3. 通过 `orchestrator.Registry.Register(ctx, id, skill)` 注册
4. （可选）通过 `skillgateway.ProviderRegistry` 注册为 Provider 接入熔断/流水线

### 运行测试

```bash
go test -v -race -timeout 30s ./...
go test -v ./internal/skill/...    # 指定包
make test-coverage                  # 生成覆盖率报告
```

### 代码规范

- Zap 结构化日志，禁止 `fmt.Print`
- 错误用 `fmt.Errorf("operation: %w", err)` 包装
- 每次变更后运行 `go vet && go test -short ./...`
- 行长 ≤ 120 字符；import 顺序：stdlib → 第三方 → internal

## 商业化能力

- ✅ 多租户隔离（PostgreSQL schema 级，UUID schema 名安全转义）
- ✅ GitHub OAuth + JWT RS256 认证，3 天免登录
- ✅ 租户禁用强制拦截（suspended 状态写操作 403）
- ✅ 多租户切换（用户可属于多个租户）
- ✅ 成员角色管理（owner/admin/member + 邀请流程）
- ✅ Skill 熔断器 + 流水线编排
- ✅ A2A 多智能体协作（5 种策略）
- ✅ 三层记忆系统
- ✅ GraphRAG 知识增强（工作区 CRUD + 文档摄入）
- ✅ MCP 服务器管理（持久化 + 自动恢复连接）
- ✅ Agent 执行历史记录与查询
- ✅ 通义千问 / 智谱 AI LLM 接入
- ✅ Web 控制台（React）：Dashboard · Agents · Skills · 知识库 · 记忆 · MCP · 成员管理 · 租户设置
- ✅ 私有化部署 / 云原生 Kubernetes
- ✅ 全链路可观测（OTel + Prometheus + Grafana）
- 🔄 Skill 插件市场
- 🔄 AI 成本治理
- 🔄 灰度发布

## 许可证

Apache License 2.0 — 详见 [LICENSE](LICENSE)

## 贡献指南

欢迎提交 Issue 和 Pull Request！详见 [CONTRIBUTING.md](CONTRIBUTING.md)

## 联系方式

- 📧 Email: [18348792873@163.com](18348792873@163.com)
- 🐙 GitHub: [byteBuilderX/ClawHermes-AI-Go](https://github.com/byteBuilderX/ClawHermes-AI-Go)
