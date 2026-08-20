# 业务与领域告警处置

本手册覆盖 Stratum 业务/领域告警：reaper、后台组件、goroutine panic、工作流、MCP 客户端、认证、
知识库、Hermes、Memory pipeline/worker 与评测。这些规则随 `monitoring/remote/rules/stratum-ai.yaml`
部署到 `monitoring/stratum-remote-rules`，对应指标由应用 `/metrics` 暴露。查询使用 Grafana
Explore，只读集群检查命令见各节；不输出 Secret、env、token 或原始响应正文。

<a id="reaper-down"></a>

## StratumReaperDown

影响：过期访客清理停止，访客与所属租户持续累积；紧急度：warning。查询
`reaper_last_cycle_timestamp_seconds`。先看 reaper 进程是否存活（Pod 重启/崩溃），再查
`kubectl logs -n stratum deploy/stratum --tail=200 | grep -i reaper`。缓解：修复崩溃根因后重新发布；
恢复标准是指标持续刷新且 resolved 已送达。

<a id="reaper-delete-errors"></a>

## StratumReaperDeleteErrors

影响：单个清理周期删除失败；紧急度：warning。查询
`increase(reaper_delete_errors_total[1h])`，按 `phase`（list/list_tenants/delete_tenant/delete_user）
定位。delete_user 硬失败需立即处理，其余先确认对应表/列是否存在。恢复后计数停止增长。

<a id="reaper-delete-errors-critical"></a>

## StratumReaperDeleteErrorsCritical

影响：清理大面积失败，访客数据持续残留；紧急度：critical。查询 4 小时累计错误数并升级 IAM/平台
owner。缓解限于修复代码/迁移后重新发布，禁止手工删用户绕过审计。恢复后 4 小时窗口无新增错误。

<a id="reaper-cycle-errors"></a>

## StratumReaperCycleErrors

影响：reaper 周期性失败；紧急度：warning。查询
`increase(reaper_cycles_total{outcome="error"}[1h])`。先看最近周期错误类型，再按
`StratumReaperDeleteErrors` 路径处理。恢复后 error 周期归零。

<a id="component-stale"></a>

## StratumComponentStale

影响：chat-cleanup/checkpoint-cleanup 超过 48 小时未运行；紧急度：warning。查询
`component_last_cycle_timestamp_seconds{component=~"chat-cleanup|checkpoint-cleanup"}`。先确认
Pod 与日志，再查组件注册是否被配置关闭；恢复后时间戳刷新。

<a id="component-error-rate"></a>

## StratumComponentErrorRate

影响：后台组件 1 小时内错误超过 5 次；紧急度：warning。查询
`increase(component_errors_total[1h])`。按 component/phase 定位并查对应日志；修复后计数停止增长。

<a id="goroutine-panic"></a>

## StratumGoroutinePanic

影响：已恢复的 goroutine panic；紧急度：warning。查询
`increase(goroutine_panics_total[10m])`。先按 component 与日志栈定位，再评估是否影响数据一致性；
修复后发布，观察 10 分钟窗口不再新增。

<a id="goroutine-panic-critical"></a>

## StratumGoroutinePanicCritical

影响：panic 风暴；紧急度：critical。查询 1 小时累计并立即升级。若与发布相关先回滚 revision，
再按栈修复。恢复后 1 小时窗口无新增且 resolved 已送达。

<a id="workflow-run-errors"></a>

## StratumWorkflowRunErrors

影响：工作流运行出错；紧急度：warning。查询
`increase(workflow_runs_total{status="error"}[10m])`。按 tenant 与运行详情定位失败节点；
修复或重跑后确认无新增错误。

<a id="workflow-error-rate"></a>

## StratumWorkflowErrorRate

影响：30 分钟累计 20 次以上工作流错误；紧急度：critical。立即冻结发布并升级；确认新 revision
相关性后回滚。恢复后错误计数与成功率同时回到正常。

<a id="mcp-client-errors"></a>

## StratumMCPClientErrors

影响：后端到 MCP server 调用错误；紧急度：warning。查询
`increase(mcp_client_requests_total{status="error"}[10m])`。按 server_name/operation 定位；
检查 MCP server 健康与配置，恢复后计数停止增长。

<a id="mcp-client-reconnects"></a>

## StratumMCPClientReconnects

影响：MCP 客户端频繁重连；紧急度：warning。查询
`increase(mcp_client_reconnects_total[1h])`。检查 server 就绪与网络策略；修复后重连计数停止增长。

<a id="auth-failures"></a>

## StratumAuthFailures

影响：认证失败增多；紧急度：warning。查询
`increase(auth_failures_total[10m])`。按 reason 分类：token 过期属正常波动，凭据/签名错误需立即
排查密钥轮换；不输出 token 或密钥内容。

<a id="knowledge-ingest-failures"></a>

## StratumKnowledgeIngestFailures

影响：知识入库失败；紧急度：warning。查询
`increase(knowledge_ingest_total{status=~"failed|error"}[30m])`。检查 chunk/embed/写入链路与依赖
（Milvus/LLM），修复后重跑失败任务并确认 ingest 计数恢复。

<a id="knowledge-embed-unavailable"></a>

## StratumKnowledgeEmbedUnavailable

影响：知识库 ingest/RAG 的嵌入模型不可用（空模型 / 目录缺失 / 目录查询失败 / 解析失败）；
紧急度：warning。查询 `increase(knowledge_embed_unavailable_total[15m])`。

知识库嵌入模型只有显式配置一个来源（无默认兜底）。触发即代表该租户的 workspace 嵌入模型
不可用，ingest 与 RAG 均 fail-closed。按 error 日志
`knowledge.embed.resolve_failed` 的 `reason` 字段区分原因：
`empty embedding model`（workspace 配置缺失）、`embedding model not in catalogue`
（显式模型不在模型管理目录）、`embedding catalogue unavailable`（目录查询故障）、
`resolve embedding failed`（provider 解析/连通失败）。

处置：按 tenant 检查 workspace 的 embedding_model 配置与模型管理目录状态。缺模型的
workspace 无法在创建后补填（模型不可变），需在模型管理启用/恢复对应模型，或新建 workspace。
注意：缺模型的存量 workspace 读取/更新不会报 400（必填校验只作用于创建路径），以本计数为
监控信号；此类 workspace 若走 semantic 分块策略，实际以 recursive 分块（chunk 阶段不触发
本计数，直到 embed 阶段才 fail-closed）。

关联告警：ingest 侧同一事件会同时触发 `StratumKnowledgeIngestFailures`（3/30m），属预期
配对告警。本计数 label 为 `tenant`，与 memory 侧 `memory_embed_unavailable_total` 的
`tenant_id` label 命名不一致，是两组件既有设计决策，跨组件聚合时需对齐 label。

<a id="hermes-errors"></a>

## StratumHermesErrors

影响：Hermes 事件处理失败；紧急度：warning。查询
`increase(hermes_events_processed_total{status=~"publish_error|handler_error|unmarshal_error"}[10m])`。
按 event_type/status 定位 handler 或反序列化问题；修复后计数停止增长。

<a id="memory-pipeline-panics"></a>

## StratumMemoryPipelinePanics

影响：Memory pipeline panic；紧急度：warning。查询
`increase(memory_pipeline_panics_total[10m])`。按 component 与日志栈定位，评估消息是否进入 DLQ；
修复后发布，确认 10 分钟窗口无新增。

<a id="memory-dlq"></a>

## StratumMemoryDLQ

影响：Memory 消息进入死信队列；紧急度：warning。查询
`increase(memory_dlq_total[1h])`。按 tenant/stage 定位失败阶段，修复后确认 DLQ 不再增长并处理积压。

<a id="memory-dlq-critical"></a>

## StratumMemoryDLQCritical

影响：DLQ 大量堆积；紧急度：critical。立即升级 Memory owner；确认消费链路与存储可用性，
受控处理积压（保留审计），恢复后 DLQ 计数回落。

<a id="memory-embed-unavailable"></a>

## StratumMemoryEmbedUnavailable

影响：租户无可用嵌入模型，记忆写入持续进入 DLQ；紧急度：warning。查询
`increase(memory_embed_unavailable_total[15m])`。按 tenant_id 检查嵌入模型配置与 provider 连通性，
修复后确认计数停止增长。

<a id="memory-worker-panics"></a>

## StratumMemoryWorkerPanics

影响：Memory worker panic；紧急度：warning。查询
`increase(memory_worker_panics_total[10m])`。按 worker 定位日志栈；修复后发布并确认无新增。

<a id="memory-worker-error-rate"></a>

## StratumMemoryWorkerErrorRate

影响：Memory worker 错误率超过 10%；紧急度：warning。查询
`rate(memory_worker_messages_total{status="error"}[30m])`。按 worker/tenant 定位；
修复后错误率回落且消息吞吐正常。

<a id="evaluation-job-errors"></a>

## StratumEvaluationJobErrors

影响：评测任务错误；紧急度：warning。查询
`increase(evaluation_jobs_total{status=~"error|list_error"}[10m])`。按 status 定位评测链路；
修复后重跑失败任务并确认计数停止增长。

<a id="model-unhealthy"></a>

## StratumModelUnhealthy

影响：模型健康状态机进入 unhealthy 超过 5 分钟；紧急度：warning。查询
`model_health{status="unhealthy"}`。模型进入 unhealthy 后解析链视为未命中、继续沿降级链
降级并 fail-closed（不选中熔断模型），探活 worker 按 recovery 窗口周期放行 half-open 探测，
恢复后自动回 healthy。处置：按 model 检查 provider 连通性、上游限流/配额与最近错误
（`model.health.degraded` WARN 日志带 model/from/to），修复后确认 `model_health` 回到
healthy（`model.health.recovered` INFO 日志）。

<a id="memory-migration-stalled"></a>

## StratumMemoryMigrationStalled

影响：记忆嵌入迁移后台扫描连续两拍未推进进度；紧急度：warning。查询
`increase(memory_migration_stalled_total[15m])`。按 tenant_id/from_model/to_model 定位迁移。
停滞通常由 embed provider 连续失败（buildDocs 逐条 embed 失败 → 迁移标记 failed，不会停滞）、
仓库查询挂起或 worker panic 重启循环导致。处置：检查 `memory.migration.worker.*` 日志与
`memory.migration.*` 迁移事件日志；确认 embed resolver 对目标模型可用后，用管理接口
RetryMigration 续传（failed/canceled 均支持断点续传）。

<a id="route-fallback-surge"></a>

## StratumRouteFallbackSurge

影响：模型降级链回退频次异常升高（15 分钟 > 10 次）；紧急度：warning。查询
`increase(route_fallback_total[15m])`。降级链每次因模型失败沿链回退都会递增
`route_fallback_total{from_model,to_model}`；正常运行时主模型健康、回退应接近 0。频次骤升
通常伴随主模型 unhealthy（与 `StratumModelUnhealthy` 配对告警）。处置：按 from_model 检查健康
状态与 provider 连通性；若为上游侧故障，待恢复后回退自动回落。

<a id="llm-model-resolution-failed"></a>

## StratumLLMModelResolutionFailed

影响：LLM 模型解析配置失效（5 分钟内有增量）；紧急度：warning。查询
`increase(llm_model_resolution_errors_total[5m])`。代码内禁止写死兜底模型，模型名只能来自
模型目录（models / providers 表）；两种场景必须告警而非静默降级：
`reason=invalid_model`（显式请求的模型不在目录 / 已禁用 / provider 不健康，fail-closed 拒绝）
与 `reason=no_default`（未显式指定模型且目录没有任何默认/推荐模型）。处置：按 model/reason
检查模型管理页面的 models / providers 配置（default_model、enabled、recommended、
default_embedding），修复后计数停止增长、告警自动恢复。
