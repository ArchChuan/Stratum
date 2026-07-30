# 远端服务治理监控验收证据（2026-07-29）

## 范围与结论

本次验收仅覆盖传统服务治理监控：可用性、HTTP RED、Kubernetes 工作负载、容量、依赖服务、监控系统自身和
飞书告警投递；不包含 LLM、Agent、Token 或业务指标。

集群内监控、四个 Grafana 看板以及飞书 firing/resolved 投递已通过。独立的 GitHub Actions 集群外健康监控
尚未通过：生产 `PUBLIC_BASE_URL` 是 HTTP，而监控程序按安全合同只接受 HTTPS。该问题必须通过为公网入口
配置 TLS 解决，不得放宽客户端的 HTTPS 校验。因此，本记录不能作为“集群外兜底已完成”的证明。

## 变更与部署

- 监控初始部署：PR #161，merge commit `aac424156c44b0d1de484913070ce5ead1f17650`。
- Blackbox 指标标签契约修复：PR #168，merge commit `d64e45c0191b25056e8e8eae942e99f3dcfa9662`。
- 当前部署候选：`10109bcf46caa6033d5678506cd3b6e6b917a83b`。
- 权威部署任务：GitHub Actions run `30437353530`，结论 `success`；应用 Helm 发布、健康检查和
  `Reconcile remote monitoring` 均通过。
- 验收任务：GitHub Actions run `30440883662`，head
  `63b20ca731f85ec29c2ae3a770a6c55bf267743b`，结论 `success`。

## 集群内验收结果

受控 runner 通过临时端口转发读取 Prometheus 和飞书适配器的脱敏状态，结果如下：

| 合同 | 结果 |
| --- | --- |
| 公网 Blackbox 样本 `probe_success` 唯一且健康 | `probe_samples=1` |
| 受验收查询发现的非健康目标 | `unhealthy_targets=0` |
| Stratum 规则组 | `rule_groups=6` |
| Stratum 规则组求值错误 | `rule_errors=0` |
| Stratum Grafana 看板 ConfigMap | `dashboards=4` |
| 飞书 firing | `delivery=success`，2026-07-29 09:58:43 UTC |
| 飞书 resolved | `delivery=success`，2026-07-29 10:03:40 UTC |

飞书测试使用唯一临时告警 `StratumMonitoringAcceptanceTest`，严重级别为 `critical`。适配器只有在飞书返回
成功响应后才增加 `feishu_alert_delivery_total{status="success"}`，因此上述两个结果证明飞书接受了 firing 与
resolved 消息。测试规则在 resolved 等待前显式删除，退出 trap 再执行幂等删除；未修改 PVC、CRD、Helm
history、Grafana 状态或 Prometheus TSDB。

## 本地系统验收

监控修复对应的版本化系统 soak 已通过：

- mode：`soak`
- profile：`test`
- duration：604 秒
- packs：12
- capabilities：94
- actions：408
- evidence：UI 140、HTTP 137、DB 169
- cleanup：passed
- residual entity IDs：空
- unverified：0
- attestation：
  `test/e2e/attestations/7b8e549cfaab098e6369b1d7168248770bd969bce5d803c3702c4d18809f11f7.json`

`E2E_REQUIRED_MODE=soak E2E_REQUIRED_PROFILE=test make e2e-attestation-check` 已通过。

上述 attestation 绑定监控修复源码，不绑定本证据文档提交。PR #170 为当前源码重新执行 600 秒 soak 时，三次
运行分别在 guest 响应、Agent Context 聊天页就绪和 Agent-Skill-MCP 创建页就绪处失败；失败点不同，且第三次
已使用独立数据库 `stratum_e2e_monitoring_acceptance` 排除共享数据库竞争。按照系统化调试门禁，三次状态漂移后
停止重跑，当前 PR 不得在缺少新 attestation 时合并。独立测试库已精确删除，没有保留测试实体。

本验收分支的部署安全合同和提交钩子通过。`make risk-guardrails` 的后端、架构、迁移、部署、认证、Knowledge、
Memory、MCP 和 runtime-governance 检查通过，但本机前端依赖状态使 typecheck 失败：缺少
`vitest/globals` 类型定义，同时本机 TypeScript 版本把 `baseUrl` 弃用升级为错误。远端验收任务自身的完整
`Test` job 通过。

## 集群外兜底阻断

定时任务 run `30439767566` 在进入探测前 fail closed，错误为
`REMOTE_HEALTH_URL must be a valid HTTPS URL`。其环境使用生产 `PUBLIC_BASE_URL` 拼接健康路径；当前值是 HTTP。
由于探测未执行，失败、去重和恢复状态迁移尚无真实远端证据，飞书兜底通知也未触发。

关闭该阻断需要：

1. 为远端公网入口配置可验证的域名证书和 TLS 1.2+；
2. 将 production `PUBLIC_BASE_URL` 更新为对应 HTTPS 地址；
3. 重新运行正常健康探测；
4. 在隔离测试状态下验证一次 failure、一次重复 failure（不得重复通知）和一次 recovery；
5. 确认临时 Issue/告警状态清理完成，再补充本记录。

## 可复用知识候选

- 类别：`case`；结论：Prometheus metric relabel 阶段添加的标签不会出现在 `/api/v1/targets` 的 target labels
  中，应通过 `/api/v1/query` 验证样本标签，或显式使用 target relabeling；证据：PR #168、部署任务
  `30437353530`；范围：Prometheus Operator、ServiceMonitor 和 Blackbox exporter；推荐去向：项目运行手册或
  ADR，并作为 Obsidian 候选提交 Hermes 去重。
- 类别：`correction`；结论：外部健康监控的 HTTPS 输入合同与仅 HTTP 的远端入口配置不兼容，定时任务会在
  探测前 fail closed；证据：GitHub Actions run `30439767566`；范围：当前 remote-test 公网入口；推荐去向：
  project Git/部署 ADR。
