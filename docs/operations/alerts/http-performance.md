# HTTP 性能告警处置

Grafana 先看“Stratum HTTP 性能”，按 route 模板和状态码缩小范围。安全查询如下：

```promql
sum by (path) (rate(stratum:http_requests_total:rate5m[5m]))
sum by (path) (rate(stratum:http_5xx_requests_total:rate5m[5m]))
histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket[5m])))
```

只读检查：`kubectl get pods -n stratum -o wide`、`kubectl describe deployment/stratum -n stratum`、
`kubectl top pods -n stratum`。按流量变化 → route/status → Pod 资源/重启 → 依赖可用性 → 最近发布排查。
缓解限于停止发布、有界扩容、回滚单个 revision 或隔离已确认的坏实例；禁止无限重试或关闭超时。恢复需连续
两个窗口低于阈值并确认 resolved。critical 立即升级服务和平台值班；warning 工作时段交服务 owner。留存查询
截图、标签、时间线和变更号，不保存请求体、响应体、token 或用户数据。

<a id="high-http-5xx-rate"></a>

## StratumHighHTTP5xxRate

影响：部分请求失败；紧急度：warning。查询
`sum(rate(stratum:http_5xx_requests_total:rate5m[5m])) / clamp_min(sum(rate(stratum:http_requests_total:rate5m[5m])), 0.001)`。
优先按 path 找集中错误，再核对依赖与发布；缓解后确认 5xx 比例和绝对请求量同时恢复。

<a id="critical-http-5xx-rate"></a>

## StratumCriticalHTTP5xxRate

影响：大面积 API 失败；紧急度：critical。使用与 warning 相同查询并对照公网可用性。立即冻结发布，确认新
revision 相关性后回滚；若依赖故障则按依赖 owner 升级。恢复后做关键路径无副作用探测。

<a id="high-http-p95-latency"></a>

## StratumHighHTTPP95Latency

影响：用户操作变慢；紧急度：warning。查询
`histogram_quantile(0.95, sum by (le,path) (rate(http_request_duration_seconds_bucket[5m])))`。先排除低流量噪声，
再检查 route、CPU throttling、内存和依赖；有限扩容或回滚后确认吞吐未下降掩盖延迟。

<a id="critical-http-p95-latency"></a>

## StratumCriticalHTTPP95Latency

影响：请求接近超时、可用性受损；紧急度：critical。沿 warning 的查询定位最慢 route，并对照 5xx、in-flight
和依赖 target。优先回滚明确相关变更或隔离饱和实例，恢复需延迟、错误率和请求量三者正常。
