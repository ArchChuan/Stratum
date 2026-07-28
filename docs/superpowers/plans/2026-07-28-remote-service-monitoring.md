# Remote Test Service Monitoring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the remote K3s test service continuously observable and deliver actionable traditional service
performance, stability, capacity, and availability alerts to Feishu from reproducible Git-managed configuration.

**Architecture:** Keep the existing `kube-prometheus-stack` release as the control plane, make its chart version and
values authoritative in Git, add a Stratum `ServiceMonitor`, Blackbox public-path probing, tested `PrometheusRule`
resources, Grafana dashboards, and a bounded Go Alertmanager-to-Feishu adapter. Add a scheduled GitHub Actions probe
as an external fallback because cluster-local monitoring cannot detect total host loss.

**Tech Stack:** Go 1.25.12, Prometheus Operator / kube-prometheus-stack 87.10.1, Prometheus Blackbox Exporter chart
11.15.1, Prometheus rule unit tests, Alertmanager, Grafana provisioning, Helm 3, Kubernetes, GitHub Actions.

---

## Baseline and Execution Rules

- Work only in `/home/yang/go-projects/stratum-remote-monitoring` on branch `feat/remote-monitoring`.
- Run `bash scripts/quality/risk-regression-guard.sh --explain` before implementation.
- The baseline `stratum-verify go-test` currently fails in
  `TestSystemAssistantHTTPContractsUseRealHandlerServiceAndPostgres`: commit `10aec7c` changed the managed system
  assistant to prefer database fields while the E2E still rejects `name="tampered"`. Do not modify that unrelated
  contract in this branch. Re-run the suite at completion and report this baseline separately if it remains.
- Never print or render `FEISHU_WEBHOOK_URL`. Tests use `httptest.Server` URLs or fixed redacted sentinels.
- Before changing the remote `kps` release, capture its values, manifests, CR inventory, PVCs, dashboards, datasources,
  and unmanaged resources into a local secure temporary directory. Do not commit secrets or raw runtime dumps.
- Use exact chart pins. Do not use `latest`, a floating chart range, or tag-only custom runtime images in the remote
  deployment.
- Each task ends with the focused tests shown below and a conventional commit.

## File Map

### Application Helm contract

- `helm/templates/servicemonitor.yaml`: operator scrape contract for the backend `/metrics` endpoint.
- `helm/values.yaml`: disabled-by-default ServiceMonitor options.
- `helm/values-demo.yaml`: enables the remote scrape contract and selects the `kps` release.
- `scripts/quality/check-helm-image-rendering-test.sh`: asserts labels, path, interval, and timeout render correctly.

### Monitoring GitOps configuration

- `monitoring/remote/versions.env`: exact chart versions and release names consumed by validation and deployment.
- `monitoring/remote/kube-prometheus-stack-values.yaml`: Prometheus, Alertmanager, Grafana, retention, selectors, and
  standard Kubernetes rule settings.
- `monitoring/remote/blackbox-exporter-values.yaml`: public health probe module and ServiceMonitor.
- `monitoring/remote/rules/*.yaml`: Stratum-specific PrometheusRule groups.
- `monitoring/remote/tests/stratum-rules.test.yaml`: deterministic PromQL rule tests.
- `monitoring/remote/alertmanager/alertmanager.yaml`: grouping, inhibition, time routing, and adapter receiver.
- `monitoring/remote/dashboards/*.json`: service, HTTP, resources, and dependency dashboards.
- `monitoring/remote/resources/*.yaml`: adapter, supported dependency monitors, and dashboard ConfigMaps.

### Feishu adapter

- `internal/platform/alerting/model.go`: bounded Alertmanager webhook input and Feishu card output contracts.
- `internal/platform/alerting/render.go`: allowlisted message rendering.
- `internal/platform/alerting/delivery.go`: delivery budget and retry classification.
- `internal/platform/alerting/handler.go`: body limit, decoding, status mapping, and metrics.
- `internal/platform/alerting/metrics.go`: adapter Prometheus metrics.
- `internal/platform/alerting/*_test.go`: contract, security, retry, timeout, and metric tests.
- `cmd/feishu-alert-adapter/main.go`: configuration, HTTP lifecycle, graceful shutdown, and readiness.
- `docker/feishu-alert-adapter.Dockerfile`: non-root static adapter image.

### Operations and automation

- `scripts/quality/monitoring-config-test.sh`: hermetic validation entrypoint.
- `scripts/quality/monitoring-config-test-test.sh`: proves validation failures propagate.
- `scripts/deploy-remote-monitoring.sh`: inventory, atomic Helm upgrade, resources, readiness, and safe smoke checks.
- `scripts/deploy-remote-monitoring-test.sh`: shell contract tests with fake `helm` and `kubectl`.
- `.github/workflows/deploy.yml`: builds the adapter, creates the secret safely, and invokes the monitoring deploy.
- `.github/workflows/remote-health-monitor.yml`: independent public probe and Feishu failure/recovery delivery.
- `cmd/remote-health-monitor/main.go`: external probe with GitHub Issue state and Feishu messages.
- `cmd/remote-health-monitor/main_test.go`: probe, state transition, and redaction tests.
- `docs/operations/remote-monitoring-runbook.md`: stack operations and notification testing.
- `docs/operations/alerts/*.md`: one actionable Chinese runbook per notifying alert family.

## Task 1: Add a Failing ServiceMonitor Rendering Contract

**Files:**

- Modify: `scripts/quality/check-helm-image-rendering-test.sh`
- Create: `helm/templates/servicemonitor.yaml`
- Modify: `helm/values.yaml`
- Modify: `helm/values-demo.yaml`

- [ ] **Step 1: Add the failing render assertions**

Append a dedicated render output and assertions to `scripts/quality/check-helm-image-rendering-test.sh`:

```bash
SERVICE_MONITOR_RENDER="${TMP_ROOT}/service-monitor.yaml"

helm template stratum "${ROOT}/helm" \
    -f "${ROOT}/helm/values-demo.yaml" >"${SERVICE_MONITOR_RENDER}"

grep -Fq 'kind: ServiceMonitor' "${SERVICE_MONITOR_RENDER}"
grep -Fq 'release: kps' "${SERVICE_MONITOR_RENDER}"
grep -Fq 'path: /metrics' "${SERVICE_MONITOR_RENDER}"
grep -Fq 'interval: 30s' "${SERVICE_MONITOR_RENDER}"
grep -Fq 'scrapeTimeout: 10s' "${SERVICE_MONITOR_RENDER}"
```

- [ ] **Step 2: Run the contract and verify RED**

Run:

```bash
bash scripts/quality/check-helm-image-rendering-test.sh
```

Expected: FAIL because the rendered chart has no `ServiceMonitor`.

- [ ] **Step 3: Add disabled-by-default values**

Replace the empty `serviceMonitor` object in `helm/values.yaml` with:

```yaml
serviceMonitor:
  enabled: false
  interval: 30s
  scrapeTimeout: 10s
  path: /metrics
  additionalLabels: {}
```

Set this in `helm/values-demo.yaml`:

```yaml
serviceMonitor:
  enabled: true
  interval: 30s
  scrapeTimeout: 10s
  path: /metrics
  additionalLabels:
    release: kps
```

- [ ] **Step 4: Add the template**

Create `helm/templates/servicemonitor.yaml`:

```yaml
{{- if .Values.serviceMonitor.enabled }}
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: {{ include "stratum.fullname" . }}
  labels:
    {{- include "stratum.labels" . | nindent 4 }}
    {{- with .Values.serviceMonitor.additionalLabels }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
spec:
  selector:
    matchLabels:
      {{- include "stratum.selectorLabels" . | nindent 6 }}
  namespaceSelector:
    matchNames:
      - {{ .Release.Namespace }}
  endpoints:
    - port: http
      path: {{ .Values.serviceMonitor.path | quote }}
      interval: {{ .Values.serviceMonitor.interval | quote }}
      scrapeTimeout: {{ .Values.serviceMonitor.scrapeTimeout | quote }}
      honorLabels: false
{{- end }}
```

- [ ] **Step 5: Verify GREEN**

Run:

```bash
bash scripts/quality/check-helm-image-rendering-test.sh
```

Expected: `Helm image rendering tests passed`.

- [ ] **Step 6: Commit**

```bash
git add helm/templates/servicemonitor.yaml helm/values.yaml helm/values-demo.yaml \
  scripts/quality/check-helm-image-rendering-test.sh
git commit -m '[feat](monitoring): expose backend scrape contract'
```

## Task 2: Establish Monitoring Version Pins and Validation Guardrails

**Files:**

- Create: `monitoring/remote/versions.env`
- Create: `monitoring/remote/kube-prometheus-stack-values.yaml`
- Create: `monitoring/remote/blackbox-exporter-values.yaml`
- Create: `scripts/quality/monitoring-config-test.sh`
- Create: `scripts/quality/monitoring-config-test-test.sh`
- Modify: `Makefile`

- [ ] **Step 1: Write the validation self-test first**

Create `scripts/quality/monitoring-config-test-test.sh` using the repository's temp-directory pattern. It must create
fake `helm`, `promtool`, and `amtool` binaries, record invocations, and prove that each non-zero fake result makes the
validator fail. Its core assertion helper is:

```bash
assert_fails() {
    local failing_tool="$1"
    if FAIL_TOOL="${failing_tool}" PATH="${FAKE_BIN}:${PATH}" \
        bash "${VALIDATOR}" >/dev/null 2>&1; then
        echo "validator swallowed ${failing_tool} failure" >&2
        exit 1
    fi
}

assert_fails helm
assert_fails promtool
assert_fails amtool
```

- [ ] **Step 2: Run the self-test and verify RED**

Run:

```bash
bash scripts/quality/monitoring-config-test-test.sh
```

Expected: FAIL because `scripts/quality/monitoring-config-test.sh` does not exist.

- [ ] **Step 3: Add exact version pins**

Create `monitoring/remote/versions.env`:

```bash
KUBE_PROMETHEUS_STACK_CHART_VERSION=87.10.1
KUBE_PROMETHEUS_STACK_RELEASE=kps
BLACKBOX_EXPORTER_CHART_VERSION=11.15.1
BLACKBOX_EXPORTER_RELEASE=stratum-blackbox
MONITORING_NAMESPACE=monitoring
```

- [ ] **Step 4: Add conservative stack values**

Create `monitoring/remote/kube-prometheus-stack-values.yaml` with these controlling values:

```yaml
grafana:
  defaultDashboardsEnabled: true
  sidecar:
    dashboards:
      enabled: true
      label: grafana_dashboard
      labelValue: "1"
      searchNamespace: ALL

alertmanager:
  enabled: true
  alertmanagerSpec:
    retention: 120h
    replicas: 1

prometheus:
  enabled: true
  prometheusSpec:
    retention: 15d
    retentionSize: 15GB
    replicas: 1
    serviceMonitorSelectorNilUsesHelmValues: false
    podMonitorSelectorNilUsesHelmValues: false
    ruleSelectorNilUsesHelmValues: false
    resources:
      requests: {cpu: 250m, memory: 768Mi}
      limits: {cpu: "1", memory: 2Gi}
    storageSpec:
      volumeClaimTemplate:
        spec:
          storageClassName: local-path
          accessModes: [ReadWriteOnce]
          resources:
            requests:
              storage: 20Gi

kubeEtcd:
  enabled: true
kubeControllerManager:
  enabled: false
kubeScheduler:
  enabled: false
```

K3s controller-manager and scheduler metrics are disabled until their bind addresses and TLS access are explicitly
verified. Do not silence their target-down alerts while pretending the targets exist.

- [ ] **Step 5: Add the Blackbox values**

Create `monitoring/remote/blackbox-exporter-values.yaml`:

```yaml
config:
  modules:
    stratum_http_health:
      prober: http
      timeout: 8s
      http:
        method: GET
        preferred_ip_protocol: ip4
        follow_redirects: false
        valid_status_codes: [200]
        fail_if_body_not_matches_regexp:
          - '"service"[[:space:]]*:[[:space:]]*"Stratum"'
          - '"status"[[:space:]]*:[[:space:]]*"ok"'

serviceMonitor:
  enabled: true
  defaults:
    labels:
      release: kps
    interval: 30s
    scrapeTimeout: 10s
  targets:
    - name: stratum-public-health
      url: http://101.200.181.141:6879/api/health
      module: stratum_http_health
      additionalLabels:
        environment: remote-test
        service: stratum
```

- [ ] **Step 6: Implement the validator**

Create `scripts/quality/monitoring-config-test.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck disable=SC1091
source "${ROOT}/monitoring/remote/versions.env"

helm template kps prometheus-community/kube-prometheus-stack \
  --version "${KUBE_PROMETHEUS_STACK_CHART_VERSION}" \
  --namespace "${MONITORING_NAMESPACE}" \
  -f "${ROOT}/monitoring/remote/kube-prometheus-stack-values.yaml" >/dev/null
helm template stratum-blackbox prometheus-community/prometheus-blackbox-exporter \
  --version "${BLACKBOX_EXPORTER_CHART_VERSION}" \
  --namespace "${MONITORING_NAMESPACE}" \
  -f "${ROOT}/monitoring/remote/blackbox-exporter-values.yaml" >/dev/null

find "${ROOT}/monitoring/remote/rules" -type f -name '*.yaml' -print0 2>/dev/null | \
  xargs -0 -r promtool check rules
promtool test rules "${ROOT}/monitoring/remote/tests/stratum-rules.test.yaml"
amtool check-config "${ROOT}/monitoring/remote/alertmanager/alertmanager.yaml"

find "${ROOT}/monitoring/remote/dashboards" -type f -name '*.json' -print0 2>/dev/null | \
  xargs -0 -r -n1 jq -e . >/dev/null
```

- [ ] **Step 7: Add the Make target and verify**

Add `monitoring-config-test` to `.PHONY` and:

```make
monitoring-config-test:
 bash scripts/quality/monitoring-config-test-test.sh
 bash scripts/quality/monitoring-config-test.sh
```

Run:

```bash
bash scripts/quality/monitoring-config-test-test.sh
```

Expected: PASS with all fake failure paths detected. The real validator may report missing local binaries; execute it
inside the pinned validation container in Task 9.

- [ ] **Step 8: Commit**

```bash
git add monitoring/remote Makefile scripts/quality/monitoring-config-test*.sh
git commit -m '[test](monitoring): add configuration guardrails'
```

## Task 3: Implement the Feishu Message Contract with TDD

**Files:**

- Create: `internal/platform/alerting/model.go`
- Create: `internal/platform/alerting/render.go`
- Create: `internal/platform/alerting/render_test.go`

- [ ] **Step 1: Write failing rendering tests**

Create tests that unmarshal a grouped Alertmanager payload, render a Feishu interactive card, and assert:

```go
func TestRenderCardUsesAllowlistedFieldsAndStatus(t *testing.T) {
 group := AlertGroup{
  Status: "firing",
  CommonLabels: map[string]string{"alertname": "StratumPublicEndpointDown", "severity": "critical"},
  CommonAnnotations: map[string]string{
   "summary": "远端入口不可用", "runbook_url": "https://example.invalid/runbook",
  },
  Alerts: []Alert{{Status: "firing", Labels: map[string]string{"service": "stratum"}}},
 }
 card, err := RenderCard(group)
 require.NoError(t, err)
 require.Equal(t, "interactive", card.MsgType)
 require.Contains(t, card.Card.Header.Title.Content, "FIRING")
 require.NotContains(t, mustJSON(t, card), "token")
}

func TestRenderCardRejectsUnsupportedStatus(t *testing.T) {
 _, err := RenderCard(AlertGroup{Status: "unknown"})
 require.ErrorIs(t, err, ErrInvalidAlertGroup)
}
```

- [ ] **Step 2: Run and verify RED**

Run:

```bash
go test ./internal/platform/alerting -run 'TestRenderCard' -count=1
```

Expected: compile failure because the package does not exist.

- [ ] **Step 3: Implement bounded input and output models**

Define only these Alertmanager fields in `model.go`: `status`, `commonLabels`, `commonAnnotations`, and alert
`status/labels/annotations/startsAt/endsAt`. Define Feishu `msg_type`, card header, and text/markdown elements. Add:

```go
var ErrInvalidAlertGroup = errors.New("invalid alert group")

const (
 maxAlertsPerMessage = 20
 maxFieldRunes       = 500
)
```

- [ ] **Step 4: Implement allowlisted rendering**

`RenderCard` accepts only `firing` and `resolved`, truncates text by runes, renders no raw map, and reads only:

```go
var allowedLabelKeys = []string{"alertname", "severity", "service", "environment", "component", "instance"}
var allowedAnnotationKeys = []string{"summary", "description", "dashboard_url", "runbook_url"}
```

Use red header color for firing and green for resolved. Escape user-controlled text before including it in card
markdown.

- [ ] **Step 5: Verify GREEN**

Run:

```bash
go test ./internal/platform/alerting -run 'TestRenderCard' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/platform/alerting
git commit -m '[feat](monitoring): render bounded Feishu alerts'
```

## Task 4: Implement Bounded Feishu Delivery and HTTP Lifecycle

**Files:**

- Create: `internal/platform/alerting/delivery.go`
- Create: `internal/platform/alerting/delivery_test.go`
- Create: `internal/platform/alerting/handler.go`
- Create: `internal/platform/alerting/handler_test.go`
- Create: `internal/platform/alerting/metrics.go`
- Create: `cmd/feishu-alert-adapter/main.go`

- [ ] **Step 1: Write retry-classification tests**

Cover successful 2xx, permanent 4xx, retryable 408/429/5xx, timeout, cancellation during backoff, body closure, and a
maximum of three attempts. Use an injected `httpclient.Doer` and sleeper so tests do not sleep.

```go
func TestDeliverStopsOnPermanentClientError(t *testing.T) {
 doer := &sequenceDoer{statuses: []int{http.StatusBadRequest, http.StatusOK}}
 d := NewDelivery(doer, noSleep)
 err := d.Send(context.Background(), "https://example.invalid/hook", FeishuMessage{})
 require.Error(t, err)
 require.Equal(t, 1, doer.calls)
}
```

- [ ] **Step 2: Run and verify RED**

Run:

```bash
go test ./internal/platform/alerting -run 'TestDeliver|TestHandler' -count=1
```

Expected: FAIL because delivery and handler are undefined.

- [ ] **Step 3: Implement delivery**

Use a total context budget of 10 seconds, three attempts, base delay 100 milliseconds, maximum delay 2 seconds, and
retry only transport failures, 408, 429, and 5xx. Bound and discard response bodies without logging them. Return an
error containing only the status code and attempt count.

- [ ] **Step 4: Implement handler and metrics**

The handler must:

```go
const maxWebhookBodyBytes = 1 << 20

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
 if r.Method != http.MethodPost {
  http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
  return
 }
 reader := http.MaxBytesReader(w, r.Body, maxWebhookBodyBytes)
 defer reader.Close()
 var group AlertGroup
 if err := json.NewDecoder(reader).Decode(&group); err != nil {
  http.Error(w, "invalid alert payload", http.StatusBadRequest)
  return
 }
 // Render, deliver, update metrics, and return 2xx only after successful delivery.
}
```

Expose `feishu_alert_delivery_total{status}`, `feishu_alert_delivery_duration_seconds`,
`feishu_alert_delivery_retries_total`, and `feishu_alert_requests_in_flight`. Do not label by alert text or webhook.

- [ ] **Step 5: Implement process lifecycle**

`cmd/feishu-alert-adapter/main.go` must require `FEISHU_WEBHOOK_URL`, serve `/alertmanager`, `/metrics`, `/livez`, and
`/readyz`, use `ReadHeaderTimeout=5s`, `ReadTimeout=15s`, `WriteTimeout=15s`, `IdleTimeout=60s`, and perform a
10-second graceful shutdown on SIGTERM/SIGINT. Startup errors are fatal; the webhook value is never logged.

- [ ] **Step 6: Verify focused tests and race safety**

Run:

```bash
go test -race ./internal/platform/alerting ./cmd/feishu-alert-adapter -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/platform/alerting cmd/feishu-alert-adapter
git commit -m '[feat](monitoring): add Feishu alert adapter'
```

## Task 5: Package and Monitor the Feishu Adapter

**Files:**

- Create: `docker/feishu-alert-adapter.Dockerfile`
- Create: `monitoring/remote/resources/feishu-alert-adapter.yaml`
- Modify: `.github/workflows/deploy.yml`
- Modify: `scripts/quality/check-deployment-safety-test.sh`

- [ ] **Step 1: Add failing deployment-safety assertions**

Require the workflow and manifest to contain:

```bash
require 'FEISHU_WEBHOOK_URL' 'Feishu secret injection missing'
require 'kubectl create secret generic stratum-monitoring-secrets' 'monitoring secret creation missing'
require_file "${ROOT}/monitoring/remote/resources/feishu-alert-adapter.yaml" \
  'readOnlyRootFilesystem: true' 'adapter filesystem hardening missing'
require_file "${ROOT}/monitoring/remote/resources/feishu-alert-adapter.yaml" \
  'runAsNonRoot: true' 'adapter non-root policy missing'
```

- [ ] **Step 2: Verify RED**

Run:

```bash
bash scripts/quality/check-deployment-safety-test.sh
```

Expected: FAIL on missing adapter deployment and secret injection.

- [ ] **Step 3: Add the pinned build image**

Create a multi-stage Dockerfile that builds only `./cmd/feishu-alert-adapter`, copies it into the same pinned Alpine
base policy used by the repository after resolving and recording the digest, runs as UID/GID 65532, and exposes 8080.
The final entrypoint is:

```dockerfile
ENTRYPOINT ["/feishu-alert-adapter"]
```

- [ ] **Step 4: Add hardened Kubernetes resources**

Create a Deployment, ClusterIP Service, and ServiceMonitor. The Deployment uses one replica, RollingUpdate,
`runAsNonRoot`, `readOnlyRootFilesystem`, dropped capabilities, seccomp RuntimeDefault, resource requests/limits,
HTTP probes, a 30-second termination grace period, and `FEISHU_WEBHOOK_URL` from
`monitoring/stratum-monitoring-secrets`. The ServiceMonitor carries `release: kps`.

- [ ] **Step 5: Build and push the adapter in CD**

Add adapter metadata/build steps beside the backend image, propagate its digest as a deploy output, create the Secret
with:

```bash
kubectl create secret generic stratum-monitoring-secrets \
  --namespace monitoring \
  --from-literal=FEISHU_WEBHOOK_URL="${{ secrets.FEISHU_WEBHOOK_URL }}" \
  --dry-run=client -o yaml | kubectl apply -f -
```

Patch the adapter manifest image with the immutable digest using `kustomize` or a checked `envsubst` input that
rejects non-`sha256:<64 hex>` values. Do not echo rendered secrets.

- [ ] **Step 6: Verify GREEN**

Run:

```bash
bash scripts/quality/check-deployment-safety-test.sh
go test -race ./internal/platform/alerting ./cmd/feishu-alert-adapter -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add docker/feishu-alert-adapter.Dockerfile monitoring/remote/resources/feishu-alert-adapter.yaml \
  .github/workflows/deploy.yml scripts/quality/check-deployment-safety-test.sh
git commit -m '[feat](monitoring): deploy hardened Feishu adapter'
```

## Task 6: Add Availability and HTTP Rules with PromQL Unit Tests

**Files:**

- Create: `monitoring/remote/rules/stratum-availability.yaml`
- Create: `monitoring/remote/rules/stratum-http.yaml`
- Create: `monitoring/remote/tests/stratum-rules.test.yaml`

- [ ] **Step 1: Write rule tests before rules**

Add synthetic series for `probe_success`, ready replicas, `http_requests_total`, and histogram buckets. Assert:

- public probe failure fires at 3 minutes and not at 2 minutes;
- 5xx ratio does not fire below 20 requests;
- warning fires above 5%, critical above 20%;
- health and metrics routes are excluded;
- P95 warning and critical thresholds behave at 2 and 5 seconds;
- resolved inputs produce no alerts.

Use exact expected labels:

```yaml
alert_rule_test:
  - eval_time: 4m
    alertname: StratumPublicEndpointDown
    exp_alerts:
      - exp_labels:
          severity: critical
          service: stratum
          environment: remote-test
```

- [ ] **Step 2: Verify RED**

Run:

```bash
promtool test rules monitoring/remote/tests/stratum-rules.test.yaml
```

Expected: FAIL because rule files do not exist.

- [ ] **Step 3: Implement availability rules**

Add `StratumPublicEndpointDown`, `StratumBackendUnavailable`, `StratumFrontendUnavailable`, and
`StratumTargetMissing`. Every rule includes dashboard and runbook annotations and a `for` duration from the design.

The public expression is:

```promql
probe_success{job="stratum-blackbox-prometheus-blackbox-exporter", service="stratum", environment="remote-test"} == 0
```

- [ ] **Step 4: Implement HTTP recording and alert rules**

Add recording rules for five-minute request volume, 5xx ratio, and P95 latency, excluding `/health`, `/livez`,
`/readyz`, and `/metrics`. Use `clamp_min` on denominators and an explicit `>= 20` volume predicate.

- [ ] **Step 5: Validate rules**

Run:

```bash
promtool check rules monitoring/remote/rules/stratum-availability.yaml \
  monitoring/remote/rules/stratum-http.yaml
promtool test rules monitoring/remote/tests/stratum-rules.test.yaml
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add monitoring/remote/rules monitoring/remote/tests
git commit -m '[feat](monitoring): alert on availability and HTTP RED signals'
```

## Task 7: Add Workload, Capacity, Dependency, and Monitoring Rules

**Files:**

- Create: `monitoring/remote/rules/stratum-workloads.yaml`
- Create: `monitoring/remote/rules/stratum-capacity.yaml`
- Create: `monitoring/remote/rules/stratum-dependencies.yaml`
- Create: `monitoring/remote/rules/stratum-monitoring.yaml`
- Modify: `monitoring/remote/tests/stratum-rules.test.yaml`
- Create: `monitoring/remote/resources/dependency-monitors.yaml`

- [ ] **Step 1: Add failing edge-case tests**

Test node-not-ready inhibition inputs, restart increase, memory availability, PVC thresholds, positive-growth
prediction, no prediction on flat/decreasing series, exporter disappearance, Prometheus rule failures, Alertmanager
notification failures, and adapter delivery failures.

- [ ] **Step 2: Verify RED**

Run:

```bash
promtool test rules monitoring/remote/tests/stratum-rules.test.yaml
```

Expected: FAIL because the new rule groups are absent.

- [ ] **Step 3: Add workload and capacity rules**

Prefer standard kube-prometheus recording rules where available. Custom rules are limited to Stratum namespace
workloads and the agreed thresholds. Prediction expressions require at least six hours of samples and a positive
`deriv` before using `predict_linear`.

- [ ] **Step 4: Add dependency collection only where verified**

Create Service/ServiceMonitor resources for native Prometheus endpoints that exist in the rendered deployments:
Milvus `/metrics`, etcd `/metrics`, and adapter `/metrics`. Add NATS, PostgreSQL, and Redis exporters only if their
least-privilege credential and pinned-image contracts can be rendered and tested. If MinIO cluster metrics require
authentication, use a dedicated read-only monitoring credential; do not make its metrics endpoint public.

Before writing each rule, run a fixture or remote read-only query to capture the actual metric name. Do not add rules
for guessed metric names. Record unsupported coverage explicitly in the runbook.

- [ ] **Step 5: Add monitoring-system rules**

Cover Prometheus configuration reload/rule evaluation failures, expected target loss, Alertmanager notification
failures, Blackbox disappearance, and:

```promql
increase(feishu_alert_delivery_total{status="failed"}[10m]) > 0
```

- [ ] **Step 6: Validate all rules**

Run:

```bash
promtool check rules monitoring/remote/rules/*.yaml
promtool test rules monitoring/remote/tests/stratum-rules.test.yaml
```

Expected: PASS with no skipped rule cases.

- [ ] **Step 7: Commit**

```bash
git add monitoring/remote/rules monitoring/remote/tests monitoring/remote/resources/dependency-monitors.yaml
git commit -m '[feat](monitoring): cover workloads capacity and dependencies'
```

## Task 8: Configure Alertmanager Routing, Inhibition, and Feishu Templates

**Files:**

- Create: `monitoring/remote/alertmanager/alertmanager.yaml`
- Create: `monitoring/remote/alertmanager/templates.tmpl`
- Create: `monitoring/remote/alertmanager/alertmanager-test.yaml`
- Modify: `monitoring/remote/kube-prometheus-stack-values.yaml`

- [ ] **Step 1: Add config contract fixtures**

Create a test fixture that checks these route outcomes with `amtool config routes test`:

- `critical` routes to `feishu-critical` at all times;
- `warning` routes to `feishu-warning` only in `cn-work-hours`;
- `info` routes to `null`;
- Watchdog routes to `null`;
- resolved notifications are enabled.

- [ ] **Step 2: Verify RED**

Run:

```bash
amtool check-config monitoring/remote/alertmanager/alertmanager.yaml
```

Expected: FAIL because the configuration does not exist.

- [ ] **Step 3: Implement routing and work hours**

Use:

```yaml
route:
  receiver: null
  group_by: [alertname, service, environment, severity]
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 4h
  routes:
    - receiver: null
      matchers: ['alertname="Watchdog"']
    - receiver: feishu-critical
      matchers: ['severity="critical"']
    - receiver: feishu-warning
      matchers: ['severity="warning"']
      active_time_intervals: [cn-work-hours]
    - receiver: null
      matchers: ['severity="info"']

time_intervals:
  - name: cn-work-hours
    time_intervals:
      - weekdays: [monday:friday]
        times: [{start_time: "09:00", end_time: "19:00"}]
        location: Asia/Shanghai
```

Both Feishu receivers POST to `http://stratum-feishu-alert-adapter.monitoring.svc:8080/alertmanager` and set
`send_resolved: true`.

- [ ] **Step 4: Implement inhibition**

Add public symptom over component causes, node down over Pod causes, deployment unavailable over replica/Pod causes,
and critical over warning for the same service/alert family. Preserve monitoring-system alerts even when Stratum is
down.

- [ ] **Step 5: Wire the config through kube-prometheus-stack values**

Use the chart-supported Alertmanager configuration field or a named Secret mounted through `alertmanagerSpec`.
Render the final chart and prove the config references only the adapter Service, never the Feishu URL.

- [ ] **Step 6: Validate GREEN**

Run:

```bash
amtool check-config monitoring/remote/alertmanager/alertmanager.yaml
amtool config routes test --config.file=monitoring/remote/alertmanager/alertmanager.yaml \
  severity=critical service=stratum environment=remote-test
```

Expected receiver: `feishu-critical`.

- [ ] **Step 7: Commit**

```bash
git add monitoring/remote/alertmanager monitoring/remote/kube-prometheus-stack-values.yaml
git commit -m '[feat](monitoring): route and inhibit Feishu alerts'
```

## Task 9: Provision Query-Tested Grafana Dashboards

**Files:**

- Create: `monitoring/remote/dashboards/stratum-service-overview.json`
- Create: `monitoring/remote/dashboards/stratum-http.json`
- Create: `monitoring/remote/dashboards/stratum-resources.json`
- Create: `monitoring/remote/dashboards/stratum-dependencies.json`
- Create: `monitoring/remote/resources/dashboards.yaml`
- Create: `scripts/quality/monitoring-dashboard-test.sh`

- [ ] **Step 1: Write failing dashboard contract tests**

The script must parse every JSON file, reject datasource UIDs other than `prometheus`, reject template variables that
can expose tenant/user IDs, and require each dashboard to contain a non-empty title, tags `stratum` and `remote-test`,
and links to the monitoring runbook.

- [ ] **Step 2: Verify RED**

Run:

```bash
bash scripts/quality/monitoring-dashboard-test.sh
```

Expected: FAIL because the dashboard files are absent.

- [ ] **Step 3: Add four focused dashboards**

Use stable dimensions and these core queries:

- overview: `probe_success`, request rate, 5xx ratio, P95, ready replicas, active alerts;
- HTTP: existing recording rules broken down only by bounded `path` templates;
- resources: standard node/container/PVC recording rules for the `stratum` namespace;
- dependencies: only metrics verified in Task 7, with an explicit text panel listing unsupported coverage.

Set `uid` values `stratum-remote-overview`, `stratum-remote-http`, `stratum-remote-resources`, and
`stratum-remote-dependencies`. Missing data remains null; do not coerce it to zero.

- [ ] **Step 4: Generate labeled ConfigMaps**

Create `monitoring/remote/resources/dashboards.yaml` with one ConfigMap per dashboard and:

```yaml
metadata:
  labels:
    grafana_dashboard: "1"
```

Generate the embedded JSON deterministically through a script or checked build step; do not maintain divergent JSON
copies manually.

- [ ] **Step 5: Verify GREEN**

Run:

```bash
bash scripts/quality/monitoring-dashboard-test.sh
jq -e . monitoring/remote/dashboards/*.json >/dev/null
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add monitoring/remote/dashboards monitoring/remote/resources/dashboards.yaml \
  scripts/quality/monitoring-dashboard-test.sh
git commit -m '[feat](monitoring): provision remote service dashboards'
```

## Task 10: Add an External Health Monitor with Durable State

**Files:**

- Create: `cmd/remote-health-monitor/main.go`
- Create: `cmd/remote-health-monitor/main_test.go`
- Create: `.github/workflows/remote-health-monitor.yml`

- [ ] **Step 1: Write state-transition tests**

Model the monitor state through a single GitHub Issue labeled `remote-health-monitor`. Test:

```text
healthy + no open issue       -> no message
failed three bounded attempts + no open issue -> create issue, send firing once
failed + open issue           -> update issue timestamp, no duplicate firing
healthy + open issue          -> close issue, send resolved once
GitHub state API failure      -> fail closed, do not claim recovery
Feishu delivery failure       -> exit non-zero and leave issue open
```

Use fake probe, GitHub, and Feishu interfaces; assert no webhook or health body appears in errors.

- [ ] **Step 2: Verify RED**

Run:

```bash
go test ./cmd/remote-health-monitor -count=1
```

Expected: compile failure because the command is absent.

- [ ] **Step 3: Implement bounded probing and state transitions**

Probe `REMOTE_HEALTH_URL` three times within a 30-second total budget, require HTTP 200 and decode exactly the stable
`status/service` JSON fields. Use GitHub's REST API with `GITHUB_TOKEN`, `issues: write`, a fixed label, and a fixed
title. Issue bodies contain only timestamps, status codes, and fixed diagnostic categories.

- [ ] **Step 4: Add the scheduled workflow**

Create:

```yaml
name: Remote Health Monitor
on:
  schedule:
    - cron: '*/5 * * * *'
  workflow_dispatch:
permissions:
  contents: read
  issues: write
concurrency:
  group: remote-health-monitor
  cancel-in-progress: false
jobs:
  probe:
    runs-on: ubuntu-latest
    timeout-minutes: 3
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25.12'
      - run: go run ./cmd/remote-health-monitor
        env:
          REMOTE_HEALTH_URL: ${{ vars.PUBLIC_BASE_URL }}/api/health
          FEISHU_WEBHOOK_URL: ${{ secrets.FEISHU_WEBHOOK_URL }}
          GITHUB_TOKEN: ${{ github.token }}
```

- [ ] **Step 5: Verify GREEN and workflow safety**

Run:

```bash
go test -race ./cmd/remote-health-monitor -count=1
bash scripts/quality/secret-scan-test.sh
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/remote-health-monitor .github/workflows/remote-health-monitor.yml
git commit -m '[feat](monitoring): add external health fallback'
```

## Task 11: Implement Safe Monitoring Deployment and Remote Inventory

**Files:**

- Create: `scripts/deploy-remote-monitoring.sh`
- Create: `scripts/deploy-remote-monitoring-test.sh`
- Modify: `.github/workflows/deploy.yml`
- Modify: `scripts/quality/check-deployment-safety-test.sh`

- [ ] **Step 1: Write fake-tool deployment tests**

Prove the script:

- exits before mutation when current release inventory fails;
- rejects an unexpected current chart/release/namespace;
- runs validation before `helm upgrade`;
- uses exact chart pins and `--atomic --wait`;
- never runs `helm uninstall`, deletes CRDs/PVCs, or applies `k8s/monitoring.yaml`;
- propagates Helm, kubectl apply, rollout, target, and rule-health failures;
- writes inventory only to a `mktemp -d` directory and removes it on exit.

- [ ] **Step 2: Verify RED**

Run:

```bash
bash scripts/deploy-remote-monitoring-test.sh
```

Expected: FAIL because the deploy script does not exist.

- [ ] **Step 3: Implement read-only preflight inventory**

The script begins with `set -euo pipefail`, sources `versions.env`, creates a private temp directory with `umask 077`,
and captures safe inventory from `helm list/status/get values`, CR names, PVC names, dashboard ConfigMap names, and
datasource ConfigMap names. It never prints secret data or `helm get manifest` sections containing Secrets.

- [ ] **Step 4: Implement atomic reconciliation**

Run repository validation, then:

```bash
helm upgrade --install "${KUBE_PROMETHEUS_STACK_RELEASE}" \
  prometheus-community/kube-prometheus-stack \
  --version "${KUBE_PROMETHEUS_STACK_CHART_VERSION}" \
  --namespace "${MONITORING_NAMESPACE}" --create-namespace \
  -f monitoring/remote/kube-prometheus-stack-values.yaml \
  --atomic --wait --timeout 15m

helm upgrade --install "${BLACKBOX_EXPORTER_RELEASE}" \
  prometheus-community/prometheus-blackbox-exporter \
  --version "${BLACKBOX_EXPORTER_CHART_VERSION}" \
  --namespace "${MONITORING_NAMESPACE}" \
  -f monitoring/remote/blackbox-exporter-values.yaml \
  --atomic --wait --timeout 10m
```

Apply repository resources, wait for the adapter and operator reconciliation, then query Prometheus through a
temporary port-forward for expected targets, loaded rules, and zero evaluation failures. Trap always terminates the
port-forward and removes the temp directory.

- [ ] **Step 5: Wire deployment after application Helm succeeds**

Invoke the script only for the current deployment candidate and only after the application Helm rollout is healthy.
Keep monitoring reconciliation serialized in the existing `stratum-production` concurrency group.

- [ ] **Step 6: Verify GREEN**

Run:

```bash
bash scripts/deploy-remote-monitoring-test.sh
bash scripts/quality/check-deployment-safety-test.sh
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add scripts/deploy-remote-monitoring*.sh .github/workflows/deploy.yml \
  scripts/quality/check-deployment-safety-test.sh
git commit -m '[feat](monitoring): reconcile remote monitoring safely'
```

## Task 12: Add Actionable Chinese Runbooks and Deprecate Stale Manifests

**Files:**

- Create: `docs/operations/remote-monitoring-runbook.md`
- Create: `docs/operations/alerts/availability.md`
- Create: `docs/operations/alerts/http-performance.md`
- Create: `docs/operations/alerts/workloads.md`
- Create: `docs/operations/alerts/capacity.md`
- Create: `docs/operations/alerts/dependencies.md`
- Create: `docs/operations/alerts/monitoring-system.md`
- Modify: `docs/agent/observability.md`
- Modify: `docs/agent/deployment-architecture.md`
- Modify or delete after reference proof: `k8s/monitoring.yaml`
- Modify: `docs/documentation-map.md`

- [ ] **Step 1: Add a runbook-link consistency check**

Extend `monitoring-config-test.sh` to extract every `runbook_url` path from custom rules, map it to a repository file,
and fail if the file or an alert heading is absent.

- [ ] **Step 2: Verify RED**

Run:

```bash
bash scripts/quality/monitoring-config-test.sh
```

Expected: FAIL because the runbooks do not exist.

- [ ] **Step 3: Write the operations runbook**

Document pinned releases, architecture, target/rule/dashboard checks, Feishu rotation and delivery test, bounded
silences, upgrades, rollback without PVC/CRD deletion, retention, backup, external monitor state issue, and how to
separate application failure from monitoring failure.

- [ ] **Step 4: Write alert-family runbooks**

Every Chinese runbook contains impact, urgency, Grafana and safe PromQL queries, read-only Kubernetes commands,
likely causes in evidence order, bounded mitigation, rollback, escalation, recovery checks, and evidence retention.
Commands must not print Secret objects, environment variables, tokens, or raw response bodies.

- [ ] **Step 5: Resolve stale source-of-truth documentation**

Search all references to `k8s/monitoring.yaml`. If none are active, delete it and update docs to name
`monitoring/remote/` as remote authority. If an active local workflow still needs it, add an explicit header that it
is local-only and remove the unpinned `latest` image. Do not silently keep two remote authorities.

- [ ] **Step 6: Regenerate agent instructions if source docs require it**

Run:

```bash
make agent-instructions
make agent-instructions-check
bash scripts/quality/monitoring-config-test.sh
```

Expected: PASS and generated instructions current.

- [ ] **Step 7: Commit**

```bash
git add docs monitoring/remote k8s/monitoring.yaml
git commit -m '[docs](monitoring): add alert response runbooks'
```

## Task 13: Local Verification Gate

**Files:**

- Modify only if a verified defect is found in task-owned code.

- [ ] **Step 1: Run format and focused Go tests**

```bash
gofmt -w internal/platform/alerting cmd/feishu-alert-adapter cmd/remote-health-monitor
go test -race ./internal/platform/alerting ./cmd/feishu-alert-adapter ./cmd/remote-health-monitor -count=1
```

Expected: PASS.

- [ ] **Step 2: Run monitoring and deployment guardrails**

```bash
make monitoring-config-test
bash scripts/quality/check-helm-image-rendering-test.sh
bash scripts/quality/check-deployment-safety-test.sh
bash scripts/quality/secret-scan-test.sh
make risk-guardrails
```

Expected: PASS with no skipped validator.

- [ ] **Step 3: Run repository-wide Go verification**

```bash
stratum-verify go-test
```

Expected: all task-owned packages pass. If the known baseline system-assistant contract still fails, attach the exact
same test name and root-cause evidence; do not claim the entire suite passed.

- [ ] **Step 4: Run the risk classifier**

```bash
bash scripts/quality/risk-regression-guard.sh --acceptance \
  helm monitoring internal/platform/alerting cmd/feishu-alert-adapter cmd/remote-health-monitor \
  scripts/deploy-remote-monitoring.sh .github/workflows
```

Expected: `soak`, because the change adds an external notification dependency and deployment resources.

- [ ] **Step 5: Commit verification-only corrections**

Commit only corrections required by the verification output, with one focused commit per root cause.

## Task 14: Remote Deployment and Alert Delivery Acceptance

**Files:**

- Create: `docs/operations/evidence/remote-monitoring-acceptance-2026-07-28.md`
- Modify task-owned code only when remote evidence proves a defect.

- [ ] **Step 1: Confirm prerequisites without exposing values**

Verify that `FEISHU_WEBHOOK_URL` and `PUBLIC_BASE_URL` exist in the GitHub production environment, that the configured
Feishu bot accepts messages, and that cluster access works. Report only `configured=true/false`.

- [ ] **Step 2: Trigger deployment through the normal CD workflow**

Push the feature branch only after local verification, open a PR, and use the repository's deployment path after
merge or an explicitly authorized manual-dispatch candidate. Do not bypass the workflow with an undocumented local
apply for the final state.

- [ ] **Step 3: Prove collection and dashboards**

Through a secure port-forward, query Prometheus for expected targets, custom rule groups, rule evaluation errors,
probe success, application RED metrics, node/workload/PVC metrics, verified dependency metrics, and adapter metrics.
Query core dashboard expressions and record only safe counts/statuses.

- [ ] **Step 4: Prove firing and resolved delivery**

Apply a namespaced test rule with a unique non-sensitive run ID, wait for Alertmanager firing, confirm the Feishu
firing card, remove the condition, confirm resolved delivery, then delete the rule. Repeat with a dedicated failing
Blackbox target rather than stopping the public service.

- [ ] **Step 5: Prove external fallback transitions**

Run `remote-health-monitor.yml` against a controlled failing URL, confirm one open state issue and one Feishu firing
message, rerun to prove deduplication, then restore the real URL and confirm issue closure plus resolved delivery.

- [ ] **Step 6: Run required system acceptance**

```bash
STATEFUL_E2E_PROFILE=test STATEFUL_E2E_DURATION_SEC=600 STATEFUL_E2E_PACKS=all make e2e-system-soak
make e2e-attestation-check
```

Monitor the runner to terminal state. No capability may be skipped or unreconciled. Record mode, seed, packs,
action/evidence counts, attestation safe path, cleanup, residual entities, and risks.

- [ ] **Step 7: Clean up and document evidence**

Delete test rules, targets, silences, issues created only for failure injection, and port-forwards. Write the acceptance
document with source SHA, chart versions, safe target/rule/dashboard results, delivery timestamps, E2E evidence, and
known single-node boundaries. Do not include webhook URLs, tokens, cookies, passwords, or raw alert payloads.

- [ ] **Step 8: Final verification commit**

```bash
git add docs/operations/evidence/remote-monitoring-acceptance-2026-07-28.md
git commit -m '[test](monitoring): record remote alert acceptance'
```

## Completion Audit

Before claiming the objective complete, map every design success criterion to current evidence:

| Requirement | Required evidence |
|---|---|
| Git-reproducible stack | pinned files, clean render, successful atomic CD reconciliation |
| Complete expected collection | Prometheus target API and verified metric queries |
| Public end-to-end visibility | Blackbox probe metrics and controlled failure |
| Correct rules | `promtool` checks/tests and zero runtime evaluation failures |
| Feishu delivery | observed firing and resolved messages |
| Dashboards | provisioned ConfigMaps and successful core queries |
| Monitoring blind-spot fallback | external workflow failure, deduplication, and recovery evidence |
| Operational readiness | runbook link check and exercised diagnostic steps |
| Security | secret scan, redaction tests, no secret output or rendered secret artifacts |
| Cleanup | zero temporary rules, silences, targets, issues, and local processes |
| System regression | selected soak attestation matches the final source |

Any missing, inferred, stale, skipped, or indirect evidence means the goal remains incomplete.
