# 监控系统告警处置

Grafana 先看“Stratum 依赖与监控系统”。只读检查：

```bash
kubectl get pods -n monitoring -o wide
kubectl get prometheusrule,servicemonitor -n monitoring
kubectl describe deployment -n monitoring stratum-feishu-alert-adapter
kubectl get events -n monitoring --sort-by=.lastTimestamp
```

按 Prometheus/Alertmanager 自指标 → controller/Pod 状态 → CR 状态 → 最近 GitOps/Secret rotation 排查。监控故障
期间以集群外健康探测和应用只读健康证据兜底，不把“没有告警”当作服务健康。缓解限于回滚监控 release/配置、
恢复单个组件或重新 rollout 适配器；不得 uninstall，不得删除 CRD、PVC、Grafana 状态或历史。恢复需 targets、
rules、通知和看板均正常，并做一次受控 firing/resolved。critical 立即升级平台值班；warning 工作时段处理。
留存状态摘要、规则错误分类、revision 和消息时间，不输出 Secret/env/webhook/raw response body。

<a id="prometheus-config-reload-failed"></a>

## StratumPrometheusConfigReloadFailed

影响：新 scrape/rule 配置未生效；紧急度：critical。查询
`prometheus_config_last_reload_successful`。检查 operator events 和最近配置 diff；回滚失败配置，恢复后确认值为 1、
预期 targets/rules 出现。

<a id="prometheus-rule-evaluation-failures"></a>

## StratumPrometheusRuleEvaluationFailures

影响：告警可能漏发或错误；紧急度：warning。查询
`increase(prometheus_rule_evaluation_failures_total[10m])`。按 rule group 定位表达式/series 问题，先回滚相关规则；
恢复后计数不再增长且所有 group `lastError` 为空。

<a id="alertmanager-notification-failures"></a>

## StratumAlertmanagerNotificationFailures

影响：告警已 firing 但飞书可能收不到；紧急度：critical。查询
`increase(alertmanager_notifications_failed_total[5m])`。先确认适配器 Service/target，再检查 Alertmanager 路由；
修复后执行唯一测试告警验证 firing/resolved。

<a id="feishu-adapter-missing"></a>

## StratumFeishuAdapterMissing

影响：飞书通知链路完全失明；紧急度：critical。查询
`absent(up{namespace="monitoring",service="stratum-feishu-alert-adapter"})`。检查 Deployment、Service、
ServiceMonitor、端口和 rollout；只验证 Secret 存在与引用，不读取内容。恢复后 target up 并做通知测试。

<a id="blackbox-exporter-missing"></a>

## StratumBlackboxExporterMissing

影响：公网可用性探针失明；紧急度：critical。查询
`absent(up{job=~".*blackbox.*",environment="remote-test"})`。对照集群外探测 Issue，检查 exporter release、Service
和 ServiceMonitor；回滚相关 release 后确认 exporter 与 probe series 恢复。

<a id="feishu-delivery-failures"></a>

## StratumFeishuDeliveryFailures

影响：适配器收到告警但 webhook 投递失败；紧急度：critical。查询
`feishu_alert_delivery_total{status="failed"}`，并在 Grafana 选择最近十分钟范围检查增量。检查失败计数、Pod
状态和最近 webhook rotation；不要读取
webhook 或上游正文。必要时重新应用受控 Secret 并 rollout，恢复后验证 firing/resolved 且失败计数停止增长。

<a id="loki-unavailable"></a>

## StratumLokiUnavailable

影响：日志查询与告警数据源失明；紧急度：critical。查询
`up{namespace="monitoring",service="loki"}`。检查 Loki Deployment、PVC 与 ServiceMonitor；回滚/re-apply 后确认
target up 且写入日志可在 Grafana Loki 查询。

<a id="promtail-unavailable"></a>

## StratumPromtailUnavailable

影响：节点日志采集完全中断；紧急度：critical。查询
`up{namespace="monitoring",service="promtail"}`。检查 Promtail DaemonSet、hostPath 挂载与 ServiceMonitor；
恢复后确认 target up。

<a id="promtail-no-active-files"></a>

## StratumPromtailNoActiveFiles

影响：Promtail 存活但没 tail 到任何容器日志，Loki 持续无新数据——日志链路静默断裂（曾因 `__path__`
glob 缺容器目录层级导致 `files_active_total=0`）。紧急度：critical。查询
`promtail_files_active_total`。先在节点验证 `/var/log/pods/*<uid>/*/*.log` 能匹配（两级目录结构），再检查
relabel 的 `__path__` 规则；恢复后 `files_active_total>0` 且日志持续写入。

<a id="jaeger-unavailable"></a>

## StratumJaegerUnavailable

影响：trace 查询与采样接收失明；紧急度：critical。查询
`up{namespace="monitoring",service="jaeger",endpoint="admin"}`。检查 Jaeger Deployment、badger PVC 与
ServiceMonitor；恢复后确认 target up 且 trace 可查询。

<a id="otel-collector-unavailable"></a>

## StratumOtelCollectorUnavailable

影响：app→collector→Jaeger/Opik 的 trace 管道中断；紧急度：critical。查询
`up{namespace="stratum",service="stratum-otel-collector",endpoint="metrics"}`。检查 collector Deployment、
`stratum-config` 中 `OPIK_OTLP_ENDPOINT` 是否存在（缺失会导致 CreateContainerConfigError）、ServiceMonitor；
恢复后确认 target up 且新 trace 到达 Jaeger。

<a id="otel-exporter-failures"></a>

## StratumOtelExporterFailures

影响：collector 导出 span 失败，trace 可能缺失（曾因无 sending_queue/retry 静默丢批次）；紧急度：warning。
查询 `increase(otelcol_exporter_send_failed_spans[10m])` 并按 exporter 拆分。检查 Jaeger/Opik endpoint 可达性
与导出器 queue/retry 配置；恢复后失败计数停止增长且 span 正常导出。

<a id="otel-receiver-silent"></a>

## StratumOtelReceiverSilent

影响：后端在服务 HTTP 请求，但 collector 的 gRPC 传输零接收 span，trace 静默丢失；紧急度：warning。
这是 collector "up=1 且导出失败=0" 也覆盖不到的盲区。典型根因：应用 exporter 长连接被 kube-proxy
stale conntrack DNAT 钉在已删除的 collector backend IP 上（IP 被其它 collector 复用并应答成功），
应用"以为在导出"实际送错目标。排查：比较 tcpdump 中目标 IP 与 `kubectl get endpoints stratum-otel-collector -n stratum`；
确认应用 exporter 连接目标是服务 VIP 且 DNAT 到当前 backend；重启应用 Deployment 打断长连接即可恢复。
