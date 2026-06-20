# NATS / JetStream Development Rules

## 架构定位

NATS JetStream 在 stratum 中**专用于 memory pipeline**，是记忆持久化三阶段异步处理的消息总线。其他业务域**不直接使用 NATS**——它们依赖各自 infrastructure 层封装的同步接口。

> 旧版 `internal/hermes/client.go` 通用事件总线已不存在，勿引用。

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
        │                       摘要触发阈值：4096 tokens，最多取 100 条历史
        ▼
  Milvus (向量) + PG (摘要/实体)
```

失败消息最终路由至 `MEMORY_DLQ` stream（subject: `memory.dlq.{tenantID}`），保留 168h。

## JetStream Stream 配置

| Stream | Subject Prefix | Retention | MaxAge | Storage |
|--------|---------------|-----------|--------|---------|
| `MEMORY_RAW` | `memory.raw.>` | WorkQueue | 72h | File |
| `MEMORY_ENRICHED` | `memory.enriched.>` | WorkQueue | 72h | File |
| `MEMORY_DLQ` | `memory.dlq.>` | Limits | 168h | File |

Stream 由 `JetStreamManager.EnsureStreams(ctx)` 幂等创建（启动时调用）。

## Constants

所有 subject / stream 名均为常量，禁止硬编码字符串：

```go
// pkg/constants/memory.go
MemoryRawStream       = "MEMORY_RAW"
MemoryEnrichedStream  = "MEMORY_ENRICHED"
MemoryDLQStream       = "MEMORY_DLQ"
MemoryRawSubject      = "memory.raw"
MemoryEnrichedSubject = "memory.enriched"
MemoryDLQSubject      = "memory.dlq"
```

Subject 拼接方式：`fmt.Sprintf("%s.%s", constants.MemoryRawSubject, tenantID)`

## Connection Config

```
URL 格式：nats://host:4222          默认：nats://localhost:4222
```

连接在 `internal/memory/infrastructure/pipeline/pipeline.go` 中初始化（`nats.Connect`），传入 `JetStreamManager`，再分发给各 Worker。连接失败 Warn 不阻断启动。

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
4. **DLQ 监控**：`dlqTotal` counter 异常增长说明 Embed 或 Enrich 持续失败，需告警
5. **Subject 不含空格**：命名遵循 `domain.stage.tenantID` 三段格式
6. **Consumer 配置改动**：修改 AckWait/MaxDeliver 需同步更新 `pkg/constants/memory.go`
