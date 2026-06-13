# Project Facts Reference

## Directory Structure

```
cmd/server/main.go          - 唯一入口：初始化 Harness，注册所有组件
api/
  router.go                 - 路由注册（Gin），所有端点在此集中定义
  handler/                  - 每域一个 handler 文件
  middleware/               - ErrorHandler, MetricsMiddleware, RequireGlobalAdmin,
                              RequireTenantRole, TraceMiddleware
  model/                    - Request/Response DTO，无业务逻辑
internal/
  config/                   - Viper 配置加载，InitializeServices 连接外部依赖
  harness/                  - 应用生命周期：Component 注册 → 顺序启动 → 逆序停止
  hermes/                   - NATS 客户端封装，Publish/Subscribe
  agent/                    - Agent 框架（BaseAgent, Registry 持久化至 PostgreSQL）
  agent/a2a/                - A2A 协议（Discovery, Negotiation, Orchestrator, Protocol）
  memory/                   - 三层记忆：短期(Buffer/Window/Summary)、长期(Vector)、实体
  skill/                    - Skill 定义（BaseSkill, CodeSkill, LLMSkill）和 Executor
  skillgateway/             - Skill 执行引擎（AtomicEngine、PipelineEngine、CircuitBreaker、Audit）
    providers/              - SkillProvider 适配器（LLM、Code、MCP、RegistryAdapter）
  orchestrator/             - Skill 注册表，持久化至 PostgreSQL tenant schema
  knowledge/                - GraphRAG（Neo4j + Milvus），KnowledgeIngest，RAGService
  llmgateway/               - LLM 统一网关（OpenAI/Anthropic/Ollama），EmbeddingClient
  mcp/                      - MCP 客户端管理、SkillAdapter、Cache，类型定义
  embedding/                - EmbeddingService，调用 LLMGateway 生成向量
  document/                 - 文档解析器（Parser）
  textchunk/                - 文本分块（Chunker）
  auth/                     - GitHub OAuth、JWT(RS256)、Middleware、TokenStore、Onboard
  migration/                - PostgreSQL public schema 迁移
pkg/
  observability/            - Logger(Zap), Tracer(OTEL), PrometheusMetrics
  postgres/                 - pgxpool 封装
  redis/                    - go-redis 封装
  tenantdb/                 - 租户 context、schema 路由、ExecTenant 辅助函数
  vector/                   - VectorStore（Milvus SDK v2.4.2）
web/                        - React + Vite 前端控制台
k8s/                        - Kubernetes manifests（含 monitoring、network-policy、ingress）
helm/                       - Helm Chart（templates: deployment, service, frontend）
grafana/                    - Grafana 数据源 + 仪表板配置
```

## Dependency Versions & SDK Usage

| 依赖 | 版本 | 关键说明 |
|------|------|---------|
| Go | 1.22+ | 泛型，slog 兼容 |
| Gin | v1.9+ | 路由组 `r.Group`，middleware 在 router.go 注册 |
| NATS | v1.31+ | JetStream 模式，subject 格式 `domain.action` |
| Milvus SDK | v2.4.2 | `client.Search` 参数顺序见 `pkg/vector/vector_store.go` |
| Neo4j Driver | v5.x | `session.Run` 返回 `(Result, error)`，用 `result.Collect(ctx)` |
| pgx | v5.x | pgxpool，事务内用 `SET LOCAL search_path` 切换租户 |
| go-redis | v9.x | `redis.NewClient`，context-aware API |
| Zap | v1.26+ | 生产用 `NewProduction()`，开发用 `NewDevelopment()` |
| OTEL | v1.21+ | TracerProvider 在 main 初始化，通过 context 传播 |
| Viper | v1.18+ | 支持 `.env` + 环境变量，优先级：env > file > default |
| JWT | golang-jwt/jwt v5 | RS256 签名，Claims 含 tenant_id / role / global_role |

## Error Handling Patterns

1. **API 层**：统一 `model.ErrorResponse{Code, Message}`，语义正确的 HTTP 状态码
2. **internal 层**：返回 `error`，用 `fmt.Errorf("op: %w", err)` 包装，不吞错误
3. **外部连接**：`config.InitializeServices` 失败时 Warn 而不阻塞启动；Milvus/Neo4j 连接 3s 超时
4. **Context**：所有跨组件调用传递 `context.Context`，支持超时与取消

## Concurrency Safety Rules

- Registry/Manager 类型用 `sync.RWMutex`（读多写少）
- 单个 Agent 执行用 `sync.Mutex`（不支持同一 Agent 并发执行）
- CircuitBreaker 每个 skill 独立 `sync.Mutex`
- Goroutine 间通信用 channel，不共享内存

## Testing Conventions

- 单元测试 `*_test.go` 与源码同目录
- 集成测试需 Docker 服务，标记 `//go:build integration`
- 运行：`make test`（单元），`make test-integration`（集成）
- 目标覆盖率 ≥ 80% 业务逻辑代码

## Build & Deploy

- 编译产物：`bin/stratum`
- Docker 镜像：`Dockerfile` 多阶段构建，非 root 用户运行
- 本地开发：`./start.sh`（启动 docker-compose + 应用）
- K8s 部署：`helm install` 使用 `helm/` chart
- 数据库迁移：启动时自动运行 `internal/migration/sql/` 下的 SQL 文件
