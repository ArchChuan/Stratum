# 容量告警处置

Grafana 先看“Stratum 资源容量”。只读命令：`kubectl top nodes`、`kubectl top pods -n stratum`、
`kubectl get pvc -A`、`kubectl describe node <node>`、`kubectl describe pvc -n <namespace> <pvc>`。
按当前用量 → 同标签历史/预测 → 工作负载分布 → requests/limits → 最近流量或发布排查。缓解限于有审批的扩容、
迁移负载、降低已确认的非关键并发或回滚容量回归；不得删除 PVC、TSDB、CRD 或历史。恢复需当前值与趋势均回到
安全区，Pod/目标正常且 resolved 已送达。critical 立即升级平台与数据 owner；warning 在工作时段规划容量。
留存查询摘要、节点/PVC 名称、revision、容量变更和恢复验证，不保存数据内容或凭据。

<a id="node-memory-low"></a>

## StratumNodeMemoryLow

影响：节点可能 OOM、驱逐 Pod；紧急度：warning。查询
`100 * (1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)`。对照 Pod working set 和 events；
有界扩容/迁移或回滚内存回归，确认 available 稳定恢复。

<a id="node-cpu-high"></a>

## StratumNodeCPUHigh

影响：请求延迟和调度受影响；紧急度：warning。查询
`100 * (1 - avg by(instance) (rate(node_cpu_seconds_total{mode="idle"}[5m])))`。对照 throttling、请求量和节点负载；
扩容或回滚后同时验证延迟与 CPU。

<a id="node-filesystem-low"></a>

## StratumNodeFilesystemLow

影响：节点写入失败、Pod 驱逐；紧急度：warning。查询
`100 * node_filesystem_avail_bytes{fstype!~"tmpfs|overlay"} / node_filesystem_size_bytes`。定位 mountpoint 和增长源；
按保留策略归档或扩容，禁止临时删除监控历史/PVC。

<a id="node-inodes-low"></a>

## StratumNodeInodesLow

影响：即使有字节空间也无法创建文件；紧急度：warning。查询
`100 * node_filesystem_files_free / node_filesystem_files`。先定位 mountpoint 和小文件来源；由平台 owner 有界清理
确认可再生的临时文件或扩容，保留清理清单。

<a id="container-memory-near-limit"></a>

## StratumContainerMemoryNearLimit

影响：容器接近 OOMKill；紧急度：warning。查询
`container_memory_working_set_bytes{namespace="stratum"} / container_spec_memory_limit_bytes{namespace="stratum"}`。
按容器、重启和流量定位；修复泄漏、调限额或回滚，禁止仅无限抬高 limit。

<a id="container-cpu-throttled"></a>

## StratumContainerCPUThrottled

影响：延迟上升、吞吐下降；紧急度：warning。查询
`rate(container_cpu_cfs_throttled_periods_total{namespace="stratum"}[5m]) / clamp_min(rate(container_cpu_cfs_periods_total{namespace="stratum"}[5m]), 0.001)`。
对照 usage、limit 和 HTTP P95；有证据后调 request/limit、扩容或回滚。

<a id="pvc-usage-high"></a>

## StratumPVCUsageHigh

影响：持久卷余量下降；紧急度：warning。查询
`100 * kubelet_volume_stats_used_bytes / kubelet_volume_stats_capacity_bytes`。确认 PVC 与增长趋势，安排扩容/归档；
恢复需低于阈值且趋势可控。

<a id="pvc-usage-critical"></a>

## StratumPVCUsageCritical

影响：持久服务即将写满；紧急度：critical。使用同一 PVC 查询并立即升级数据 owner；停止非必要写入或扩容，
不得删除数据库/监控历史作为临时方案。验证文件系统和应用写入恢复。

<a id="pvc-exhaustion-predicted"></a>

## StratumPVCExhaustionPredicted

影响：按趋势将在规划窗口耗尽；紧急度：warning。查询
`predict_linear(kubelet_volume_stats_available_bytes[6h], 4 * 24 * 3600)`，并确认同标签 `offset 6h` 有历史。
排除短时批处理后制定扩容/保留策略。

> 注意：local-path 卷的 `kubelet_volume_stats_*` 上报的是整块根盘统计（bind mount 无独立设备），因此该告警
> 对 local-path PVC 会随节点盘趋势集体触发。规则已通过 `kube_persistentvolumeclaim_info{storageclass!="local-path"}`
> 排除 local-path，只对真实 volume-backed PVC 生效；节点盘容量由 `StratumFilesystemExhaustionPredicted/Imminent`
> 覆盖。镜像/快照导致的节点盘增长由 k3s 镜像 GC（kubelet `image-gc-high/low-threshold`）与部署流水线
> 上线后的 `k3s ctr images prune` 清理（deploy.yml `Prune stale node images after rollout` 步骤）。

<a id="pvc-exhaustion-imminent"></a>

## StratumPVCExhaustionImminent

影响：PVC 即将耗尽；紧急度：critical。使用同一预测并缩短观察窗口，立即升级 owner，受控降写或扩容；恢复后
确认预测值为正且当前余量安全。

<a id="filesystem-exhaustion-predicted"></a>

## StratumFilesystemExhaustionPredicted

影响：节点文件系统按趋势将在规划窗口耗尽；紧急度：warning。查询
`predict_linear(node_filesystem_avail_bytes{fstype!~"tmpfs|overlay"}[6h], 4 * 24 * 3600)` 并核对 `offset 6h`。
确认 mountpoint 的稳定增长源后扩容或调整有依据的保留策略。

<a id="filesystem-exhaustion-imminent"></a>

## StratumFilesystemExhaustionImminent

影响：节点文件系统即将耗尽并可能中断服务；紧急度：critical。立即升级平台 owner，冻结非必要写入并扩容；
不得删除 Prometheus PVC/历史。恢复后验证当前余量、预测和节点状态。
