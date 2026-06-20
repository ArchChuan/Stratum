# Project Facts Reference

## Directory Structure

```
cmd/server/main.go          - 唯一入口：初始化 Harness，注册所有组件（≤30 行）
api/
  http/
    router.go               - 路由注册（Gin），按域拆分为 registerXxx 私有函数
    handler/                - 每域一个文件，只做请求解析 + 响应组装
    dto/                    - Request/Response 结构体，无业务逻辑
  middleware/               - ErrorHandler · CORSMiddleware · TraceMiddleware ·
                              MetricsMiddleware · JWTMiddleware · InjectTenantContext ·
                              RequireTenantRole · RequireGlobalAdmin · RequireActiveTenant
  wiring/                   - 组合根：构造 application + infrastructure，装配 Container
internal/
  agent/{domain,application,infrastructure}
                            - Agent 框架：ReAct 循环、Registry、ChatStore、A2A 协议
  iam/{domain,application,infrastructure}
                            - 多租户 IAM：Tenant / Admin / JWT / OAuth / OnBoard
  knowledge/{domain,application,infrastructure}
                            - GraphRAG：WorkspaceService、RAGService、文档摄取
  llmgateway/{domain,application,infrastructure}
                            - LLM 统一网关（OpenAI/Anthropic/Ollama）、ModelService
  mcp/{domain,application,infrastructure}
                            - MCP 服务器管理、SkillAdapter、MCPTools 接口
  memory/{domain,application,infrastructure}
                            - 记忆持久化 + JetStream 三阶段 pipeline
  platform/{config,domain,harness}
                            - Viper 配置、Harness 生命周期（顺序启动→逆序停止）
  skill/{domain,application,infrastructure}
                            - Skill CRUD + AtomicEngine + PipelineEngine + CircuitBreaker
pkg/
  constants/               - 跨包共享业务/配置常量（agent · auth · memory · pagination · timeouts）
  observability/           - Logger(Zap) · Tracer(OTEL) · PrometheusMetrics
  reqctx/                  - 请求级 context 键（user_id / tenant_id / trace_id）
  storage/{milvus,postgres,redis}
                           - 各存储驱动封装（pgxpool · go-redis · Milvus SDK）
  tenantdb/                - 租户 context、schema 路由、ExecTenant 辅助函数
  vector/                  - VectorStore（Milvus SDK v2.4.2），pkg/storage/milvus 升级中
  migration/               - PostgreSQL public schema 迁移（golang-migrate）
  httpclient/              - 带重试/超时的 HTTP 客户端封装
  textchunk/               - 文本分块（Chunker）
  crypto/                  - AES 加密工具
web/                        - React 18 + Vite 4 前端控制台（src/modules 按域组织）
k8s/                        - Kubernetes manifests（含 monitoring · network-policy · ingress）
helm/                       - Helm Chart（templates: deployment · service · frontend）
grafana/                    - Grafana 数据源 + 仪表板配置
```

## Dependency Versions & SDK Usage

| 依赖 | 版本 | 关键说明 |
|------|------|---------|
| Go | 1.22+ | 泛型，slog 兼容 |
| Gin | v1.9+ | 路由组 `r.Group`，middleware 在 router.go 注册 |
| NATS | v1.31+ | JetStream 模式；memory pipeline 用 `nats.go/jetstream` 包直接操作 |
| Milvus SDK | v2.4.2 | `client.Search` 参数顺序见 `pkg/vector/vector_store.go` |
| pgx | v5.x | pgxpool，事务内用 `SET LOCAL search_path` 切换租户 |
| go-redis | v9.x | `redis.NewClient`，context-aware API |
| Zap | v1.26+ | 生产用 `NewProduction()`，开发用 `NewDevelopment()` |
| OTEL | v1.21+ | TracerProvider 在 main 初始化，通过 context 传播 |
| Viper | v1.18+ | 支持 `.env` + 环境变量，优先级：env > file > default |
| JWT | golang-jwt/jwt v5 | RS256 签名，Claims 含 tenant_id / role / global_role |

## Error Handling Patterns

1. **API 层**：`c.Error(err)` 交给 `ErrorHandler` middleware 统一映射；domain sentinel（`ErrNotFound`、`ErrNameConflict`）→ 对应 HTTP 状态码
2. **internal 层**：返回 `error`，用 `fmt.Errorf("op: %w", err)` 包装，不吞错误
3. **外部连接**：`platform.harness` 中连接失败 Warn 而不阻塞启动；Milvus/NATS 连接有超时
4. **Context**：所有跨组件调用传递 `context.Context`，支持超时与取消

## Concurrency Safety Rules

- Registry/Manager 类型用 `sync.RWMutex`（读多写少）
- 单个 Agent 执行用 `sync.Mutex`（不支持同一 Agent 并发执行）
- CircuitBreaker 每个 skill 独立 `sync.Mutex`
- Goroutine 间通信用 channel，不共享内存

## Testing Conventions

- 单元测试 `*_test.go` 与源码同目录
- 集成测试需 Docker 服务，标记 `//go:build integration`
- 运行：`go vet && go test -short ./...`（单元），`go test -v -race -timeout 30s ./...`（完整）
- API 契约测试：`api/http/contract_test.go` + `testdata/contracts/*.golden.json`
- 目标覆盖率 ≥ 80% 业务逻辑代码

## Build & Deploy

- 编译产物：`bin/stratum`（`make build`）
- Docker：`make docker-up` / `make docker-down`（启停 docker-compose 所有依赖服务）
- 本地开发：`make run`（go run cmd/server/main.go）
- K8s 部署：`helm install` 使用 `helm/` chart
- 数据库迁移：启动时自动运行 `pkg/migration/sql/` 下的 SQL 文件
