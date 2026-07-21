# Project Facts Reference

## Directory Structure

```
cmd/server/main.go          - 唯一入口：加载配置，BuildContainer，租户 bootstrap，启动 HTTP runtime
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
                            - LLM 统一网关（Qwen/Zhipu OpenAI-compatible）、ModelService
  evaluation/{domain,application,infrastructure}
                            - 通用评估控制面：suite/revision、异步 run/job、优化候选、实验与反馈
  workflow/{domain,application,infrastructure}
                            - 持久化静态 DAG：版本发布、异步运行、审批与人工介入
  mcp/{domain,application,infrastructure}
                            - MCP 服务器管理、ToolRegistry、工具级风险策略与审批执行
  memory/{domain,application,infrastructure}
                            - 记忆持久化 + JetStream 三阶段 pipeline
  platform/{domain,harness,runtime}
                            - 平台生命周期辅助与 HTTP/租户启动期编排
  skill/{domain,application,infrastructure}
                            - Skill instruction bundle CRUD、revision 发布与候选优化
pkg/
  constants/               - 跨包共享业务/配置常量（agent · auth · memory · pagination · timeouts）
  observability/           - Logger(Zap) · Tracer(OTEL) · PrometheusMetrics
  reqctx/                  - 请求级 context 键（user_id / tenant_id / trace_id）
  storage/{milvus,postgres,redis}
                           - 各存储驱动封装（pgxpool · go-redis · Milvus SDK）
  tenantdb/                - 租户 context、schema 路由、ExecTenant 辅助函数
  vector/                  - `pkg/storage/milvus` 的兼容 re-export；新代码禁止继续引用
  migration/               - PostgreSQL public schema 迁移（golang-migrate）
  httpclient/              - 带重试/超时的 HTTP 客户端封装
  textchunk/               - 文本分块（Chunker）
  crypto/                  - AES 加密工具
web/                        - React 18 + Vite 6 前端控制台（src/modules 按域组织）
k8s/                        - Kubernetes manifests（含 monitoring · network-policy · ingress）
helm/                       - Helm Chart（templates: deployment · service · frontend）
grafana/                    - Grafana 数据源 + 仪表板配置
```

## Dependency Versions & SDK Usage

| 依赖 | 版本 | 关键说明 |
|------|------|---------|
| Go | 1.25.12 | 以 `go.mod` toolchain directive 为准 |
| Gin | v1.9+ | 路由组 `r.Group`，middleware 在 router.go 注册 |
| NATS | v1.51 | JetStream 模式；memory pipeline 用 `nats.go/jetstream` 包直接操作 |
| Milvus SDK | v2.4.2 | 主实现位于 `pkg/storage/milvus`；`pkg/vector` 仅兼容旧 import |
| pgx | v5.9.2 | pgxpool，事务内用 `SET LOCAL search_path` 切换租户 |
| go-redis | v9.x | `redis.NewClient`，context-aware API |
| Zap | v1.26+ | 生产用 `NewProduction()`，开发用 `NewDevelopment()` |
| OTEL | v1.40 | TracerProvider 由 platform runtime 初始化，通过 context 传播 |
| 前端 | React 18.3 · Vite 6.4 · AntD 5.20 | 版本以 `web/package.json` 与 lockfile 为准 |
| JWT | golang-jwt/jwt v5 | RS256 签名，Claims 含 tenant_id / role / global_role |

## Error Handling Patterns

1. **API 层**：`c.Error(err)` 交给 `ErrorHandler` middleware 统一映射；domain sentinel（`ErrNotFound`、`ErrNameConflict`）→ 对应 HTTP 状态码
2. **internal 层**：返回 `error`，用 `fmt.Errorf("op: %w", err)` 包装，不吞错误
3. **外部连接**：`api/wiring` 负责构造依赖；可降级组件记录 Warn，必要组件构造失败则 `BuildContainer` 返回错误并逆序清理
4. **Context**：所有跨组件调用传递 `context.Context`，支持超时与取消

## Concurrency Safety Rules

- Registry/Manager 类型用 `sync.RWMutex`（读多写少）
- 单个 Agent 执行用 `sync.Mutex`（不支持同一 Agent 并发执行）
- LLM provider 的 CircuitBreaker 各自维护状态锁；Skill 当前不是可直接执行的 gateway
- Goroutine 间通信用 channel，不共享内存

## Testing Conventions

- 单元测试 `*_test.go` 与源码同目录
- 集成测试需 Docker 服务，标记 `//go:build integration`
- 运行：`go vet && go test -short ./...`（单元），`go test -v -race -timeout 30s ./...`（完整）
- API 契约测试：`api/http/contract_test.go` + `api/http/testdata/contracts/*.golden.json`
- 目标覆盖率 ≥ 80% 业务逻辑代码

## Build & Deploy

- 编译产物：`bin/server`（`make be-build`）
- Docker 依赖：`make infra-up` / `make infra-down`；可观测性：`make obs-up` / `make obs-down`
- 本地开发：`make run`（go run cmd/server/main.go）
- K8s 部署：`helm install` 使用 `helm/` chart
- 数据库迁移：启动时自动运行 `pkg/migration/sql/` 下的 SQL 文件
