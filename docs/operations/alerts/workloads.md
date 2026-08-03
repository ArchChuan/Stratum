# Kubernetes 工作负载告警处置

Grafana 先看“Stratum 资源容量”。只读命令：

```bash
kubectl get nodes
kubectl get pods -n stratum -o wide
kubectl describe pod -n stratum <pod-name>
kubectl get events -n stratum --sort-by=.lastTimestamp
```

按节点状态 → Pod phase/conditions → events → controller revision → 资源容量排查。缓解限于回滚、扩容或对单个
已确认故障 Pod 做受控重建；节点操作由平台负责人执行，不批量删除 Pod。回滚后检查 desired/ready、重启数和
服务探针。节点或多副本影响立即升级平台值班；单 Pod warning 交服务 owner。保留 describe/events 摘要、
revision 和时间线，不输出 Secret、env 或日志中的敏感正文。

<a id="node-not-ready"></a>

## StratumNodeNotReady

影响：承载 Stratum 的节点不可调度，可能造成整体不可用；紧急度：critical。查询
`kube_node_status_condition{condition="Ready",status="true"}`。确认节点、受影响 Pod 和其他节点容量；平台
负责人有界隔离/恢复节点，应用侧只做容量允许下的迁移。恢复需节点 Ready 且 Pod/探针稳定。

<a id="pod-restarting-frequently"></a>

## StratumPodRestartingFrequently

影响：实例抖动或短暂失败；紧急度：warning。查询
`increase(kube_pod_container_status_restarts_total{namespace="stratum"}[10m])`。按 exit reason、events、OOM、
probe 和发布顺序定位；若新版本相关则回滚，恢复后观察重启计数不再增长。

<a id="pod-cumulative-restarts"></a>

## StratumPodCumulativeRestarts

影响：Pod 间歇崩溃并自行恢复，未达到 CrashLoopBackOff 阈值但持续累积；紧急度：warning。查询
`increase(kube_pod_container_status_restarts_total{namespace="stratum"}[30m])`。
此告警填补短窗口 `StratumPodRestartingFrequently`（10 分钟 3 次）与 `StratumPodCrashLooping` 之间的盲区——
每秒崩溃一次但进程立刻退出，10 分钟内累积 600 次重启，滑动窗口仍可能漏报。
按 exit code、events、应用日志定位根因；新版本相关则回滚，恢复后确认 30 分钟窗口重启增量回归正常。

<a id="pod-crash-looping"></a>

## StratumPodCrashLooping

影响：实例持续不可用；紧急度：critical。查询
`max_over_time(kube_pod_container_status_waiting_reason{namespace="stratum",reason="CrashLoopBackOff"}[5m])`。
检查启动错误分类、配置存在性和依赖，不读取 Secret 内容；回滚或修复配置引用后确认 Ready。

<a id="pod-pending-too-long"></a>

## StratumPodPendingTooLong

影响：期望副本无法启动、冗余下降；紧急度：warning。查询
`kube_pod_status_phase{namespace="stratum",phase="Pending"}`。先看 scheduler events，再查节点资源、PVC 和
image pull；按证据扩容/修复调度约束，禁止盲目反复重建。

<a id="pod-unhealthy-exit"></a>

## StratumPodUnhealthyExit

影响：容器以非零退出码反复崩溃，可能伴随功能间歇不可用；紧急度：warning。查询
`kube_pod_container_status_last_terminated_reason{namespace="stratum",reason=~"Error|OOMKilled"}` 定位
最近终止原因（如 panic 栈、OOM），再对照 `kubectl logs -n stratum <pod> --previous --tail=200` 与
events。此告警覆盖“每小时一次”的低频崩溃——`StratumPodRestartingFrequently`（10 分钟 3 次）和
`StratumPodCumulativeRestarts`（30 分钟 5 次）均可能漏报。若是新版本相关则回滚 revision；若是应用
panic，按栈定位并修复后重新发布。恢复后观察 2 小时窗口重启增量归零且 resolved 已送达。
