# 依赖监控告警处置

Grafana 先看“Stratum 依赖与监控系统”。当前只采集已验证的 etcd 2379 和 Milvus 9091 metrics target；
PostgreSQL、Redis、NATS、MinIO 未声明原生指标，禁止据此推断其健康。只读检查：

```bash
kubectl get pods,svc,endpoints -n stratum -o wide
kubectl get servicemonitor -n monitoring
kubectl describe servicemonitor -n monitoring stratum-dependencies
```

按 Prometheus target → Service endpoints → 依赖 Pod Ready/restart → 网络策略 → 最近配置变更排查。缓解限于
恢复监控端口/selector、回滚依赖配置或由 owner 执行受控重启；不得删除 PVC 或清理数据。恢复需 target 连续
up，且相关应用可用性/错误率正常。critical 立即升级依赖与平台 owner；warning 工作时段处理。保留 target
标签、events、revision 与恢复时间，不抓取原始 metrics body，不输出 Secret/env/token。

<a id="dependency-target-missing"></a>

## StratumDependencyTargetMissing

影响：etcd 或 Milvus 监控失明，依赖本身未必故障；紧急度：warning。分别查询
`up{namespace="stratum",service="stratum-etcd-metrics",endpoint="metrics"}` 和
`up{namespace="stratum",service="stratum-milvus-metrics",endpoint="metrics"}`，缺失 series 或值为 0 即可识别具体
依赖，并在 targets 页按 service 核对。若应用同时异常，
按依赖故障升级；若仅 target 缺失，修复 ServiceMonitor/Service/端口映射。恢复后确认两类 target 和规则无错误。
