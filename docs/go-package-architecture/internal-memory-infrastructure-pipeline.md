# internal/memory/infrastructure/pipeline

该包构建基于 JetStream 和 PostgreSQL outbox 的异步记忆处理流水线，并提供富化、embedding、注入、召回及 LLM/向量适配能力。

完整导入路径：`github.com/byteBuilderX/stratum/internal/memory/infrastructure/pipeline`

```mermaid
flowchart TB
  subgraph runtimeGroup["流水线运行时"]
    lifecycle["pipeline.go · config.go<br/>Pipeline · Config · Start/Stop<br/>EmbedClient · LLMClient · resolver"]
    messaging["jetstream.go · outbox_poller.go · events.go · dead_letter.go<br/>JetStreamManager · OutboxPoller · DLQ publisher<br/>MemoryRawEvent/MemoryEnrichedEvent/DeadLetterEvent"]
    workers["enricher.go · enricher_prompt.go · embedder.go<br/>EnricherWorker · EmbedderWorker"]
  end
  subgraph adapterGroup["适配与查询"]
    adapters["embed_adapter.go · llm_extractor.go · vector_adapter.go<br/>EmbedClientAdapter · LLMExtractor · MilvusVectorAdapter"]
    query["injector.go · recall_tool.go<br/>MemoryInjector · RecallHandler<br/>BuildContext · hybrid recall fusion"]
  end
  metrics["metrics.go<br/>Prometheus metrics"]
  ports["internal/memory/domain/port"]
  llm["internal/llmgateway/domain"]
  pkg["pkg/constants · observability · tenantdb<br/>tokenutil · vector"]
  ext["NATS JetStream · pgx · Prometheus · zap"]
  tests["测试<br/>events_test.go · pipeline_test.go · recall_fusion_test.go"]
  lifecycle --> messaging
  lifecycle --> workers
  workers --> adapters
  messaging --> workers
  query --> adapters
  adapters --> ports
  workers --> ports
  query --> ports
  adapters --> llm
  lifecycle --> pkg
  messaging --> pkg
  query --> pkg
  messaging --> ext
  metrics --> ext
  tests -.events_test.-> messaging
  tests -.pipeline_test.-> lifecycle
  tests -.recall_fusion_test.-> query
```

## 说明

`Pipeline.Start` 创建 JetStream consumer，并监督 enricher、embedder 与 outbox poller 的 goroutine 生命周期。永久失败或最后一次瞬态失败通过 `dead_letter.go` 发布脱敏 DLQ 元数据。原始事件先由 `EmbedderWorker` 生成 embedding 并写入 Milvus，随后形成 enriched 事件，再由 `EnricherWorker` 调用 LLM 完成元数据富化与持久化；`MemoryInjector` 和 `RecallHandler` 在请求路径执行 PostgreSQL 文本检索与向量召回融合。
