# internal/memory/infrastructure/workers

该包提供按租户运行的后台记忆作业，包括事实抽取、过期清理、事实 supersede 判定、实体画像重建、History 聚合与分层晋级，以及租户发现与 worker 生命周期管理。

完整导入路径：`github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers`

```mermaid
flowchart TB
  subgraph internalPkg["包内实现"]
    direction TB
    watcher["tenant_watcher.go<br/>TenantWatcher · WorkerSet<br/>发现租户并管理 worker 生命周期"]
    workerSet["按租户构建 WorkerSet"]
    factJobs["extraction_worker.go · gc_worker.go<br/>ExtractionWorker · GCWorker<br/>抽取事实与清理过期事实"]
    supersedeJobs["supersede_worker.go · llm_superseder.go<br/>SupersedeWorker · LLMSuperseder<br/>候选事实 supersede 判定"]
    profileJob["profile_worker.go<br/>ProfileWorker<br/>实体画像重建"]
    historyJob["history_worker.go · history_summarizer.go<br/>HistoryWorker<br/>聚合、压缩、晋级与 source-ID 归档"]
    support["helpers.go · metrics.go<br/>SleepCtx · runWithRestart<br/>worker Prometheus 指标"]
    watcher --> workerSet
    workerSet --> factJobs
    workerSet --> supersedeJobs
    workerSet --> profileJob
    factJobs ~~~ supersedeJobs
    supersedeJobs ~~~ profileJob
    factJobs --> support
    profileJob --> support
  end
  projectDeps["直接项目依赖<br/>internal/memory/application<br/>internal/memory/domain · internal/memory/domain/port<br/>internal/memory/infrastructure/pipeline<br/>internal/llmgateway/domain<br/>pkg/constants · pkg/timeutil"]
  externalDeps["关键外部依赖<br/>pgxpool · Prometheus · zap"]
  tests["测试汇总<br/>extraction · GC · helpers · profile · supersede · history"]
  internalPkg --> projectDeps
  internalPkg --> externalDeps
  projectDeps ~~~ externalDeps
  tests -.对应包内行为测试.-> internalPkg
```

## 说明

`TenantWatcher` 周期读取租户并为新增租户构建 `WorkerSet`，移除租户时停止对应 worker。各 worker 都围绕 domain/port 执行单一后台职责，并用 `helpers.go` 的可取消等待与重启监督逻辑维持生命周期；`HistoryWorker` 在租户后台聚合、压缩并按 age/capacity 晋级历史段，只有替换段写入成功后才按精确 source IDs 归档来源；LLM supersede 与 History summarizer 都复用 tenant-scoped LLM 能力。
