# 可用性告警处置

Grafana 先看“Stratum 服务总览”。安全 PromQL 可在 Grafana Explore 查询；只读集群检查统一使用：

```bash
kubectl get pods,svc,endpoints -n stratum -o wide
kubectl describe deployment/stratum -n stratum
kubectl get ingress -A
```

原因按证据顺序排查：公网探针结果 → Service endpoints → Pod Ready/重启 → Ingress/网络 → 最近发布。
缓解必须有限：暂停发布、回滚单个 release、临时扩容；禁止绕过健康检查或删除数据。回滚前记录 revision，
回滚后确认公网和集群内探针连续恢复。critical 立即升级值班负责人；warning 在工作时段通知服务 owner；节点/
入口级影响升级平台负责人。留存时间线、标签、查询摘要、只读输出和 revision，不保存响应正文或凭据。

<a id="public-endpoint-down"></a>

## StratumPublicEndpointDown

影响：用户公网入口不可用；紧急度：critical。查询 `probe_success{service="stratum",environment="remote-test"}`。
优先检查 Blackbox 自身是否 up，再对照集群外健康探测 Issue；两者都失败时检查入口、Service 和后端。
缓解仅限回滚最近入口/应用变更或恢复故障节点，恢复标准是连续探测成功且 resolved 已送达。

<a id="backend-unavailable"></a>

## StratumBackendUnavailable

影响：API 请求失败；紧急度：critical。查询 `up{namespace="stratum",service="stratum",endpoint="http"}`。
按 endpoints、Ready、重启、资源和最近发布顺序定位；可有界回滚或扩容，恢复后验证 `/api/health` 合同和目标 up。

<a id="frontend-unavailable"></a>

## StratumFrontendUnavailable

影响：页面无法访问；紧急度：critical。查询 `probe_success{service="stratum",environment="remote-test"}` 并在
Grafana 对照后端可用性。若后端正常，检查前端 Service/Ingress/静态资源发布；回滚前端 revision 后验证页面探针。

<a id="target-missing"></a>

## StratumTargetMissing

影响：监控失明，应用未必故障；紧急度：warning。查询
`absent(up{namespace="stratum",service="stratum",endpoint="http"})`。检查 ServiceMonitor selector、Service
标签、端口名和 Prometheus targets；只修复发现链，不重启健康应用。恢复标准是目标重新出现并连续为 up。
