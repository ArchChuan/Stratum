# NATS / JetStream Development Rules

## 架构定位

NATS 在 stratum 中分两条线：**JetStream 专用于 memory pipeline**，是记忆持久化异步处理的消息总线（raw / enriched / extraction / reflection / DLQ 五条 stream，见下）。IAM 的 hermes 事件总线消费**平台共享 NATS core 连接**做领域事件分发（`internal/iam/infrastructure/hermes/client.go`；core 订阅无 durable 语义，所有实例离线期间发布的消息会丢失，需要不丢消息时必须迁移到 JetStream durable consumer）。

## Memory Pipeline 三阶段

```
[memory_service.Add]
        │
        ▼
  memory_outbox (PG 表)       ← outbox 预过滤（≥10 rune，≤2000 rune）
        │
  [OutboxPoller]               内部轮询间隔 1s，批次 50 条
        │  Publish
        ▼
  MEMORY_RAW stream            subject: memory.raw.{tenantID}
        │
  [EmbedWorker × 2]            Consumer: embed-worker，AckWait 30s，MaxDeliver 5
        │  向量化后 Publish
        ▼
  MEMORY_ENRICHED stream       subject: memory.enriched.{tenantID}
        │
  [EnrichWorker × 1]           Consumer: enrich-worker，AckWait 60s，MaxDeliver 5
│                       摘要触发阈值：1000 tokens，最多取 100 条历史
        ▼
  Milvus (向量) + PG (摘要/实体)
```

永久失败以及达到 `MaxDeliver` 的瞬态失败最终路由至 `MEMORY_DLQ` stream
（subject: `memory.dlq.{tenantID}`），保留 168h。

DLQ 事件只保存 message ID、tenant ID、stage、原 Stream/Subject、序列号、投递次数、错误分类和失败时间，
不保存用户正文或原始错误文本。DLQ publish 成功后才 `TermWithReason` 原消息；publish 失败则 `Nak`，避免
错误地确认并丢失原消息。DLQ 提供按错误码定向重放：`ReplayService.ReplayByErrorCode`
（`internal/memory/infrastructure/pipeline/replay.go`）+ `POST /admin/memory/dlq/replay`
（`api/http/router.go`，`api/http/handler/memory_dlq_replay_handler.go`），从 DLQ 拉取事件、error_code
匹配后重放回原始 raw subject。

## JetStream Stream 配置

| Stream | Subject Prefix | Retention | MaxAge | Storage |
|--------|---------------|-----------|--------|---------|
| `MEMORY_RAW` | `memory.raw.>` | WorkQueue | 72h | File |
| `MEMORY_ENRICHED` | `memory.enriched.>` | WorkQueue | 72h | File |
| `MEMORY_EXTRACTION` | `memory.extraction.>` | WorkQueue | 72h | File |
| `MEMORY_REFLECTION` | `memory.reflection.>` | WorkQueue | 72h | File |
| `MEMORY_DLQ` | `memory.dlq.>` | Limits | 168h | File |

`MEMORY_EXTRACTION` 承载对话事实提取任务（Redis buffer flush 后发布，`memory.extraction.{tenant}`）；
`MEMORY_REFLECTION` 承载任务结束后的工具轨迹反思任务（`memory.reflection.{tenant}`）。

Stream 由 `JetStreamManager.EnsureStreams(ctx)` 幂等创建（启动时调用）。

## Constants

所有 subject / stream 名均为常量，禁止硬编码字符串：

```go
// pkg/constants/memory.go
MemoryRawStream        = "MEMORY_RAW"
MemoryEnrichedStream   = "MEMORY_ENRICHED"
MemoryExtractionStream = "MEMORY_EXTRACTION"
MemoryReflectionStream = "MEMORY_REFLECTION"
MemoryDLQStream        = "MEMORY_DLQ"
MemoryRawSubject       = "memory.raw"
MemoryEnrichedSubject  = "memory.enriched"
MemoryExtractionSubject = "memory.extraction"
MemoryReflectionSubject = "memory.reflection"
MemoryDLQSubject       = "memory.dlq"
```

Subject 拼接方式：`fmt.Sprintf("%s.%s", constants.MemoryRawSubject, tenantID)`

## Connection Config

```
URL 格式：nats://host:4222          默认：nats://localhost:4222
```

连接由 `api/wiring/storage.go` 通过 `pkg/messaging/nats.Connect` 初始化，并把 `nats.Conn` / legacy `JetStreamContext` 放入共享 `Storage`。Memory wiring 再从同一连接创建 `nats.go/jetstream` 实例供 pipeline 和 worker 使用。连接失败 Warn，不阻断启动；此时 memory pipeline 不装配。

## EventPublisher Port

memory domain 层通过消费者侧 port 发布事件，不直接依赖 NATS：

```go
// internal/memory/domain/port/publisher.go
type EventPublisher interface {
    Publish(ctx context.Context, subject string, payload []byte) error
}
```

infrastructure 实现位于 `pipeline/outbox_poller.go`，使用 `js.Publish`。

## Pipeline 组件快速参考

| 组件 | 文件 | 职责 |
|------|------|------|
| `OutboxPoller` | `outbox_poller.go` | 轮询 PG outbox → publish to MEMORY_RAW |
| `EmbedWorker` | `embedder.go` | 消费 MEMORY_RAW → 生成向量 → publish to MEMORY_ENRICHED |
| `EnrichWorker` | `enricher.go` | 消费 MEMORY_ENRICHED → 提取实体/摘要 → 写 Milvus+PG |
| `JetStreamManager` | `jetstream.go` | EnsureStreams + CreateConsumer 幂等辅助 |
| `MemoryInjector` | `injector.go` | 实现 `agent/domain/port.MemoryInjector`，构建注入字符串 |
| `RecallTool` | `recall_tool.go` | 实现 `agent/domain/port.RecallMemoryFn`，供 ReAct 调用 |

## Rules

1. **不引入新的 NATS 用法**：business domain 不直接调用 `nats.Conn`，只通过 `EventPublisher` port
2. **Worker 幂等**：消息可重复投递（MaxDeliver > 1），处理逻辑必须幂等
3. **Handler 快速返回**：Worker 的消费 goroutine 内不做阻塞操作，重型任务已经是异步
4. **DLQ 监控**：`memory_dlq_total{tenant_id,stage}` 异常增长说明 Embed 或 Enrich 持续失败，需告警
5. **Subject 不含空格**：命名遵循 `domain.stage.tenantID` 三段格式
6. **Consumer 配置改动**：修改 AckWait/MaxDeliver 需同步更新 `pkg/constants/memory.go`
7. **AckWait 续租**：Worker 处理及 DLQ publish 期间按 `AckWait/2` 发送 `InProgress`，在最终 `Ack/Term/Nak` 前停止续租
