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
