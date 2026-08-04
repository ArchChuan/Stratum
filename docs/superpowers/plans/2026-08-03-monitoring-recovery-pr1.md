# Monitoring Recovery PR1 Implementation Plan

> **NOTE (2026-08-04):** `StratumPlatformMCP*` 告警规则已随 platform-mcp 移除；`StratumReaperDown` 中 platform-mcp 实例的常驻误报随之消失。
>
> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 guest reaper 指标注册缺陷、把 `stratum-ai` 21 条规则迁入 `monitoring/remote`、新增进程级崩溃告警，并把 release receipt 升级为诚实的 v2（`prior_digests` + `rollback_check=pending`）。

**Architecture:** 4 个独立任务：Go 指标注册修复 + 冒烟测试；Prometheus 规则（新告警 + 全量迁移）与 runbook；helm chart 规则副本移除；deploy.yml/schema 收据升级。全部工作在 `feat/monitoring-recovery` worktree 中完成。

**Tech Stack:** Go 1.25、Prometheus/promtool、kube-prometheus-stack、Helm、GitHub Actions。

## 执行环境约定

仓库规定禁止在 main 直接提交；本计划全部命令基于 worktree
`/home/yang/go-projects/stratum-monitoring-recovery`。普通命令用
`cd /home/yang/go-projects/stratum-monitoring-recovery && ...`；git 写命令统一用
`git -C /home/yang/go-projects/stratum-monitoring-recovery ...`（shell 钩子要求）。

---

### Task 1: 修复 reaper 指标注册（线上每小时崩溃根因）

**Files:**

- Modify: `pkg/observability/prometheus.go`（`registerExtendedMetrics`，约 460 行附近）
- Create: `pkg/observability/prometheus_metrics_test.go`
- Test: `pkg/observability/prometheus_metrics_test.go`

- [ ] **Step 1: 写失败测试（MetricsProvider 全方法冒烟）**

创建 `pkg/observability/prometheus_metrics_test.go`：

```go
package observability

import (
 "testing"

 "go.uber.org/zap"
)

// exerciseAllMetrics calls every MetricsProvider method with representative
// dummy arguments. Any metric that is declared but never registered makes the
// underlying Prometheus vector nil and panics, which fails this test.
func exerciseAllMetrics(m MetricsProvider) {
 // HTTP
 m.IncHTTPRequest("GET", "/health", 200)
 m.RecordHTTPRequestDuration("GET", "/health", 0.1)
 m.IncHTTPRequestsInFlight()
 m.DecHTTPRequestsInFlight()
 // Skill
 m.IncSkillExecution("skill-1", "rag", "ok")
 m.RecordSkillExecutionDuration("skill-1", 0.1)
 m.SetSkillCircuitBreakerState("skill-1", 0)
 // Agent
 m.IncAgentExecution("agent-1", "react", "ok")
 m.RecordAgentExecutionDuration("agent-1", "react", 0.1)
 m.RecordAgentStepCount("agent-1", "react", 3)
 m.IncSystemAssistantRequest("admin", "v1", "ok")
 m.RecordSystemAssistantTTFT("admin", "v1", 0.2)
 m.RecordOfficialDocsSearchResults("v1", "ok", 2)
 m.RecordSystemAssistantDiagnosticArea("admin", "security", "ok", 0.3)
 m.RecordSystemAssistantEvidenceGaps("v1", "ok", 1)
 m.IncResourceProposal("memory", "create", "approved")
 m.RecordResourceProposalReviewDuration("memory", "create", 0.4)
 m.RecordResourceProposalDraftEdits("memory", "create", 2)
 // Platform MCP (nil-guarded, must not panic)
 m.IncPlatformMCPRequest("tool", "low", "ok")
 m.RecordPlatformMCPRequestDuration("tool", "ok", 0.1)
 m.IncPlatformMCPRequestsInFlight()
 m.DecPlatformMCPRequestsInFlight()
 m.IncPlatformMCPAuthDenial("denied")
 m.IncPlatformMCPTokenExchange("ok")
 m.IncPlatformMCPReplayDenial("denied")
 m.IncPlatformMCPBackendRequest("tool", "2xx")
 m.IncPlatformMCPUnknownOutcome("tool")
 m.IncPlatformMCPContractMismatch("tool")
 m.SetPlatformMCPCertificateExpiry(3600)
 m.SetPlatformMCPCertificateRotation("rotated", 1)
 // LLM
 m.IncLLMRequest("qwen-plus", "qwen", "ok")
 m.RecordLLMRequestDuration("qwen-plus", "qwen", 0.5)
 m.IncLLMTokenUsage("qwen-plus", "prompt", 100)
 m.RecordLLMTokenHistogram("qwen-plus", "completion", 50)
 m.RecordLLMFirstTokenLatency("qwen-plus", "qwen", 0.3)
 // Knowledge / Memory
 m.IncKnowledgeQuery("rag", "ok")
 m.RecordKnowledgeQueryDuration("rag", 0.2)
 m.RecordMemoryRetrievalDuration("search", 0.1)
 m.IncKnowledgeIngest("ok")
 m.RecordKnowledgeIngestDuration(1.5)
 m.IncKnowledgeIngestInFlight()
 m.DecKnowledgeIngestInFlight()
 // Hermes
 m.IncHermesEvent("memory.raw")
 m.IncHermesEventProcessed("memory.raw", "ok")
 // Agent KPI (F3)
 m.IncAgentTaskCompleted("agent-1", "react", "proposal", "ok", "tenant-1")
 m.RecordAgentTaskLatency("agent-1", "proposal", 1.0)
 m.RecordAgentCostPerTask("agent-1", "proposal", 0.01)
 m.RecordAgentEvalScore("agent-1", "accuracy", 0.9)
 m.RecordAgentConversationTurn("agent-1", 5)
 // Scheduler / Reranker / Router
 m.IncScheduledFire("cron", "ok")
 m.IncRerankRequest("bge-m3", "ok")
 m.RecordRerankDuration("bge-m3", 0.2)
 m.IncRouteFallback("qwen-plus", "qwen-turbo")
 m.RecordBudgetRatio("tenant-1", 42)
 // Audit / Collab / Optimizer / Operation Gate / Schedule skew
 m.IncAuditEvent("high", "allowed")
 m.RecordAuditWriteQueueDepth(3)
 m.IncCollabPlan("parallel")
 m.RecordCollabTaskDuration("parallel", 2.0)
 m.IncOptimizerCandidate("proposal", "accepted")
 m.RecordOptimizerCycleDuration(3.0)
 m.IncOperationProposal("memory", "approved")
 m.RecordApprovalLatency("memory", 0.5)
 m.RecordScheduleSkew(1.5)
 // Reaper（注册缺陷的触发点）
 m.IncReaperCycle("ok")
 m.SetReaperCycleTimestamp(1785762800)
 m.IncReaperGuestDeleted()
 m.IncReaperDeleteError("delete_user")
 // Background components / Panics / Workflow / MCP client / Evaluation / Auth
 m.RecordComponentCycle("chat-cleanup")
 m.SetComponentCycleTimestamp("chat-cleanup", 1785762800)
 m.IncComponentError("chat-cleanup", "run")
 m.IncGoroutinePanic("memory-worker")
 m.IncWorkflowRun("tenant-1", "ok")
 m.RecordWorkflowRunDuration("tenant-1", 4.0)
 m.IncMCPClientRequest("mcp-server", "call", "ok")
 m.IncMCPClientReconnect("mcp-server")
 m.IncEvaluationJob("ok")
 m.IncAuthFailure("invalid_token")
}

func TestPrometheusMetricsAllMethodsRegistered(t *testing.T) {
 m := NewPrometheusMetrics(zap.NewNop())
 exerciseAllMetrics(m)
}

func TestNoopMetricsAllMethods(t *testing.T) {
 exerciseAllMetrics(NoopMetrics{})
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /home/yang/go-projects/stratum-monitoring-recovery && go test ./pkg/observability/ -run 'TestPrometheusMetricsAllMethodsRegistered' -count=1`
Expected: FAIL，panic 于 `SetReaperCycleTimestamp`（nil pointer dereference），证明 reaper 指标未注册。

- [ ] **Step 3: 注册四个 reaper 指标**

在 `pkg/observability/prometheus.go` 的 `registerExtendedMetrics` 末尾（`m.authFailuresTotal = ...` 之后、函数闭合 `}` 之前）追加：

```go
 m.reaperCyclesTotal = factory.NewCounterVec(
  prometheus.CounterOpts{Name: "reaper_cycles_total", Help: "Guest reaper cycles by outcome"},
  []string{"outcome"},
 )
 m.reaperGuestsDeleted = factory.NewCounter(
  prometheus.CounterOpts{Name: "reaper_guests_deleted_total", Help: "Expired guests deleted by the guest reaper"},
 )
 m.reaperDeleteErrors = factory.NewCounterVec(
  prometheus.CounterOpts{Name: "reaper_delete_errors_total", Help: "Guest reaper delete errors by phase"},
  []string{"phase"},
 )
 m.reaperCycleTimestamp = factory.NewGauge(
  prometheus.GaugeOpts{Name: "reaper_last_cycle_timestamp_seconds", Help: "Unix timestamp of the last guest reaper cycle"},
 )
```

指标名必须与 `monitoring/remote/rules/stratum-ai.yaml`（Task 3）中 `StratumReaperDown`/`StratumReaperDeleteErrors*`/`StratumReaperCycleErrors` 的表达式一致。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd /home/yang/go-projects/stratum-monitoring-recovery && go test ./pkg/observability/ -count=1`
Expected: PASS。再运行 `go vet ./pkg/observability/`。

- [ ] **Step 5: 提交**

```bash
git -C /home/yang/go-projects/stratum-monitoring-recovery add pkg/observability/prometheus.go pkg/observability/prometheus_metrics_test.go
git -C /home/yang/go-projects/stratum-monitoring-recovery commit -m "[fix](observability): register guest reaper metrics to stop hourly panic"
```

Expected: pre-commit 钩子全绿（含 `go vet`、增量 `go test`、gitleaks）。

---

### Task 2: 新增 StratumPodUnhealthyExit 进程级低频崩溃告警

**Files:**

- Modify: `monitoring/remote/rules/stratum-workloads.yaml`（文件末尾 `StratumPodPendingTooLong` 之后）
- Modify: `docs/operations/alerts/workloads.md`（文件末尾新增 anchor 章节）
- Modify: `monitoring/remote/tests/stratum-rules.test.yaml`（末尾新增测试块）

- [ ] **Step 1: 在 `stratum-workloads.yaml` 追加规则**

在 `StratumPodPendingTooLong` 规则块之后追加：

```yaml
      - alert: StratumPodUnhealthyExit
        expr: |
          (increase(kube_pod_container_status_restarts_total{namespace="stratum"}[2h]) >= 2
            and on (namespace, pod, container)
              kube_pod_container_status_last_terminated_reason{namespace="stratum",reason=~"Error|OOMKilled"})
            * on (namespace, pod) group_left (node, deployment) stratum:kube_pod:placement
        for: 5m
        labels:
          severity: warning
          service: stratum
          environment: remote-test
          component: workload
          alert_family: pod-health
        annotations:
          summary: Stratum container exits with non-zero status repeatedly
          description: A container restarted at least twice in two hours with a non-zero exit reason (current value {{ $value }}).
          dashboard_url: /d/stratum-resources
          runbook_url: /docs/operations/alerts/workloads.md#pod-unhealthy-exit
```

- [ ] **Step 2: 在 `docs/operations/alerts/workloads.md` 末尾追加处置章节**

```markdown
<a id="pod-unhealthy-exit"></a>

## StratumPodUnhealthyExit

影响：容器以非零退出码反复崩溃，可能伴随功能间歇不可用；紧急度：warning。查询
`kube_pod_container_status_last_terminated_reason{namespace="stratum",reason=~"Error|OOMKilled"}` 定位
最近终止原因（如 panic 栈、OOM），再对照 `kubectl logs -n stratum <pod> --previous --tail=200` 与
events。此告警覆盖“每小时一次”的低频崩溃——`StratumPodRestartingFrequently`（10 分钟 3 次）和
`StratumPodCumulativeRestarts`（30 分钟 5 次）均可能漏报。若是新版本相关则回滚 revision；若是应用
panic，按栈定位并修复后重新发布。恢复后观察 2 小时窗口重启增量归零且 resolved 已送达。
```

- [ ] **Step 3: 在 `stratum-rules.test.yaml` 追加测试块**

在文件末尾追加：

```yaml
  - name: workload rules detect slow repeated unhealthy exits
    interval: 1m
    input_series:
      - series: 'kube_pod_container_status_restarts_total{namespace="stratum",pod="slow-crash-pod",container="stratum"}'
        values: '0x61 1x60 2x5'
      - series: 'kube_pod_container_status_last_terminated_reason{namespace="stratum",pod="slow-crash-pod",container="stratum",reason="Error"}'
        values: '1x126'
      - series: 'kube_pod_info{namespace="stratum",pod="slow-crash-pod",node="remote-node"}'
        values: '1x126'
      - series: 'kube_pod_owner{namespace="stratum",pod="slow-crash-pod",owner_kind="ReplicaSet",owner_name="slow-crash-rs"}'
        values: '1x126'
      - series: 'kube_replicaset_owner{namespace="stratum",replicaset="slow-crash-rs",owner_kind="Deployment",owner_name="stratum"}'
        values: '1x126'
    alert_rule_test:
      - eval_time: 125m
        alertname: StratumPodUnhealthyExit
        exp_alerts:
          - exp_labels:
              severity: warning
              service: stratum
              environment: remote-test
              component: workload
              alert_family: pod-health
              namespace: stratum
              deployment: stratum
              node: remote-node
              pod: slow-crash-pod
              container: stratum
            exp_annotations:
              summary: Stratum container exits with non-zero status repeatedly
              description: A container restarted at least twice in two hours with a non-zero exit reason (current value 2).
              dashboard_url: /d/stratum-resources
              runbook_url: /docs/operations/alerts/workloads.md#pod-unhealthy-exit

  - name: workload rules do not alert on a single rollout restart
    interval: 1m
    input_series:
      - series: 'kube_pod_container_status_restarts_total{namespace="stratum",pod="rollout-pod",container="stratum"}'
        values: '0x61 1x65'
      - series: 'kube_pod_container_status_last_terminated_reason{namespace="stratum",pod="rollout-pod",container="stratum",reason="Error"}'
        values: '1x126'
      - series: 'kube_pod_info{namespace="stratum",pod="rollout-pod",node="remote-node"}'
        values: '1x126'
      - series: 'kube_pod_owner{namespace="stratum",pod="rollout-pod",owner_kind="ReplicaSet",owner_name="rollout-rs"}'
        values: '1x126'
      - series: 'kube_replicaset_owner{namespace="stratum",replicaset="rollout-rs",owner_kind="Deployment",owner_name="stratum"}'
        values: '1x126'
    alert_rule_test:
      - eval_time: 125m
        alertname: StratumPodUnhealthyExit
        exp_alerts: []
```

- [ ] **Step 4: 运行监控配置守卫**

准备工具（首次需要）：

```bash
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts 2>/dev/null || true
helm repo update prometheus-community >/dev/null
docker create prom/prometheus:v3.8.1 >/tmp/stratum-prom-container
docker cp "$(cat /tmp/stratum-prom-container):/bin/promtool" /tmp/promtool
chmod 0755 /tmp/promtool
docker create prom/alertmanager:v0.33.1 >/tmp/stratum-am-container
docker cp "$(cat /tmp/stratum-am-container):/bin/amtool" /tmp/amtool
chmod 0755 /tmp/amtool
```

Run: `cd /home/yang/go-projects/stratum-monitoring-recovery && PATH="/tmp:$PATH" bash scripts/quality/monitoring-config-test.sh`
Expected: 全部通过（promtool check/test rules、amtool check-config、runbook anchor 契约、dashboard JSON）。

- [ ] **Step 5: 提交**

```bash
git -C /home/yang/go-projects/stratum-monitoring-recovery add monitoring/remote/rules/stratum-workloads.yaml docs/operations/alerts/workloads.md monitoring/remote/tests/stratum-rules.test.yaml
git -C /home/yang/go-projects/stratum-monitoring-recovery commit -m "[feat](monitoring): add slow repeated unhealthy exit alert"
```

---

### Task 3: 全量迁移 `stratum-ai` 规则到 `monitoring/remote`

**Files:**

- Create: `monitoring/remote/rules/stratum-ai.yaml`
- Create: `docs/operations/alerts/business.md`
- Modify: `monitoring/remote/tests/stratum-rules.test.yaml`（`rule_files` 增加一行 + 末尾追加测试块）
- Delete: `helm/templates/stratum-prometheusrule.yaml`

- [ ] **Step 1: 创建 `monitoring/remote/rules/stratum-ai.yaml`**

完整内容（11 个 group、21 条 alert；`StratumHTTP5xxRate` 按设计删除；`StratumWorkflowErrorRate` 阈值已校准）：

```yaml
groups:
  - name: stratum-reaper
    rules:
      - alert: StratumReaperDown
        expr: (time() - reaper_last_cycle_timestamp_seconds) > 7200
        for: 10m
        labels:
          severity: warning
          service: stratum
          environment: remote-test
          component: reaper
        annotations:
          summary: Guest reaper has not run for over two hours
          description: The guest reaper last cycle is more than 7200s ago (current value {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#reaper-down

      - alert: StratumReaperDeleteErrors
        expr: increase(reaper_delete_errors_total[1h]) > 0
        for: 5m
        labels:
          severity: warning
          service: stratum
          environment: remote-test
          component: reaper
        annotations:
          summary: Guest reaper delete errors in the last hour
          description: The guest reaper recorded delete errors in the last hour (increase {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#reaper-delete-errors

      - alert: StratumReaperDeleteErrorsCritical
        expr: increase(reaper_delete_errors_total[4h]) > 20
        for: 15m
        labels:
          severity: critical
          service: stratum
          environment: remote-test
          component: reaper
        annotations:
          summary: Guest reaper delete errors are accumulating
          description: More than 20 guest reaper delete errors in four hours (current value {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#reaper-delete-errors-critical

      - alert: StratumReaperCycleErrors
        expr: increase(reaper_cycles_total{outcome="error"}[1h]) >= 3
        for: 10m
        labels:
          severity: warning
          service: stratum
          environment: remote-test
          component: reaper
        annotations:
          summary: Guest reaper error cycles in the last hour
          description: The guest reaper recorded at least three error cycles in the last hour (current value {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#reaper-cycle-errors

  - name: stratum-background
    rules:
      - alert: StratumComponentStale
        expr: (time() - component_last_cycle_timestamp_seconds{component=~"chat-cleanup|checkpoint-cleanup"}) > 172800
        for: 30m
        labels:
          severity: warning
          service: stratum
          environment: remote-test
          component: background
        annotations:
          summary: A background component has not cycled for 48 hours
          description: Component {{ $labels.component }} last cycled more than 172800s ago (current value {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#component-stale

      - alert: StratumComponentErrorRate
        expr: increase(component_errors_total[1h]) > 5
        for: 10m
        labels:
          severity: warning
          service: stratum
          environment: remote-test
          component: background
        annotations:
          summary: Background component errors in the last hour
          description: Component {{ $labels.component }} phase {{ $labels.phase }} recorded errors in the last hour (increase {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#component-error-rate

  - name: stratum-panics
    rules:
      - alert: StratumGoroutinePanic
        expr: increase(goroutine_panics_total[10m]) > 0
        for: 5m
        labels:
          severity: warning
          service: stratum
          environment: remote-test
          component: panic
        annotations:
          summary: Recovered goroutine panic in the last ten minutes
          description: Component {{ $labels.component }} panicked in the last ten minutes (increase {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#goroutine-panic

      - alert: StratumGoroutinePanicCritical
        expr: increase(goroutine_panics_total[1h]) > 10
        for: 10m
        labels:
          severity: critical
          service: stratum
          environment: remote-test
          component: panic
        annotations:
          summary: Goroutine panic storm in the last hour
          description: More than ten recovered panics in the last hour (current value {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#goroutine-panic-critical

  - name: stratum-workflow
    rules:
      - alert: StratumWorkflowRunErrors
        expr: increase(workflow_runs_total{status="error"}[10m]) > 0
        for: 5m
        labels:
          severity: warning
          service: stratum
          environment: remote-test
          component: workflow
        annotations:
          summary: Workflow run error in the last ten minutes
          description: Tenant {{ $labels.tenant_id }} had workflow run errors in the last ten minutes (increase {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#workflow-run-errors

      - alert: StratumWorkflowErrorRate
        expr: increase(workflow_runs_total{status="error"}[30m]) > 20
        for: 15m
        labels:
          severity: critical
          service: stratum
          environment: remote-test
          component: workflow
        annotations:
          summary: Workflow errors exceed 20 in thirty minutes
          description: Tenant {{ $labels.tenant_id }} accumulated more than 20 workflow errors in 30 minutes (current value {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#workflow-error-rate

  - name: stratum-mcp
    rules:
      - alert: StratumMCPClientErrors
        expr: increase(mcp_client_requests_total{status="error"}[10m]) > 3
        for: 10m
        labels:
          severity: warning
          service: stratum
          environment: remote-test
          component: mcp
        annotations:
          summary: MCP client errors in the last ten minutes
          description: Server {{ $labels.server_name }} operation {{ $labels.operation }} recorded errors (increase {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#mcp-client-errors

      - alert: StratumMCPClientReconnects
        expr: increase(mcp_client_reconnects_total[1h]) > 5
        for: 10m
        labels:
          severity: warning
          service: stratum
          environment: remote-test
          component: mcp
        annotations:
          summary: MCP client reconnects in the last hour
          description: Server {{ $labels.server_name }} reconnected frequently (increase {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#mcp-client-reconnects

  - name: stratum-auth
    rules:
      - alert: StratumAuthFailures
        expr: increase(auth_failures_total[10m]) > 5
        for: 10m
        labels:
          severity: warning
          service: stratum
          environment: remote-test
          component: auth
        annotations:
          summary: Auth failures in the last ten minutes
          description: Reason {{ $labels.reason }} recorded more than five failures in ten minutes (increase {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#auth-failures

  - name: stratum-knowledge
    rules:
      - alert: StratumKnowledgeIngestFailures
        expr: increase(knowledge_ingest_total{status=~"failed|error"}[30m]) > 3
        for: 10m
        labels:
          severity: warning
          service: stratum
          environment: remote-test
          component: knowledge
        annotations:
          summary: Knowledge ingest failures in the last thirty minutes
          description: Status {{ $labels.status }} recorded failures in the last 30 minutes (increase {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#knowledge-ingest-failures

  - name: stratum-hermes
    rules:
      - alert: StratumHermesErrors
        expr: increase(hermes_events_processed_total{status=~"publish_error|handler_error|unmarshal_error"}[10m]) > 0
        for: 5m
        labels:
          severity: warning
          service: stratum
          environment: remote-test
          component: hermes
        annotations:
          summary: Hermes event processing errors in the last ten minutes
          description: Event {{ $labels.event_type }} status {{ $labels.status }} recorded errors (increase {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#hermes-errors

  - name: stratum-memory-pipeline
    rules:
      - alert: StratumMemoryPipelinePanics
        expr: increase(memory_pipeline_panics_total[10m]) > 0
        for: 5m
        labels:
          severity: warning
          service: stratum
          environment: remote-test
          component: memory
        annotations:
          summary: Memory pipeline panic in the last ten minutes
          description: Component {{ $labels.component }} panicked in the memory pipeline (increase {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#memory-pipeline-panics

      - alert: StratumMemoryDLQ
        expr: increase(memory_dlq_total[1h]) > 5
        for: 10m
        labels:
          severity: warning
          service: stratum
          environment: remote-test
          component: memory
        annotations:
          summary: Memory DLQ messages in the last hour
          description: Tenant {{ $labels.tenant_id }} stage {{ $labels.stage }} accumulated DLQ messages (increase {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#memory-dlq

      - alert: StratumMemoryDLQCritical
        expr: increase(memory_dlq_total[1h]) > 50
        for: 10m
        labels:
          severity: critical
          service: stratum
          environment: remote-test
          component: memory
        annotations:
          summary: Memory DLQ backlog is critical
          description: Tenant {{ $labels.tenant_id }} stage {{ $labels.stage }} accumulated more than 50 DLQ messages in an hour (current value {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#memory-dlq-critical

  - name: stratum-memory-workers
    rules:
      - alert: StratumMemoryWorkerPanics
        expr: increase(memory_worker_panics_total[10m]) > 0
        for: 5m
        labels:
          severity: warning
          service: stratum
          environment: remote-test
          component: memory
        annotations:
          summary: Memory worker panic in the last ten minutes
          description: Worker {{ $labels.worker }} panicked in the last ten minutes (increase {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#memory-worker-panics

      - alert: StratumMemoryWorkerErrorRate
        expr: rate(memory_worker_messages_total{status="error"}[30m]) > 0.1
        for: 15m
        labels:
          severity: warning
          service: stratum
          environment: remote-test
          component: memory
        annotations:
          summary: Memory worker error rate exceeds ten percent
          description: Worker {{ $labels.worker }} tenant {{ $labels.tenant_id }} error rate is above 10% (current value {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#memory-worker-error-rate

  - name: stratum-evaluation
    rules:
      - alert: StratumEvaluationJobErrors
        expr: increase(evaluation_jobs_total{status=~"error|list_error"}[10m]) > 3
        for: 10m
        labels:
          severity: warning
          service: stratum
          environment: remote-test
          component: evaluation
        annotations:
          summary: Evaluation job errors in the last ten minutes
          description: Status {{ $labels.status }} recorded more than three errors in ten minutes (increase {{ $value }}).
          dashboard_url: /d/stratum-service-overview
          runbook_url: /docs/operations/alerts/business.md#evaluation-job-errors
```

- [ ] **Step 2: 创建 `docs/operations/alerts/business.md`**

```markdown
# 业务与领域告警处置

本手册覆盖 Stratum 业务/领域告警：reaper、后台组件、goroutine panic、工作流、MCP 客户端、认证、
知识库、Hermes、Memory pipeline/worker 与评测。这些规则随 `monitoring/remote/rules/stratum-ai.yaml`
部署到 `monitoring/stratum-remote-rules`，对应指标由应用 `/metrics` 暴露。查询使用 Grafana
Explore，只读集群检查命令见各节；不输出 Secret、env、token 或原始响应正文。

<a id="reaper-down"></a>

## StratumReaperDown

影响：过期访客清理停止，访客与所属租户持续累积；紧急度：warning。查询
`reaper_last_cycle_timestamp_seconds`。先看 reaper 进程是否存活（Pod 重启/崩溃），再查
`kubectl logs -n stratum deploy/stratum --tail=200 | grep -i reaper`。缓解：修复崩溃根因后重新发布；
恢复标准是指标持续刷新且 resolved 已送达。

<a id="reaper-delete-errors"></a>

## StratumReaperDeleteErrors

影响：单个清理周期删除失败；紧急度：warning。查询
`increase(reaper_delete_errors_total[1h])`，按 `phase`（list/list_tenants/delete_tenant/delete_user）
定位。delete_user 硬失败需立即处理，其余先确认对应表/列是否存在。恢复后计数停止增长。

<a id="reaper-delete-errors-critical"></a>

## StratumReaperDeleteErrorsCritical

影响：清理大面积失败，访客数据持续残留；紧急度：critical。查询 4 小时累计错误数并升级 IAM/平台
owner。缓解限于修复代码/迁移后重新发布，禁止手工删用户绕过审计。恢复后 4 小时窗口无新增错误。

<a id="reaper-cycle-errors"></a>

## StratumReaperCycleErrors

影响：reaper 周期性失败；紧急度：warning。查询
`increase(reaper_cycles_total{outcome="error"}[1h])`。先看最近周期错误类型，再按
`StratumReaperDeleteErrors` 路径处理。恢复后 error 周期归零。

<a id="component-stale"></a>

## StratumComponentStale

影响：chat-cleanup/checkpoint-cleanup 超过 48 小时未运行；紧急度：warning。查询
`component_last_cycle_timestamp_seconds{component=~"chat-cleanup|checkpoint-cleanup"}`。先确认
Pod 与日志，再查组件注册是否被配置关闭；恢复后时间戳刷新。

<a id="component-error-rate"></a>

## StratumComponentErrorRate

影响：后台组件 1 小时内错误超过 5 次；紧急度：warning。查询
`increase(component_errors_total[1h])`。按 component/phase 定位并查对应日志；修复后计数停止增长。

<a id="goroutine-panic"></a>

## StratumGoroutinePanic

影响：已恢复的 goroutine panic；紧急度：warning。查询
`increase(goroutine_panics_total[10m])`。先按 component 与日志栈定位，再评估是否影响数据一致性；
修复后发布，观察 10 分钟窗口不再新增。

<a id="goroutine-panic-critical"></a>

## StratumGoroutinePanicCritical

影响：panic 风暴；紧急度：critical。查询 1 小时累计并立即升级。若与发布相关先回滚 revision，
再按栈修复。恢复后 1 小时窗口无新增且 resolved 已送达。

<a id="workflow-run-errors"></a>

## StratumWorkflowRunErrors

影响：工作流运行出错；紧急度：warning。查询
`increase(workflow_runs_total{status="error"}[10m])`。按 tenant 与运行详情定位失败节点；
修复或重跑后确认无新增错误。

<a id="workflow-error-rate"></a>

## StratumWorkflowErrorRate

影响：30 分钟累计 20 次以上工作流错误；紧急度：critical。立即冻结发布并升级；确认新 revision
相关性后回滚。恢复后错误计数与成功率同时回到正常。

<a id="mcp-client-errors"></a>

## StratumMCPClientErrors

影响：后端到 MCP server 调用错误；紧急度：warning。查询
`increase(mcp_client_requests_total{status="error"}[10m])`。按 server_name/operation 定位；
检查 MCP server 健康与配置，恢复后计数停止增长。

<a id="mcp-client-reconnects"></a>

## StratumMCPClientReconnects

影响：MCP 客户端频繁重连；紧急度：warning。查询
`increase(mcp_client_reconnects_total[1h])`。检查 server 就绪与网络策略；修复后重连计数停止增长。

<a id="auth-failures"></a>

## StratumAuthFailures

影响：认证失败增多；紧急度：warning。查询
`increase(auth_failures_total[10m])`。按 reason 分类：token 过期属正常波动，凭据/签名错误需立即
排查密钥轮换；不输出 token 或密钥内容。

<a id="knowledge-ingest-failures"></a>

## StratumKnowledgeIngestFailures

影响：知识入库失败；紧急度：warning。查询
`increase(knowledge_ingest_total{status=~"failed|error"}[30m])`。检查 chunk/embed/写入链路与依赖
（Milvus/LLM），修复后重跑失败任务并确认 ingest 计数恢复。

<a id="hermes-errors"></a>

## StratumHermesErrors

影响：Hermes 事件处理失败；紧急度：warning。查询
`increase(hermes_events_processed_total{status=~"publish_error|handler_error|unmarshal_error"}[10m])`。
按 event_type/status 定位 handler 或反序列化问题；修复后计数停止增长。

<a id="memory-pipeline-panics"></a>

## StratumMemoryPipelinePanics

影响：Memory pipeline panic；紧急度：warning。查询
`increase(memory_pipeline_panics_total[10m])`。按 component 与日志栈定位，评估消息是否进入 DLQ；
修复后发布，确认 10 分钟窗口无新增。

<a id="memory-dlq"></a>

## StratumMemoryDLQ

影响：Memory 消息进入死信队列；紧急度：warning。查询
`increase(memory_dlq_total[1h])`。按 tenant/stage 定位失败阶段，修复后确认 DLQ 不再增长并处理积压。

<a id="memory-dlq-critical"></a>

## StratumMemoryDLQCritical

影响：DLQ 大量堆积；紧急度：critical。立即升级 Memory owner；确认消费链路与存储可用性，
受控处理积压（保留审计），恢复后 DLQ 计数回落。

<a id="memory-worker-panics"></a>

## StratumMemoryWorkerPanics

影响：Memory worker panic；紧急度：warning。查询
`increase(memory_worker_panics_total[10m])`。按 worker 定位日志栈；修复后发布并确认无新增。

<a id="memory-worker-error-rate"></a>

## StratumMemoryWorkerErrorRate

影响：Memory worker 错误率超过 10%；紧急度：warning。查询
`rate(memory_worker_messages_total{status="error"}[30m])`。按 worker/tenant 定位；
修复后错误率回落且消息吞吐正常。

<a id="evaluation-job-errors"></a>

## StratumEvaluationJobErrors

影响：评测任务错误；紧急度：warning。查询
`increase(evaluation_jobs_total{status=~"error|list_error"}[10m])`。按 status 定位评测链路；
修复后重跑失败任务并确认计数停止增长。
```

- [ ] **Step 3: 更新 `stratum-rules.test.yaml`**

在 `rule_files:` 列表末尾（`- ../rules/stratum-monitoring.yaml` 之后）追加：

```yaml
  - ../rules/stratum-ai.yaml
```

在文件末尾追加测试块：

```yaml
  - name: reaper down fires when the last cycle is stale
    interval: 1m
    input_series:
      - series: 'reaper_last_cycle_timestamp_seconds{instance="10.0.0.1:8080",pod="stratum-abc"}'
        values: '100x180'
    alert_rule_test:
      - eval_time: 3h
        alertname: StratumReaperDown
        exp_alerts:
          - exp_labels:
              severity: warning
              service: stratum
              environment: remote-test
              component: reaper
              instance: 10.0.0.1:8080
              pod: stratum-abc
            exp_annotations:
              summary: Guest reaper has not run for over two hours
              description: The guest reaper last cycle is more than 7200s ago (current value 10700).
              dashboard_url: /d/stratum-service-overview
              runbook_url: /docs/operations/alerts/business.md#reaper-down

  - name: reaper down does not fire on a fresh cycle
    interval: 1m
    input_series:
      - series: 'reaper_last_cycle_timestamp_seconds{instance="10.0.0.1:8080",pod="stratum-abc"}'
        values: '3000x60'
    alert_rule_test:
      - eval_time: 1h
        alertname: StratumReaperDown
        exp_alerts: []

  - name: goroutine panic alert fires
    interval: 1m
    input_series:
      - series: 'goroutine_panics_total{component="memory-worker"}'
        values: '0+1x7'
    alert_rule_test:
      - eval_time: 6m
        alertname: StratumGoroutinePanic
        exp_alerts:
          - exp_labels:
              severity: warning
              service: stratum
              environment: remote-test
              component: panic
            exp_annotations:
              summary: Recovered goroutine panic in the last ten minutes
              description: Component memory-worker panicked in the last ten minutes (increase 6).
              dashboard_url: /d/stratum-service-overview
              runbook_url: /docs/operations/alerts/business.md#goroutine-panic

  - name: workflow run error alert fires
    interval: 1m
    input_series:
      - series: 'workflow_runs_total{tenant_id="tenant-1",status="error"}'
        values: '0+1x7'
    alert_rule_test:
      - eval_time: 6m
        alertname: StratumWorkflowRunErrors
        exp_alerts:
          - exp_labels:
              severity: warning
              service: stratum
              environment: remote-test
              component: workflow
              tenant_id: tenant-1
            exp_annotations:
              summary: Workflow run error in the last ten minutes
              description: Tenant tenant-1 had workflow run errors in the last ten minutes (increase 6).
              dashboard_url: /d/stratum-service-overview
              runbook_url: /docs/operations/alerts/business.md#workflow-run-errors
```

注意：`StratumGoroutinePanic` 的 alert labels 会包含输入 series 的 `component` label，而规则自身
labels 也有 `component: panic`；Prometheus 合并时以 alert 自有 label 为准，因此期望 label 只保留
`component: panic`。若 promtool 输出与预期不一致，以 promtool 实际输出为准修正期望值，但必须保持
`exp_alerts` 非空断言“确实触发”。

- [ ] **Step 4: 删除 helm chart 门控副本**

删除 `helm/templates/stratum-prometheusrule.yaml`（整个文件）。

- [ ] **Step 5: 验证渲染与守卫**

```bash
cd /home/yang/go-projects/stratum-monitoring-recovery
helm template stratum ./helm -f helm/values-demo.yaml -f helm/values-demo-remote-http.yaml -n stratum | grep -c 'name: stratum-ai'
```

Expected: 输出 `0`（不再生成 stratum-ai PrometheusRule）。

```bash
make helm-lint
PATH="/tmp:$PATH" bash scripts/quality/monitoring-config-test.sh
```

Expected: 全部通过（新规则文件通过 promtool check/test rules 与 runbook anchor 契约）。

- [ ] **Step 6: 提交**

```bash
git -C /home/yang/go-projects/stratum-monitoring-recovery add monitoring/remote/rules/stratum-ai.yaml docs/operations/alerts/business.md monitoring/remote/tests/stratum-rules.test.yaml helm/templates/stratum-prometheusrule.yaml
git -C /home/yang/go-projects/stratum-monitoring-recovery commit -m "[refactor](monitoring): migrate stratum-ai rules to remote monitoring authority"
```

---

### Task 4: release receipt v2（prior_digests + rollback_check=pending）

**Files:**

- Modify: `.github/workflows/deploy.yml`
- Modify: `.test/schemas/release-verification.schema.json`
- Modify: `scripts/quality/schema-test/authority_schemas_test.go`
- Modify: `scripts/quality/release-verification-test.sh`

- [ ] **Step 1: 更新 schema 到 v2**

将 `.test/schemas/release-verification.schema.json` 整体替换为：

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://stratum.local/schemas/release-verification.schema.json",
  "type": "object",
  "additionalProperties": false,
  "required": [
    "version",
    "status",
    "commit",
    "images",
    "prior_digests",
    "rollback_basis",
    "migration_check",
    "health_check",
    "rollback_check"
  ],
  "properties": {
    "version": { "const": 2 },
    "status": { "enum": ["deployable", "blocked", "deployed"] },
    "commit": { "type": "string", "pattern": "^[0-9a-f]{40}$" },
    "images": {
      "type": "object",
      "additionalProperties": false,
      "required": ["backend", "frontend", "platform_mcp", "feishu_adapter"],
      "properties": {
        "backend": { "$ref": "#/$defs/image" },
        "frontend": { "$ref": "#/$defs/image" },
        "platform_mcp": { "$ref": "#/$defs/image" },
        "feishu_adapter": { "$ref": "#/$defs/image" }
      }
    },
    "prior_digests": {
      "type": "object",
      "additionalProperties": false,
      "required": ["backend", "frontend", "platform_mcp", "feishu_adapter"],
      "properties": {
        "backend": { "$ref": "#/$defs/priorImage" },
        "frontend": { "$ref": "#/$defs/priorImage" },
        "platform_mcp": { "$ref": "#/$defs/priorImage" },
        "feishu_adapter": { "$ref": "#/$defs/priorImage" }
      }
    },
    "rollback_basis": { "enum": ["prior_digests_preserved", "first_deploy"] },
    "migration_check": { "$ref": "#/$defs/checkStatus" },
    "health_check": { "$ref": "#/$defs/checkStatus" },
    "rollback_check": { "$ref": "#/$defs/checkStatus" }
  },
  "$defs": {
    "image": { "type": "string", "pattern": "^[^[:space:]]+@sha256:[0-9a-f]{64}$" },
    "priorImage": { "type": "string", "pattern": "^(none|[^[:space:]]+@sha256:[0-9a-f]{64})$" },
    "checkStatus": { "enum": ["passed", "failed", "pending"] }
  }
}
```

- [ ] **Step 2: deploy.yml 增加 prior digests 捕获**

在 `- name: Helm deploy` 之前插入：

```yaml
      - name: Capture prior deployment digests
        id: prior-digests
        run: |
          capture_prior() {
            kubectl get deployment "$1" -n "$2" -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || true
          }
          prior_backend="$(capture_prior stratum stratum)"
          prior_frontend="$(capture_prior stratum-frontend stratum)"
          prior_platform_mcp="$(capture_prior stratum-platform-mcp stratum)"
          prior_feishu_adapter="$(capture_prior stratum-feishu-alert-adapter monitoring)"
          jq -n --arg backend "${prior_backend:-none}" --arg frontend "${prior_frontend:-none}" \
            --arg platform_mcp "${prior_platform_mcp:-none}" --arg feishu_adapter "${prior_feishu_adapter:-none}" \
            '{backend:$backend,frontend:$frontend,platform_mcp:$platform_mcp,feishu_adapter:$feishu_adapter}' \
            > /tmp/prior-digests.json
          jq -e . /tmp/prior-digests.json >/dev/null
```

- [ ] **Step 3: 更新 receipt 步骤为 v2**

将 `Record deployed application digests` 步骤中的 receipt 生成段替换为：

```yaml
          migration=passed
          health=passed
          rollback=pending
          prior_digests="$(cat /tmp/prior-digests.json)"
          prior_count="$(jq -r '[.backend,.frontend,.platform_mcp,.feishu_adapter] | map(select(. != "none")) | length' /tmp/prior-digests.json)"
          if [[ "$prior_count" -gt 0 ]]; then
            rollback_basis="prior_digests_preserved"
          else
            rollback_basis="first_deploy"
          fi
          jq -n --arg commit "${{ needs.candidate.outputs.sha }}" --arg backend "$backend" --arg frontend "$frontend" \
            --arg platform_mcp "$platform_mcp" --arg feishu_adapter "$feishu_adapter" \
            --argjson prior_digests "$prior_digests" --arg rollback_basis "$rollback_basis" \
            --arg migration "$migration" --arg health "$health" --arg rollback "$rollback" \
            '{version:2,status:"deployed",commit:$commit,
              images:{backend:$backend,frontend:$frontend,platform_mcp:$platform_mcp,feishu_adapter:$feishu_adapter},
              prior_digests:$prior_digests,rollback_basis:$rollback_basis,
              migration_check:$migration,health_check:$health,rollback_check:$rollback}' \
            > tmp/deployment-evidence/deployment-receipt.json
```

原 `migration=passed` / `health=passed` / `rollback=passed` 三行删除，改为 `rollback=pending`。

- [ ] **Step 4: 更新 schema fixture 测试**

将 `scripts/quality/schema-test/authority_schemas_test.go` 中 `releaseReport()` 替换为：

```go
func releaseReport() map[string]any {
 digest := "registry/stratum@sha256:" + hex(64)
 return map[string]any{
  "version": 2, "status": "deployed", "commit": hex(40),
  "images": map[string]any{
   "backend": digest, "frontend": digest, "platform_mcp": digest, "feishu_adapter": digest,
  },
  "prior_digests": map[string]any{
   "backend": "none", "frontend": "none", "platform_mcp": "none", "feishu_adapter": "none",
  },
  "rollback_basis": "first_deploy",
  "migration_check": "passed", "health_check": "passed", "rollback_check": "pending",
 }
}
```

在 `tests` 列表追加两个用例，并在 `changedReleaseImage` 之后新增辅助函数：

```go
  {name: "rejects release receipt missing prior_digests", schema: release,
   value: withoutKey(releaseReport(), "prior_digests"), wantErr: true},
  {name: "rejects release receipt with unknown rollback basis", schema: release,
   value: changed(releaseReport(), "rollback_basis", "unknown"), wantErr: true},
```

```go
func withoutKey(src map[string]any, key string) map[string]any {
 dst := clone(src)
 delete(dst, key)
 return dst
}
```

- [ ] **Step 5: 更新 release-verification-test.sh**

在 `require 'rollback_check:\$rollback'` 行之后追加：

```bash
require 'prior_digests:' 'release receipt does not record prior digests'
require 'rollback_basis:\$rollback_basis' 'release receipt does not record rollback basis'
```

- [ ] **Step 6: 运行相关测试**

```bash
cd /home/yang/go-projects/stratum-monitoring-recovery
go test ./scripts/quality/schema-test/ -count=1
bash scripts/quality/release-verification-test.sh
```

Expected: schema 测试 PASS；release verification workflow contract PASS。

- [ ] **Step 7: 提交**

```bash
git -C /home/yang/go-projects/stratum-monitoring-recovery add .github/workflows/deploy.yml .test/schemas/release-verification.schema.json scripts/quality/schema-test/authority_schemas_test.go scripts/quality/release-verification-test.sh
git -C /home/yang/go-projects/stratum-monitoring-recovery commit -m "[fix](ci): record prior digests and honest rollback status in release receipt"
```

---

### Task 5: PR1 全量验证

- [ ] **Step 1: 运行 Go 与守卫**

```bash
cd /home/yang/go-projects/stratum-monitoring-recovery
go vet ./...
go test -short ./...
make code-quality
make risk-guardrails
PATH="/tmp:$PATH" bash scripts/quality/monitoring-config-test.sh
```

Expected: 全部通过。任何失败先按证据修复，禁止跳过或降级。

- [ ] **Step 2: 运行仓库验证门禁**

```bash
cd /home/yang/go-projects/stratum-monitoring-recovery
bash scripts/quality/risk-regression-guard.sh --explain
make test-verify-before-pr
```

Expected: R3 短测 + soak 通过，无 failed/skipped/unreconciled capability。

- [ ] **Step 3: 确认改动清单**

```bash
git -C /home/yang/go-projects/stratum-monitoring-recovery log --oneline origin/main..HEAD
git -C /home/yang/go-projects/stratum-monitoring-recovery diff --stat origin/main..HEAD
```

Expected: 4 个提交，改动文件与任务一致。

- [ ] **Step 4: 推送并创建 PR（需用户确认后执行）**

```bash
git -C /home/yang/go-projects/stratum-monitoring-recovery push -u origin feat/monitoring-recovery
gh pr create --base main \
  --title "[fix](monitoring): reaper metrics, rule migration, crash alert, honest rollback receipt" \
  --body "What: 注册 guest reaper 四个缺失指标并加全方法冒烟测试；新增 StratumPodUnhealthyExit 低频崩溃告警；stratum-ai 21 条规则迁入 monitoring/remote 并补 runbook/标签/promtool 测试；release receipt 升级 v2（prior_digests + rollback_check=pending）。\nWhy: 修复线上每小时 nil panic；消除低频崩溃漏报与规则守卫缺失；回滚证据诚实化。\nHowToTest: go test ./pkg/observability/；bash scripts/quality/monitoring-config-test.sh；make helm-lint；go test ./scripts/quality/schema-test/；bash scripts/quality/release-verification-test.sh；make test-verify-before-pr。"
```

PR 描述包含 What/Why/HowToTest；远端部署待 CI 全绿后另行征求用户许可。
