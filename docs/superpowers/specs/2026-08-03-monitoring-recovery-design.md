# Stratum 监控告警与恢复回滚能力补齐设计

> **NOTE (2026-08-04):** `StratumPlatformMCP*` 相关告警随 platform-mcp 整体移除，本文其余内容继续有效。

日期：2026-08-03
状态：待评审
分支：`feat/monitoring-recovery`

## 1. 背景与目标

现状核查（2026-08-03，含远端只读运行证据）发现以下明确问题：

1. **线上每小时崩溃**：`cmd/server/runtime.go` 的 guest reaper 每小时调用
   `PrometheusMetrics.SetReaperCycleTimestamp`，而 reaper 四个指标在
   `pkg/observability/prometheus.go` 只声明、从未注册，全部为 nil，导致两个后端副本
   每整点 nil pointer panic（exit code 2）同时重启（22 小时各 22 次）。
2. **现有告警漏报**：`StratumPodRestartingFrequently`（10 分钟 >3 次）与
   `StratumPodCumulativeRestarts`（30 分钟 >5 次）对“每小时 1 次”的重启无感；
   `stratum-ai` PrometheusRule 虽已加载求值，但 reaper 指标不存在，规则永远无数据，静默。
3. **规则守卫缺失**：`stratum-ai` 22 条规则位于 helm chart，不在
   `monitoring/remote/` 权威范围内，缺少 runbook_url/dashboard_url、service/environment
   标签和 promtool 测试，且被 `serviceMonitor.enabled` 门控，存在静默失效风险。
4. **备份缺失**：仅本地 docker-compose 手册（`docs/DATA_PERSISTENCE.md`），远端
   PostgreSQL 无定时备份、无恢复演练。
5. **rollback_check 虚假**：`deploy.yml` 的 release receipt 中
   `rollback_check=passed` 是硬编码字面量，未执行任何回滚验证。

目标：

- 修复 reaper 指标注册缺陷并加回归测试，消除线上每小时崩溃。
- 将 `stratum-ai` 22 条规则全量迁移到 `monitoring/remote/`，纳入既有守卫
  （runbook 契约、标签、promtool 测试），移除 chart 门控副本。
- 新增进程级低频崩溃告警，覆盖本次漏报模式。
- 建立 PostgreSQL 每日备份 + CI 恢复演练；阿里云云盘快照作为节点/磁盘故障的
  基础设施级兜底（控制台确认项，仓库侧不执行写入）。
- release receipt 升级 v2：记录 `prior_digests`，`rollback_check` 诚实记 `pending`。

## 2. 决策记录（用户已确认）

| 决策点 | 结论 |
| --- | --- |
| 规则迁移范围 | 全量迁移 22 条（含去重/校准建议，见 4.2） |
| 备份方案 | 方案 1：GitHub Actions 定时 pg_dump + artifact + CI 恢复演练；阿里云快照作兜底 |
| rollback_check | 诚实记 `pending` + 记录 `prior_digests`，不做真实回滚演练 |

## 3. 范围与不做什么

### 本轮范围

- A：reaper 指标注册修复 + 指标冒烟回归测试。
- B：`stratum-ai` 22 条规则全量迁移 + 标签/runbook/promtool 测试 + chart 副本移除。
- C：新增 `StratumPodUnhealthyExit` 进程级崩溃告警。
- D：`scripts/backup/` + `.github/workflows/backup.yml`（pg_dump 日备 + 恢复演练）+
  备份与快照恢复 runbook。
- E：`deploy.yml` receipt v2（`prior_digests` + `rollback_check=pending`）+
  schema/测试同步更新。

### 明确不做（本轮）

- PG/Redis/NATS/MinIO 原生 exporter 采集与告警（独立后续）。
- LLM/Agent/Token 新业务指标设计（独立后续）。
- `stratum-platform-mcp` PrometheusRule 迁移（保留在 chart，后续一致性项）。
- Milvus/etcd/NATS/Redis 的备份与恢复（独立后续）。
- 自动回滚触发；回滚能力继续由 `rollback.yml` 手动工作流承担。
- 任何远端集群写入（备份仅通过 GitHub Actions 从集群拉取；远端部署在 PR 后另行征得许可）。

## 4. 方案

### 4.1 A：reaper 指标修复

在 `pkg/observability/prometheus.go` 的 `registerExtendedMetrics` 中补注册四个指标：

- `reaper_cycles_total{outcome}`（CounterVec）
- `reaper_guests_deleted_total`（Counter）
- `reaper_delete_errors_total{phase}`（CounterVec）
- `reaper_last_cycle_timestamp_seconds`（Gauge）

命名与 `helm/templates/stratum-prometheusrule.yaml`（迁移后的 `stratum-ai.yaml`）中
`StratumReaperDown` 等规则的表达式一致。

回归测试：新增 `pkg/observability/prometheus_metrics_test.go` 冒烟测试，对
`NewPrometheusMetrics` 实例按顺序调用 `MetricsProvider` 全部方法（含 reaper 四个方法），
使用代表性 dummy 参数，断言不 panic；同时对 `NoopMetrics` 做同样调用。任何“声明未注册”
的指标都会让测试失败。不为 reaper 方法添加 nil-guard——panic 是注册缺陷的显式信号，
正确做法是让 CI 在测试阶段发现，而不是在线上每小时崩溃。

### 4.2 B：`stratum-ai` 规则全量迁移

#### 迁移内容

将 `helm/templates/stratum-prometheusrule.yaml` 的 12 个 group、22 条 alert 全量迁移到
`monitoring/remote/rules/stratum-ai.yaml`，保留原分组与表达式；其中
`StratumHTTP5xxRate` 按校准建议删除（见下），净迁入 21 条。

每条 alert 补充：

- labels：`service: stratum`、`environment: remote-test`、按语义 `component`
  （reaper/background/panic/workflow/mcp/auth/http/knowledge/hermes/memory/evaluation）、
  适用处 `alert_family`。
- annotations：`dashboard_url`（指向既有四个看板中语义最接近者）、`runbook_url`
  （指向新增 `docs/operations/alerts/business.md` 的对应 anchor）。

#### 规则校准（两处，评审时可否决）

1. 删除 `StratumHTTP5xxRate`：与 `StratumHighHTTP5xxRate`/`StratumCriticalHTTP5xxRate`
   （ratio + 绝对量下限，且已排除 health/metrics 路径）功能重复，保留 ratio 版为权威。
2. `StratumWorkflowErrorRate` 阈值由 `rate(...[30m]) > 0.1`（约 6 次/分钟）校准为
   `increase(workflow_runs_total{status="error"}[30m]) > 20`，与
   `StratumWorkflowRunErrors` 形成“零星错误（warning）+ 30 分钟累计大量错误（critical）”
   的互补关系。

其余 20 条规则表达式与阈值原样保留。

#### 移除 chart 副本

删除 `helm/templates/stratum-prometheusrule.yaml`。远端 `helm upgrade` 会随之删除
`stratum/stratum-ai` CR；规则改由 `deploy-remote-monitoring.sh` 渲染的
`monitoring/stratum-remote-rules`（带 `release: kps` 标签）承载。helm 删除与监控
reconcile 之间存在分钟级规则空窗，可接受（demo 环境）。

同步更新受影响的 chart 渲染/契约测试（如有引用该模板的测试）。

#### 测试

- `monitoring/remote/tests/stratum-rules.test.yaml` 的 `rule_files` 增加
  `../rules/stratum-ai.yaml`。
- 为关键规则增加 `alert_rule_test`：`StratumReaperDown`（指标过期触发、指标新鲜不触发）、
  `StratumGoroutinePanic`、`StratumKnowledgeIngestFailures` 等至少各一例正/反例。
- 新 runbook `docs/operations/alerts/business.md` 需通过
  `scripts/quality/monitoring-runbook-test.go` 的 anchor 契约
  （`<a id="..."></a>\n\n## <AlertName>` 精确匹配）。

### 4.3 C：进程级低频崩溃告警

在 `monitoring/remote/rules/stratum-workloads.yaml` 新增：

```promql
alert: StratumPodUnhealthyExit
expr: |
  increase(kube_pod_container_status_restarts_total{namespace="stratum"}[2h]) >= 2
    and on (namespace, pod, container)
      kube_pod_container_status_last_terminated_reason{namespace="stratum", reason=~"Error|OOMKilled"}
for: 5m
labels: { severity: warning, service: stratum, environment: remote-test,
          component: workload, alert_family: pod-health }
```

设计理由：本次线上模式为每 Pod 每小时 1 次重启，2 小时窗口内 ≥2 次可捕获；
单次发布每 Pod 只重启 1 次，不误报。`alert_family: pod-health` 复用既有抑制链。

配套：`docs/operations/alerts/workloads.md` 新增 anchor 与处置步骤；
`stratum-rules.test.yaml` 新增正例（2 小时 2 次 Error 重启触发）与反例（单次重启不触发）。

### 4.4 D：备份与恢复演练

#### 新增脚本

- `scripts/backup/backup-postgres.sh`：通过 SSH tunnel 执行
  `kubectl exec deploy/stratum-postgresql -n stratum -- pg_dump -Fc -U stratum -d stratum`，
  gzip 压缩并计算 sha256。凭据复用现有 GitHub Secrets（`SSH_DEPLOY_KEY`、
  `SSH_KNOWN_HOSTS`、`KUBE_CONFIG`），无新增 Secret。
- `scripts/backup/restore-drill.sh`：将 dump 恢复到临时 PostgreSQL 16（GitHub Actions
  service 容器），校验：public schema 表存在、tenant schema 数量 >0、关键表
  （users/tenants/agents 等）行数 >0；输出 `backup-drill-report.txt`；任一校验失败
  fail closed。

#### 新增工作流 `.github/workflows/backup.yml`

- 调度：每日 18:00 UTC（= 北京时间次日 02:00）+ `workflow_dispatch`。
- job `backup`：运行 `backup-postgres.sh`，上传 artifact `stratum-postgres-backup`
  （retention-days: 30，含 dump、sha256、时间戳）。
- job `restore-drill`：`needs: backup`，下载 artifact，启动 `postgres:16` service 容器，
  运行 `restore-drill.sh`，失败即工作流失败。
- 权限：`contents: read`，environment: production。

#### runbook

新增 `docs/operations/backup-restore-runbook.md`，包含：

- pg_dump artifact 的恢复流程（下载 → 临时 PG → pg_restore → 校验）。
- 阿里云快照兜底说明与恢复步骤（控制台/CLI 层面）。
- **待确认项**：当前实例（`/dev/vda`，100G，已用 78%）是否已配置自动快照策略，
  需用户在阿里云控制台确认并开启；仓库侧不执行任何写入。

### 4.5 E：rollback_check 诚实化

#### deploy.yml 变更

- 在 `Helm deploy` 步骤前新增 `Capture prior digests` 步骤：读取当前
  `deployment/stratum`、`stratum-frontend`、`stratum-platform-mcp`（stratum ns）与
  `stratum-feishu-alert-adapter`（monitoring ns）的镜像字段；首次部署时记录 `none`。
- release receipt 步骤：`rollback_check` 由硬编码 `passed` 改为变量 `rollback=pending`
  （诚实：本流水线未执行回滚验证）；写入 `prior_digests` 与
  `rollback_basis`（`prior_digests_preserved` 或 `first_deploy`）。

#### receipt schema v2

更新 `.test/schemas/release-verification.schema.json`：

- `version` 改为 `const: 2`。
- 新增可选 `prior_digests`（对象：backend/frontend/platform_mcp/feishu_adapter，
  值为镜像字符串或 `none`）。
- 新增可选 `rollback_basis`（enum：`prior_digests_preserved` / `first_deploy`）。
- `rollback_check` 枚举保持 `passed|failed|pending`。

同步更新：

- `scripts/quality/schema-test/authority_schemas_test.go` 的 `releaseReport()` fixture
  （version 2、prior_digests、rollback_check=pending）及用例。
- `scripts/quality/release-verification-test.sh`：保持对
  `rollback_check:$rollback` 的字段要求，不要求 `passed`。
- 检查 `cmd/verification-schema` 是否需要版本适配（仅做 schema 校验，预计无需改动）。

## 5. 验收标准

本地：

- `go vet ./... && go test -short ./...` 通过；新增 reaper 指标冒烟测试通过。
- `bash scripts/quality/monitoring-config-test.sh` 通过（promtool check/test rules、
  amtool check-config、runbook anchor 契约、dashboard JSON）。
- `helm template` 渲染确认不再生成 `stratum-ai` PrometheusRule；`make helm-lint` 通过。
- `make code-quality` 通过（新增函数满足复杂度/长度门禁）。
- `bash scripts/quality/risk-regression-guard.sh --explain` 与 `make risk-guardrails` 通过。
- 本地 docker PostgreSQL 验证 `restore-drill.sh`（用 `make infra-up` 的 PG 或临时容器）。
- `make test-verify-before-pr` 按 R3 运行 short + soak，无 failed/skipped/unreconciled。

远端部署后（需用户另行许可）：

- `/metrics` 出现 `reaper_last_cycle_timestamp_seconds` 等四个指标；后端 Pod 不再
  每小时崩溃（连续 ≥4 小时无 exit 2）。
- `stratum-remote-rules` 包含迁移后的 21 条业务规则 + 新增 `StratumPodUnhealthyExit`
  （共 22 条自定义告警），Prometheus 规则组求值无错误；观察窗口内无意外 firing。
- `backup.yml` 首次运行成功且 `restore-drill` 通过。
- 新部署的 release receipt 为 v2，含 `prior_digests` 与 `rollback_check=pending`。

待确认项：阿里云控制台确认/开启自动快照策略（用户侧）。

## 6. 风险与缓解

| 风险 | 缓解 |
| --- | --- |
| 迁移后业务指标首次出现可能触发新告警 | 保留原 `for` 窗口；部署后观察一个完整评估周期 |
| helm 删除 CR 与监控 reconcile 之间的规则空窗 | 窗口为分钟级，demo 环境可接受 |
| receipt schema v2 破坏现有消费方 | 同步更新 schema 测试与 release-verification 契约；version 显式升级 |
| 备份工作流依赖 GitHub Actions | 阿里云快照作基础设施兜底；备份失败时 workflow 可见并可手动重跑 |
| restore-drill 校验过松导致假通过 | 校验项明确（schema 存在、租户 schema、关键表行数），失败即 fail closed |

## 7. 交付阶段

- PR 1（监控与回滚诚实化）：A + B + C + E。
- PR 2（备份）：D。
- 每个 PR：worktree → 守卫 → `make test-verify-before-pr` → CI 全绿 → 合并 → 远端部署
  （部署前征得用户许可）。
