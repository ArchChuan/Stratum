# Stratum 服务治理审计报告

**日期：** 2026-07-15
**模式：** 全项目首轮静态审计
**审计阶段：** 只读；未修改业务代码、测试、配置或运行状态

> **2026-07-17 状态说明：** 本文保留 2026-07-15 当日审计证据。随后 capability-boundary 重构删除了
> `internal/skill/infrastructure/gateway` 与 code/llm/http executor，因此 SG-006 已随被审计调用链移除，
> 不再是当前生产路径发现；下文涉及 Skill Gateway 的执行摘要、覆盖矩阵和正向措施均为原审计时点记录。

## 执行摘要

本轮确认 7 个 High 风险，未发现有充分静态证据的 Critical 风险。最需要优先处理的故障放大路径是：

1. Kubernetes readiness 永远返回成功，NATS 或 Milvus 缺失的副本仍会持续接收流量。
2. 生产环境 3 个副本加自动扩缩容时，进程内 LLM 限流会按副本倍增，无法兑现每用户 20 次/分钟。
3. Memory JetStream 只创建了 DLQ Stream，没有任何死信发布、终止、告警和重放闭环。
4. Embed 消费者的 30 秒 `AckWait` 短于单次 Embedding 请求允许的 60 秒，慢请求可能并发重投。
5. Skill Gateway 对所有非超时错误统一重试，带副作用的 Provider 可能被重复执行。

静态审计不能证明真实流量分布、Ingress 会话粘性、JetStream 运行参数、供应商错误率和生产指标，因此相关
结论均明确区分 `Confirmed`、`Probable` 与 `Needs Runtime Verification`。

## 范围与覆盖矩阵

| 边界 | 状态 | 本轮覆盖 |
|------|------|----------|
| Gin HTTP | Covered | Router、中间件、健康检查、Agent 执行限流、429 语义 |
| Browser/Axios | Partial | API 入口和代理超时；未逐页检查重复提交与请求取消 |
| LLM/MCP HTTP | Covered | LLM 重试/熔断/流式超时、Skill Gateway 重试；MCP 只检查配置与并发信号 |
| NATS JetStream | Covered | Stream/Consumer、Ack/Nak、AckWait、MaxDeliver、DLQ、fetch 退避与幂等 |
| PostgreSQL/pgx | Partial | 启动依赖、pool 上限、memory 事务；未逐仓库检查锁与查询预算 |
| Redis | Partial | 启动依赖和限流架构；未运行 Redis 故障实验 |
| Milvus | Partial | 启动降级、连接超时、memory upsert；未检查生产 collection 指标 |
| Background work | Covered | Memory workers、Knowledge ingest、Skill/JS 并发与关闭机制 |
| Health/OTEL | Covered | `/health`、Kubernetes probes、治理相关指标和日志 |

## 调用链与重试组合

### Agent 执行

```text
Ingress → 3~10 个后端副本 → 每副本进程内 token bucket
       → Agent 90s 总预算 → LLM provider 最多 3 次连接/请求尝试
```

限流状态不共享，扩容会扩大同一租户/用户可用令牌。LLM 客户端的重试位于 Agent 总预算之内，具备退避和
抖动；但供应商错误响应正文进入 error 后可能在重试耗尽日志中泄露。

### Memory pipeline

```text
PG outbox → MEMORY_RAW → EmbedWorker(AckWait 30s, MaxDeliver 5)
          → Embedding HTTP(单次最多 60s) → Milvus Upsert
          → MEMORY_ENRICHED → EnrichWorker(AckWait 60s, MaxDeliver 5)
          → LLM 30s → PG transaction → Ack
```

Embed 处理未发送 `InProgress`，单次允许时间已超过 `AckWait`。两类 Worker 失败时只 `Nak()`，没有证明
MaxDeliver 后原消息会发布至自定义 `memory.dlq.*`。

## 分级发现

### SG-001 Readiness 与真实依赖状态脱节

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** [router.go](/home/yang/go-projects/stratum/api/http/router.go:111)、[storage.go](/home/yang/go-projects/stratum/api/wiring/storage.go:40)、[deployment.yaml](/home/yang/go-projects/stratum/k8s/deployment.yaml:148)
- **Call path:** Kubernetes probe → `/health` → 固定 200；启动时 Milvus/NATS 失败仅 Warn
- **Trigger:** NATS、JetStream 或 Milvus 启动失败或运行中不可用。
- **Existing protection:** PostgreSQL 和 Redis 连接失败会阻止 Container 构建；Milvus/NATS 被设计为可选降级。
- **Failure and impact:** Pod 仍标记 Ready 并接收流量，但 Knowledge/Memory 能力处于不完整状态；部署和告警无法区分“进程存活”与“可承载请求”。
- **Repair direction:** 分离 liveness、readiness 和 capability/degraded 状态；readiness 只检查承载核心请求所需依赖，能力降级另行暴露。
- **Compatibility concern:** 直接把所有可选依赖加入 readiness 会造成不必要摘流，需先定义核心能力和可选能力。
- **Verification:** 断开 NATS/Milvus，验证 liveness 保持、readiness/能力状态变化和流量摘除策略。

### SG-002 多副本部署使进程内限流按副本倍增

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** [rate_limit.go](/home/yang/go-projects/stratum/api/middleware/rate_limit.go:17)、[router.go](/home/yang/go-projects/stratum/api/http/router.go:175)、[values-prod.yaml](/home/yang/go-projects/stratum/helm/values-prod.yaml:4)
- **Call path:** `POST /agents/:id/execute*` → 当前副本的 `RateLimiterStore` → LLM 执行
- **Trigger:** 生产 3 副本或 HPA 扩至最多 10 副本，负载均衡把同一用户请求分配到不同 Pod。
- **Existing protection:** 每个副本按 `tenantID:userID` 使用 token bucket；Ingress 另有粗粒度 `rate-limit: 100`。
- **Failure and impact:** 注释承诺的每用户 20 次/分钟实际可达约 60~200 次/分钟，增加 LLM 成本和下游容量压力；Pod 重启还会清空令牌状态。
- **Repair direction:** 使用 Redis 原子分布式限流，或明确把全局配额放在支持租户/用户键的网关层；保留本地并发隔离作为第二层保护。
- **Compatibility concern:** 必须定义 Redis 故障时 fail-open/fail-closed，以及突发容量和现有用户体验。
- **Verification:** 三副本并发发送同一租户/用户请求，确认一分钟内全局放行数、429 和 `Retry-After`。

### SG-003 Memory DLQ 只有资源定义，没有失败闭环

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** [jetstream.go](/home/yang/go-projects/stratum/internal/memory/infrastructure/pipeline/jetstream.go:27)、[embedder.go](/home/yang/go-projects/stratum/internal/memory/infrastructure/pipeline/embedder.go:149)、[enricher.go](/home/yang/go-projects/stratum/internal/memory/infrastructure/pipeline/enricher.go:183)
- **Call path:** Worker 失败 → `Nak()` → MaxDeliver=5；仓库中没有 `memory.dlq.*` 发布者、`Term()`、advisory 消费或重放器。
- **Trigger:** Embedding、LLM、Milvus 或 PG 持续失败，或消息成为 poison message。
- **Existing protection:** 自定义 DLQ Stream 和 `memory_dlq_total` 指标已注册；Consumer 设置 MaxDeliver。
- **Failure and impact:** 达到 MaxDeliver 的消息不会因 Stream 存在而自动复制到自定义 DLQ，失败记忆可能停留为未处理状态，指标也没有实际递增路径；文档声称“最终路由 DLQ”与代码不符。
- **Repair direction:** 明确永久/瞬态错误；实现显式 DLQ publish + 原消息终止，或消费 JetStream MaxDeliver advisory 后复制；补齐告警、查询和幂等重放。
- **Compatibility concern:** DLQ payload 不得包含原始敏感正文；必须定义 DLQ 发布失败时的原消息语义。
- **Verification:** 让消息连续失败超过 MaxDeliver，检查原消息、DLQ、计数器、告警和重放结果。

### SG-004 不可解析或缺少依赖的 Memory 消息被确认并永久丢弃

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** [embedder.go](/home/yang/go-projects/stratum/internal/memory/infrastructure/pipeline/embedder.go:121)、[embedder.go](/home/yang/go-projects/stratum/internal/memory/infrastructure/pipeline/embedder.go:136)、[enricher.go](/home/yang/go-projects/stratum/internal/memory/infrastructure/pipeline/enricher.go:169)
- **Call path:** RAW/ENRICHED 消息 → Unmarshal 或依赖解析失败 → `Ack()` → WorkQueue 删除消息
- **Trigger:** 事件 schema 不兼容、消息损坏、租户没有 embedding client，或 vector store 未配置。
- **Existing protection:** 记录错误/警告和部分计数器，避免 poison message 无限热循环。
- **Failure and impact:** 消息没有进入可查询的失败通道即被永久删除，用户记忆静默缺失；部署配置错误也被当成成功消费。
- **Repair direction:** 永久错误进入脱敏 DLQ 后 `Term/Ack`；暂时缺少依赖应停止拉取、延迟重投或暴露能力不可用，不应静默确认。
- **Compatibility concern:** 不能把永久 schema 错误无限重试；需要事件版本和失败分类。
- **Verification:** 注入损坏事件、无 embedding 配置和无 vector store 三种场景，核对最终状态与用户可见语义。

### SG-005 Embed AckWait 小于单次下游超时预算

- **Severity:** High
- **Confidence:** Probable
- **Evidence:** [memory.go](/home/yang/go-projects/stratum/pkg/constants/memory.go:33)、[timeouts.go](/home/yang/go-projects/stratum/pkg/constants/timeouts.go:18)、[embedder.go](/home/yang/go-projects/stratum/internal/memory/infrastructure/pipeline/embedder.go:118)
- **Call path:** EmbedWorker → `EmbedVector` → provider HTTP（最多 60s）→ Milvus → publish → Ack；Consumer AckWait=30s
- **Trigger:** Embedding 或 Milvus 处理超过 30 秒。
- **Existing protection:** Milvus 使用稳定 message ID Upsert；Consumer MaxDeliver=5；worker 数量固定。
- **Failure and impact:** 第一份消息仍执行时 JetStream 可重投给另一个 worker，造成并发重复 provider 调用、重复 enriched publish 和额外成本；最坏与 MaxDeliver 叠加。
- **Repair direction:** 建立处理总预算并确保短于 AckWait，或在长操作期间发送 `InProgress`；同时为 enriched publish 使用稳定去重键。
- **Compatibility concern:** 盲目增大 AckWait 会延迟真正故障后的恢复。
- **Verification:** 将 embedding 延迟设置为 35~45 秒，观察 `NumRedelivered`、并发调用数和 enriched 重复数。

### SG-006 Skill Gateway 对所有非超时错误统一重试

**当前状态（2026-07-17）：已退役。** 被审计的 Skill Gateway 与 provider 执行器已从当前源码删除；
现行 Skill 是由 Agent Loop 激活的版本化 instruction bundle，当前没有该重试调用链。

- **Severity:** High
- **Confidence:** Probable
- **Evidence:** [atomic.go](/home/yang/go-projects/stratum/internal/skill/infrastructure/gateway/atomic.go:99)、[atomic.go](/home/yang/go-projects/stratum/internal/skill/infrastructure/gateway/atomic.go:144)、[gateway.go](/home/yang/go-projects/stratum/internal/skill/infrastructure/gateway/gateway.go:46)
- **Call path:** Skill Execute → Provider（HTTP/code/MCP 等）→ 任意非 Deadline 错误 → 最多 10 次额外重试
- **Trigger:** 用户启用 Retry，Provider 返回认证、参数、业务拒绝、冲突或副作用完成后的网络错误。
- **Existing protection:** 每次尝试有独立 timeout、退避和抖动；最多 11 次；超时不重试；有熔断器。
- **Failure and impact:** 永久错误浪费容量并放大延迟；非幂等 Skill 可能重复写入或触发外部动作。熔断器只在整组重试耗尽后计一次失败，不能阻止组内放大。
- **Repair direction:** 由 Provider 返回稳定错误分类；只重试瞬态且满足幂等条件的操作，并增加整体 retry budget。
- **Compatibility concern:** 现有 Provider 错误契约需要扩展，不能靠错误字符串判断控制逻辑。
- **Verification:** 对 400/401/409/429/503、连接重置和“服务端成功但响应丢失”逐项统计调用次数与副作用。

### SG-007 LLM 错误链可能把原始供应商响应写入日志

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** [openai_compat.go](/home/yang/go-projects/stratum/internal/llmgateway/infrastructure/openai_compat.go:218)、[openai_compat.go](/home/yang/go-projects/stratum/internal/llmgateway/infrastructure/openai_compat.go:261)、[openai_compat.go](/home/yang/go-projects/stratum/internal/llmgateway/infrastructure/openai_compat.go:328)
- **Call path:** Provider 非 200 → 原始 body 拼入 `lastErr` → 重试耗尽 → `zap.Error(lastErr)`
- **Trigger:** 可重试的 429/5xx 连续失败；非流式和流式路径均存在。
- **Existing protection:** 中间重试日志只记录状态码和模型，不直接记录 body。
- **Failure and impact:** 最终错误日志可能包含用户输入、模型输出、供应商内部详情或其他敏感内容，违反项目禁止记录原始 HTTP response body 的安全底线，并扩大故障期间日志风险。
- **Repair direction:** 错误只保留状态、供应商错误码、脱敏摘要和 request/trace ID；原始 body 不进入 error 链。
- **Compatibility concern:** 保留足够诊断字段，避免完全丢失供应商错误分类。
- **Verification:** 使用包含敏感标记的 503 响应，确认日志、Trace 和 API 错误均不出现标记。

## 潜伏风险与证据缺口

- [client.go](/home/yang/go-projects/stratum/pkg/httpclient/client.go:61) 的通用 HTTP retry transport 会重试所有方法且没有重建 request body；本轮未找到 `WithRetry` 的生产调用，因此列为潜伏风险，不计入 High 总数。
- `RateLimiterStore` 的 map 只在请求触发且间隔满 10 分钟时清理。它有 30 分钟淘汰机制，但大规模一次性 key 的峰值内存需压测确认。
- MCP 配置允许重试和 timeout，本轮没有逐 transport 验证所有错误分类及总预算。
- PostgreSQL repository 的查询/锁超时没有完成逐方法核验；需要按高 QPS 写路径专项审计。
- Ingress `rate-limit: 100` 的确切语义依赖控制器版本与配置，不能视为租户级成本配额。

## 已有正确治理措施

- Knowledge ingest 同时限制接受队列和并发 worker，满载返回 429，并跟踪 WaitGroup 完成关闭。
- 原审计时点的 Skill code executor 有全局与租户级 semaphore；该 executor 当前已移除。
- Memory worker 的 Fetch 错误使用有上限的指数退避，避免 NATS 抖动时 CPU 自旋。
- Outbox publish 有 3 秒事务内超时；失败回滚保留 outbox，避免长期持锁和消息丢失。
- LLM 非流式、流式 TTFT、token idle 和 Agent 总执行时间采用分层 timeout；流式建立后不盲目重试。
- 原审计时点的 LLM provider 和 Skill Gateway 均实现互斥保护的 HalfOpen 单探测熔断状态；当前仅前者仍存在。
- PostgreSQL 和 Redis 连接失败会阻止服务构建，避免关键依赖缺失时启动成假健康实例。

## 建议修复顺序

1. **先修 SG-003、SG-004、SG-005：** 共同定义 JetStream 失败分类、Ack 预算和 DLQ 闭环，避免分别修补产生冲突。
2. **并行修 SG-001：** 明确核心 readiness 与可选能力 degraded 状态，随后补故障环境 E2E。
3. **修 SG-002：** 先确定全局配额语义和 Redis 故障策略，再实现分布式限流。
4. **SG-006 无需单独修复：** capability-boundary 重构已删除该执行路径；若未来重新引入可执行 provider，必须重新审计错误分类与幂等契约。
5. **修 SG-007：** 变更局部、风险明确，可独立快速完成并补脱敏测试。

## 未覆盖与运行验证建议

- 未连接真实 NATS、Redis、Milvus、PostgreSQL 或 LLM Provider；未执行故障注入和负载测试。
- 未逐页审计前端重复提交、Axios 取消和 429/503 UX。
- 未逐个检查全部 repository、MCP transport 和第三方 SDK 的内部重试。
- 下一阶段建议先构建隔离环境，验证 NATS 35~45 秒慢处理、MaxDeliver、DLQ、三副本限流和 readiness 摘流。
- 所有修复均涉及真实链路，必须使用 `stratum-e2e-development`，不得打印 token、密钥、原始 API key 或原始响应 body。

## 修复授权门禁

审计到此停止。请明确选择发现编号或修复范围后再修改业务代码。

## 修复记录

### 2026-07-15：SG-003、SG-004、SG-005

用户授权修复这三个 Memory JetStream 风险，并确认 DLQ 首版只保存脱敏元数据，不保存用户正文或原始错误
文本；独立重放能力推迟到受控存储、权限和审计方案明确后实现。

| Finding | 状态 | 实施结果 |
|---------|------|----------|
| SG-003 | 已完成失败隔离；受控重放待设计 | 永久错误和最后一次瞬态失败发布 DLQ，成功后 `TermWithReason`；发布失败 `Nak`；使用稳定 DLQ 消息 ID 去重 |
| SG-004 | 已修复 | 损坏事件、缺少 embedding/LLM/vector 依赖不再 `Ack` 静默丢弃，改为脱敏 DLQ |
| SG-005 | 已修复 | Embed/Enrich 处理期间按 `AckWait/2` 发送 `InProgress`，完成消息状态转换前停止续租 |

DLQ 元数据包含 message ID、tenant ID、stage、原 Stream/Subject、序列号、投递次数、错误分类和 UTC 时间；
不包含原始 payload。验证证据见本次提交的单元测试、嵌入式真实 JetStream 测试及最终 E2E 记录。

**验证结果：**

- `go test -race ./internal/memory/infrastructure/pipeline -count=1`：20 个测试通过。
- `go vet`：通过。
- `go test -short ./...`：711 个测试、78 个包通过。
- 真实环境启动 PostgreSQL/PgBouncer、Redis、NATS、Milvus 和后端，`GET /health` 返回 200。
- 向真实 `MEMORY_RAW` 发布一条带测试标记的损坏消息；运行中的 EmbedWorker 生成
  `stage=embed`、`error_code=invalid_event`、`original_stream=MEMORY_RAW` 的 DLQ 元数据，Stream 序列号和
  投递次数完整，测试标记未出现在 DLQ payload。
- Prometheus 出现对应 `memory_dlq_total{stage="embed",tenant_id="..."} 1`。
- 两轮代码审查发现并修复了 heartbeat/disposition 竞争、DLQ publish 期间租约空窗、去重 ID 包含可变错误
  分类、Metadata 缺失导致去重碰撞和 nil dereference 五个问题；最终复审无剩余 actionable finding，随后重新
  执行上述真实 E2E，未复用旧结果。
- 临时 E2E 脚本已删除，后端及本次启动的依赖容器已停止。

OTEL Collector 未在本次最小依赖集合中启动，后端出现 trace export unavailable 警告；不影响 NATS/DB/Milvus
验证链路，也不是本次修改引入的问题。
