# 架构分层规则（DDD bounded context）

> 后端架构的权威约束。任何跨层、跨 context 的改动前必读。

## 目录骨架

```
api/{http/{handler,dto,middleware},wiring}
internal/<ctx>/{domain/{,port/},application,infrastructure}
pkg/storage/{postgres,redis,milvus,tenantnaming}
pkg/{httpclient,observability,crypto,constants,migration,textchunk,tenantdb,vector}
```

- 8 个 bounded context：`agent · memory · knowledge · skill · mcp · iam · llmgateway · platform`
- 跨域路由层（如 `capgateway`）作为 ACL，必要时下沉进消费上下文

## 依赖方向

- `handler → application → domain/port`
- `infrastructure` 实现 port，由 `api/wiring/Container` 集中装配；Shutdown 逆序释放
- 跨 context 调用走「消费者侧」port（接口放消费方 `domain/port/`），禁止 import 兄弟上下文的 `application` / `infrastructure`

## 单向底线（CI 用 go-arch-lint + depguard 固化）

- `pkg/` 不 import `internal/`
- `domain/` 零第三方依赖（仅 stdlib + `pkg/constants`）
- `application/` 不 import `pgx` / `redis` / `nats` / `gin`
- `handler` 不 import `internal/*/infrastructure` 与 `pgx` / `redis` / `milvus`

## 各层职责速查

| 层 | 该做的事 | 不能做 |
|----|---------|--------|
| `dto/` | 结构 + binding tag | 业务规则、白名单、计算 |
| `handler/` | bind → 取 tenant → call service → render（≤15 行/方法），错误用 `c.Error(err)` 交给 ErrorHandler | import `pgx*`/`redis*`/`milvus*`/`internal/*/infrastructure`；散写白名单/SQL/编排 |
| `middleware/` | 横切：auth · trace · metrics · `domain.Err* → HTTP` 映射 | 任何业务编排 |
| `wiring/` | 组合根：构造 application + infrastructure，塞 Container，反向 Shutdown；跨 ctx ACL（≤30 行 thin adapter） | 处理 HTTP/业务规则 |
| `application/` | 用例编排 · 事务/Saga 边界 · DTO↔聚合 · 鉴权检查 · 发领域事件 | SQL、HTTP、序列化、业务不变量校验（让 domain 自检） |
| `domain/` | 实体、值对象、聚合根、不变量、领域算法（评分/状态机/切块策略）；`domain/port/` 出向接口契约 | 任何第三方依赖（`pkg/constants` 除外）；存在「贫血结构体」（无方法纯字段） |
| `infrastructure/<adapter>/` | 唯一职责：实现 `domain/port/` 接口；DB/MQ/HTTP IO；错误翻译（`pgconn.PgError → domain.ErrXxx`） | 业务规则、跨 ctx import 兄弟 `domain`/`infrastructure`、port 接口定义 |

## 消费者侧接口与运行时解析

- 跨 ctx：消费方在自己 `domain/port/` 定接口 → provider 在自己 `infrastructure/` 实现 → `api/wiring/` thin adapter 转接
- 跨租户：通过 `Resolver` 函数类型请求时延迟解析，例 `type EmbedServiceResolver func(ctx, tenantID) EmbedClient`，由 wiring 注入
- 接口最小化：消费者只声明需要的方法集；接口被 ≥2 个消费者复用时仍放消费方包，不去被依赖方暴露
- 单元测试必须能 mock port，不允许 build tag / init 替换实现

## 错误分层

- domain 定义 `Err*` → infrastructure 翻译 → application 编排 → middleware 映射 HTTP
- 响应体 `{"error":"..."}` 冻结
- API 向后兼容由 `api/http/contract_test.go` + `testdata/contracts/*.golden.json` 守护

## 架构决策（WHY）

| 决策 | 原因 |
|------|------|
| 多租户 PostgreSQL schema 隔离 | `SET LOCAL search_path` 切换租户，`pkg/tenantdb` 封装 |
| JWT RS256（非 HS256） | 非对称签名，网关可验证无需共享密钥 |
| NATS JetStream（非 Kafka） | 轻量、Go 原生、支持持久化 subject 格式 `domain.action` |
| Milvus v2.4.2（非 pgvector） | GraphRAG 需要高维向量检索，pgvector 性能不达标 |
| Harness 生命周期管理 | 组件顺序启动 → 逆序停止，避免依赖竞争 |
| LLMGateway 统一抽象 | 屏蔽当前 Qwen/Zhipu OpenAI-compatible provider 差异，切换不改业务代码 |
| No AI control logic | 路由/重试/状态机必须硬编码，AI 只做语言任务 |
