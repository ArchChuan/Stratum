# HTTP 性能告警处置

Grafana 先看“Stratum HTTP 性能”，按 route 模板和状态码缩小范围。安全查询如下：

```promql
stratum:http_requests:increase5m{service="stratum",environment="remote-test"}
stratum:http_5xx_ratio:ratio5m{service="stratum",environment="remote-test"}
stratum:http_requests_by_path:increase5m{service="stratum",environment="remote-test"}
stratum:http_5xx_ratio_by_path:ratio5m{service="stratum",environment="remote-test"}
stratum:http_request_duration_seconds_by_path:p95_5m{service="stratum",environment="remote-test"}
```

`increase5m` 是最近五分钟请求数，`ratio5m` 是 0–1 的失败比例，`p95_5m` 单位为秒；这些 recording
series 已完成窗口计算，查询时不要再次套 `rate()` 或 `increase()`。

只读检查：`kubectl get pods -n stratum -o wide`、`kubectl describe deployment/stratum -n stratum`、
`kubectl top pods -n stratum`。按流量变化 → route/status → Pod 资源/重启 → 依赖可用性 → 最近发布排查。
缓解限于停止发布、有界扩容、回滚单个 revision 或隔离已确认的坏实例；禁止无限重试或关闭超时。恢复需连续
两个窗口低于阈值并确认 resolved。critical 立即升级服务和平台值班；warning 工作时段交服务 owner。留存查询
截图、标签、时间线和变更号，不保存请求体、响应体、token 或用户数据。

<a id="high-http-5xx-rate"></a>

## StratumHighHTTP5xxRate

影响：部分请求失败；紧急度：warning。查询告警原式
`stratum:http_5xx_ratio:ratio5m{service="stratum",environment="remote-test"} > 0.05 and stratum:http_requests:increase5m{service="stratum",environment="remote-test"} >= 20`。
优先按 path 找集中错误，再核对依赖与发布；缓解后确认 5xx 比例和绝对请求量同时恢复。

<a id="critical-http-5xx-rate"></a>

## StratumCriticalHTTP5xxRate

影响：大面积 API 失败；紧急度：critical。查询告警原式
`stratum:http_5xx_ratio:ratio5m{service="stratum",environment="remote-test"} > 0.20 and stratum:http_requests:increase5m{service="stratum",environment="remote-test"} >= 20`，并对照公网可用性。立即冻结发布，确认新
revision 相关性后回滚；若依赖故障则按依赖 owner 升级。恢复后做关键路径无副作用探测。

<a id="high-http-p95-latency"></a>

## StratumHighHTTPP95Latency

影响：用户操作变慢；紧急度：warning。查询
`stratum:http_request_duration_seconds_by_path:p95_5m{service="stratum",environment="remote-test"}`，按 path
查看最近五分钟 P95 秒数；总览告警 series 是
`stratum:http_request_duration_seconds:p95_5m{service="stratum",environment="remote-test"}`。先排除低流量噪声，
再检查 route、CPU throttling、内存和依赖；有限扩容或回滚后确认吞吐未下降掩盖延迟。

<a id="critical-http-p95-latency"></a>

## StratumCriticalHTTPP95Latency

影响：请求接近超时、可用性受损；紧急度：critical。查询
`stratum:http_request_duration_seconds:p95_5m{service="stratum",environment="remote-test"} > 5`，并用
`stratum:http_request_duration_seconds_by_path:p95_5m{service="stratum",environment="remote-test"}` 定位最慢
route；同时对照 5xx、in-flight 和依赖 target。优先回滚明确相关变更或隔离饱和实例，恢复需延迟、错误率和
请求量三者正常。
