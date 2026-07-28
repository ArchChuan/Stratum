# 远端服务治理监控运行手册

本文是远端测试环境传统服务治理监控的操作入口。范围仅包括可用性、HTTP 性能、Kubernetes
工作负载、容量、依赖服务和监控系统自身；不包含 LLM、Agent、Token 或业务指标。告警处置细节见
`alerts/` 下六类手册。

## 权威来源与固定版本

远端唯一配置权威是 `monitoring/remote/`，部署入口是 `scripts/deploy-remote-monitoring.sh`。固定版本由
`monitoring/remote/versions.env` 管理：`kube-prometheus-stack` 87.10.1、
`prometheus-blackbox-exporter` 11.15.1；release 分别是 `monitoring/kps` 和
`monitoring/stratum-blackbox`。飞书适配器部署必须使用 registry digest，禁止浮动 tag。

架构链路如下：Stratum `/metrics`、Kubernetes exporters、etcd/Milvus metrics 和 Blackbox 探针由
Prometheus 抓取；规则交给 Alertmanager；Alertmanager 调用集群内飞书适配器；适配器才持有 webhook。
Grafana sidecar 从 `grafana_dashboard=1` 的 ConfigMap 加载四个看板。GitHub Actions 的定时健康探测在
集群外独立检查公网 `/api/health`，用固定标题的 GitHub Issue 保存 firing/resolved 状态。

## 日常只读检查

先确认工作负载和目标，再看规则，最后看通知链路：

```bash
kubectl get pods -n monitoring
kubectl get servicemonitor,prometheusrule -n monitoring
kubectl get configmap -n monitoring -l grafana_dashboard=1
kubectl port-forward -n monitoring service/kps-kube-prometheus-stack-prometheus 19090:9090
```

在另一个终端只检查 API 状态和数量，不输出原始响应正文：

```bash
curl --fail --silent --max-time 5 'http://127.0.0.1:19090/api/v1/targets' |
  jq '{status, active: [.data.activeTargets[] | {health, job: .labels.job, service: .labels.service}]}'
curl --fail --silent --max-time 5 'http://127.0.0.1:19090/api/v1/rules' |
  jq '{status, groups: [.data.groups[] | {name, lastError}]}'
```

Grafana 应出现“服务总览、HTTP 性能、资源容量、依赖与监控系统”四个 Stratum 看板。若 Prometheus
查询正常但看板缺失，检查 dashboard ConfigMap 和 Grafana sidecar，而不是先修改告警规则。

## 飞书 webhook 轮换与端到端测试

1. 在飞书侧创建新 webhook；只把值写入受控 Secret 管理流程，不在命令行、工单或日志中回显。
2. 更新 `monitoring/stratum-feishu-webhook` 后重启适配器并等待 rollout：

    ```bash
    kubectl rollout restart deployment/stratum-feishu-alert-adapter -n monitoring
    kubectl rollout status deployment/stratum-feishu-alert-adapter -n monitoring --timeout=5m
    kubectl get pods -n monitoring -l app.kubernetes.io/name=stratum-feishu-alert-adapter
    ```

3. 用临时 `PrometheusRule` 产生一个明确标记为测试的 firing，确认飞书收到；删除该测试规则后确认收到
resolved。测试规则必须有短时限、唯一名称和负责人，验收完成立即清理。不要直接调用 webhook，也不要输出
Secret、容器环境变量或上游原始响应体。
4. 保留消息时间、alertname、状态和关联变更号；不要保存 webhook URL。

## 安全静默

只有已知维护窗口可静默。matcher 必须尽量窄（alertname、namespace、deployment），设置创建人、原因和
明确结束时间；优先分钟/小时级，不创建无结束时间或覆盖整个环境的 silence。创建前截图/记录现有 firing，
窗口结束后确认 silence 已过期且真实告警能重新投递。`critical` 默认全天通知，`warning` 默认仅工作日
09:00–19:00（Asia/Shanghai），不得用静默改变这项长期路由策略。

## 升级

1. 在 PR 中修改 `versions.env` 与对应 values，查阅上游 release notes 和 CRD 兼容说明。
2. 本地完成 render、promtool、amtool、路由、dashboard、部署安全和 secret scan。
3. 部署脚本先清点现有 Helm release、CR、PVC、dashboard 和 datasource，再使用
`helm upgrade --install --atomic --wait`。
4. 升级后验证目标、规则、四个看板、飞书 firing/resolved 和外部健康探测状态机。
5. 观察至少一个完整告警评估/通知周期后再结束变更窗口。

## 回滚

使用 `helm history -n monitoring <release>` 确认上一成功 revision，再执行有界的
`helm rollback -n monitoring <release> <revision> --wait --timeout 15m`。自定义资源回滚到上一个 Git
提交并重新 apply。任何情况下都不得运行 `helm uninstall`，不得删除 Prometheus/Grafana PVC、监控 CRD、
Helm history 或 TSDB 历史。回滚后重新验证 targets、rules、dashboard 和 firing/resolved。

## 保留、备份与恢复

Prometheus 当前保留 15 天且上限 15GB；Alertmanager 保留 120 小时。容量告警触发时先保留证据并扩容，
不要以删除 PVC 作为缓解。变更前备份 Helm values、相关 CR/ConfigMap 清单和 Grafana dashboard JSON；只备份
Secret 的存在性、版本/校验标识，不把 Secret 内容写入普通制品。若环境要求长期审计，应将告警事件与看板
快照导出到受控证据库，不能依赖短期 TSDB。恢复演练应在隔离环境验证查询和看板，禁止覆盖在线 PVC。

## 集群外健康监控状态

`.github/workflows/remote-health-monitor.yml` 每五分钟探测 `${PUBLIC_BASE_URL}/api/health`。固定标题的
GitHub Issue 是持久状态：body 只含 timestamp、status、diagnostic、notification；不要人工改标题或字段。
`pending` 表示飞书发送或状态写回尚未完成，`sent` 表示该状态已通知。该链路采用 at-least-once：在飞书成功
但 Issue 写回失败的极端窗口可能重复通知，优先避免漏警。恢复通知成功后 Issue 才关闭。排障时只记录 HTTP
状态/合同分类，不保存响应正文。

## 区分应用故障与监控故障

- 公网 Blackbox 和集群外探测都失败，而 Prometheus/Alertmanager 正常：优先按应用或入口故障处理。
- 应用健康检查正常，但 `StratumTargetMissing`：优先检查 ServiceMonitor、selector、端口和 Prometheus 抓取。
- 多类应用告警同时消失，并出现规则评估、配置 reload 或通知失败：先恢复监控控制面，不把“无告警”当健康。
- 集群内监控与公网探测同时不可达：按集群/节点/网络大故障升级，使用集群外证据确认影响。
- 飞书无消息但 Alertmanager 有 firing：检查适配器和 notification/delivery failures；不要反复制造业务告警。

所有事件都保留时间线、告警标签、查询结果摘要、只读命令输出、变更 revision 和恢复验证；凭据、Secret、
token、cookie、环境变量和原始上游响应体不得进入证据。
