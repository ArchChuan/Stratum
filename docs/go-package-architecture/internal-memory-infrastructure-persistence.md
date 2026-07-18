# internal/memory/infrastructure/persistence

该包实现 memory 的 PostgreSQL/Redis/Milvus 持久化适配器，覆盖实体、事实、active snapshot、History、通用记忆、抽取队列、消息缓冲与向量数据清理。

完整导入路径：`github.com/byteBuilderX/stratum/internal/memory/infrastructure/persistence`

```mermaid
flowchart LR
  postgres["entity_repo.go · fact_repo.go · memory_repo.go<br/>EntityRepo · FactRepo · MemoryRepo"]
  tiered["active_snapshot_repo.go · history_repo.go<br/>ActiveSnapshotRepo · HistoryRepo"]
  queue["extraction_queue.go<br/>ExtractionQueue"]
  redis["message_buffer_store.go<br/>RedisMessageBufferStore"]
  milvus["milvus_adapter.go<br/>MilvusPortAdapter"]
  domain["internal/memory/domain"]
  ports["internal/memory/domain/port"]
  storage["pkg/storage/postgres · pkg/storage/milvus"]
  pgx["pgx v5 · pgxpool · pgconn"]
  redisdep["go-redis v9"]
  tests["测试<br/>active_snapshot_repo · history_repo · memory_repo · milvus_adapter<br/>message_buffer_store · entity_repo · extraction_queue · fact_repo · testutil"]
  postgres -.实现仓储端口.-> ports
  queue -.实现队列端口.-> ports
  redis -.实现缓冲端口.-> ports
  milvus -.实现向量端口.-> ports
  postgres --> domain
  queue --> domain
  postgres --> storage
  milvus --> storage
  postgres --> pgx
  queue --> pgx
  redis --> redisdep
  tests -.entity_repo / fact_repo.-> postgres
  tests -.extraction_queue.-> queue
  tests -.message_buffer_store.-> redis
```

## 说明

PostgreSQL 仓储通过 tenant-aware 执行器访问租户 schema，并将 `pgx/pgconn` 错误翻译为领域错误。active snapshot 使用来源时间防止旧/重复事件覆盖新快照；History 以确定性 aggregation key 和精确 source IDs 保证重试幂等与安全归档。Redis adapter 直接映射消息缓冲所需命令；Milvus adapter 包装公共 `VectorStore`，提供 upsert 和按用户/Agent 清理能力。
