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

影响：告警可能漏发或错误；紧急度：critical。查询
`increase(prometheus_rule_evaluation_failures_total[5m])`。按 rule group 定位表达式/series 问题，先回滚相关规则；
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
`increase(stratum_feishu_delivery_failures_total[5m])`。检查失败计数、Pod 状态和最近 webhook rotation；不要读取
webhook 或上游正文。必要时重新应用受控 Secret 并 rollout，恢复后验证 firing/resolved 且失败计数停止增长。
