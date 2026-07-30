# Remote Test Service Monitoring and Alerting Design

## Status

- Date: 2026-07-28
- Scope: traditional service performance, availability, capacity, and stability
- Environment: remote K3s test service exposed at `http://101.200.181.141:6879`
- Decision: manage the existing `kube-prometheus-stack` through GitOps and deliver alerts through a Feishu bot

## Goal

Build a reproducible monitoring and alerting system for the remote test service that detects user-visible outages,
performance regressions, workload instability, resource exhaustion, dependency failure, and monitoring failure. The
system must provide actionable alerts, useful dashboards, tested notification delivery, and version-controlled
operations without exposing credentials.

This phase deliberately excludes LLM, Agent, token-cost, evaluation, and business-conversion telemetry.

## Evidence and Constraints

### Repository evidence

- The remote cluster already runs `kube-prometheus-stack` release `kps` in namespace `monitoring`; the documented
  observed chart version is `87.10.1`.
- The release is not installed or upgraded by `.github/workflows/deploy.yml`, so the current monitoring state is not
  reproducible from Git.
- `helm/values-demo.yaml` currently sets `serviceMonitor.enabled: false`, so the chart does not establish a reliable
  Prometheus Operator scrape contract for the backend.
- `k8s/monitoring.yaml` describes a separate, unpinned standalone Prometheus deployment and is not the source of truth
  for the remote cluster.
- `k8s/ingress.yaml` contains obsolete labels and ingress assumptions and must not be reused as the remote monitoring
  contract.
- The public health endpoint returned HTTP 200 with `{"service":"Stratum","status":"ok"}` during design discovery.
- The deployment workflow verifies health only during deployment. It provides no continuous availability or
  notification guarantee.
- The environment is a single-node K3s test system with single replicas for important stateful services. Alerts must
  reflect that constraint and must not claim production high availability.

### Long-term and upstream evidence

- The governed knowledge protocol requires project facts to take precedence, Obsidian notes to be treated as
  read-only design input, and current external behavior to be checked against authoritative sources.
- Prometheus guidance recommends alerting on user-visible symptoms, keeping paging alerts few and actionable, allowing
  slack for transient blips, linking alerts to consoles, and monitoring the monitoring system itself.
- Google SRE burn-rate guidance supports multi-window SLO alerts for sufficiently mature services. This test service
  does not yet have reliable traffic baselines, so this design first records availability and latency objectives and
  uses conservative duration-based alerts. Burn-rate paging is a later evidence-driven refinement, not a fabricated
  initial threshold.
- Alertmanager supports grouping, inhibition, time intervals, and resolved notifications. These mechanisms are used
  to reduce duplicate alerts rather than building custom routing logic.
- Blackbox Exporter provides independent HTTP probing, but an exporter inside the same cluster cannot detect total
  cluster or host loss. A scheduled GitHub Actions probe therefore provides an external fallback.

Authoritative references:

- <https://prometheus.io/docs/practices/alerting/>
- <https://prometheus.io/docs/alerting/latest/configuration/>
- <https://sre.google/workbook/alerting-on-slos/>
- <https://github.com/prometheus/blackbox_exporter>
- <https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack>

## Success Criteria

The design is implemented only when all of the following are proven against the remote test environment:

1. The monitoring stack, its pinned versions, rules, dashboards, scrape contracts, and notification routing can be
   reconstructed from the repository plus secrets.
2. Prometheus reports healthy targets for Kubernetes, the node, Stratum, Blackbox Exporter, and every dependency for
   which a supported metrics endpoint or exporter is intentionally installed.
3. Public-path probing covers Traefik, frontend Nginx, and the backend health endpoint.
4. Rule validation and PromQL unit tests pass, and Prometheus reports no rule evaluation failures.
5. A controlled test alert produces both firing and resolved messages in Feishu.
6. A controlled public-probe failure triggers a critical alert and resolves after recovery.
7. Grafana dashboards display current remote data for service health, HTTP behavior, workloads, resources,
   dependencies, and active alerts.
8. The external scheduled probe reports the service independently of the cluster monitoring stack.
9. Temporary alerts, silences, test resources, and port forwards are removed after verification.
10. No webhook, token, cookie, password, private key, or raw sensitive response is committed or printed.

## Architecture

```text
Stratum /metrics -----------+
Kubernetes and node metrics +--> Prometheus --> PrometheusRule --> Alertmanager --> Feishu adapter --> Feishu
dependency exporters -------+         |                  |               |
Blackbox public HTTP probe -+         +--> Grafana       +--> inhibit/group/time routing

GitHub Actions scheduled external probe -----------------------------------------------> Feishu
```

### Ownership boundaries

- `kube-prometheus-stack` owns Prometheus, Alertmanager, Grafana, kube-state-metrics, node-exporter, their CRDs, and
  standard Kubernetes rules.
- The Stratum Helm chart owns the backend Service labels and `ServiceMonitor` needed to scrape `/metrics`.
- Repository-managed monitoring resources own Stratum-specific rules, dashboards, Blackbox configuration, Feishu
  adapter deployment, and operations documentation.
- GitHub Actions owns the external synthetic probe and receives its own Feishu webhook secret.
- Application code continues to own metric semantics. This phase adds application metrics only when a required
  traditional service signal cannot be derived from the existing HTTP or runtime metrics.

## GitOps Layout

The implementation will use a focused `monitoring/remote/` tree:

```text
monitoring/remote/
  kube-prometheus-stack-values.yaml
  blackbox-exporter-values.yaml
  alertmanager/
    config.yaml
    templates.tmpl
  rules/
    stratum-availability.yaml
    stratum-http.yaml
    stratum-workloads.yaml
    stratum-capacity.yaml
    stratum-dependencies.yaml
    stratum-monitoring.yaml
  dashboards/
    stratum-service-overview.json
    stratum-http.json
    stratum-resources.json
    stratum-dependencies.json
  tests/
    stratum-rules.test.yaml
```

The exact filenames may be reduced during planning if the chart's sidecar loading contract makes a smaller layout
clearer. Responsibilities must remain separated. The obsolete standalone `k8s/monitoring.yaml` will be explicitly
deprecated or removed only after reference and remote-ownership checks prove it is unused.

## Collection Design

### Application service

Enable the Stratum Helm `ServiceMonitor` with labels selected by the remote `kps` Prometheus instance. Scrape the
backend service port at `/metrics` every 30 seconds with a 10-second timeout. Preserve bounded label cardinality:
route templates are allowed; raw URLs, tenant IDs, user IDs, agent IDs, and request IDs are not infrastructure alert
dimensions.

Primary signals:

- `up` and scrape duration for target health.
- `http_requests_total` for request rate and status-class error rate.
- `http_request_duration_seconds` for latency distributions.
- `http_requests_in_flight` for saturation context.
- Go and process collector metrics for runtime memory, goroutines, file descriptors, CPU, and restarts.

### Public path

Blackbox Exporter probes `GET http://101.200.181.141:6879/api/health` and requires:

- HTTP 200;
- response body containing the stable health contract;
- bounded DNS/connect/TLS/processing time metrics where applicable;
- an explicit timeout below the Prometheus scrape timeout.

The endpoint is currently plain HTTP. The monitor must expose this as an accepted environment constraint and must not
represent transport security as healthy merely because the request succeeds.

### Kubernetes and host

Use the standard stack collectors for:

- node CPU, memory, filesystem, inode, and network saturation;
- Pod readiness, restart loops, pending state, and deployment replica availability;
- PVC usage and predicted exhaustion;
- Prometheus, Alertmanager, operator, rule evaluation, and scrape health.

### Dependencies

Monitor PostgreSQL, Redis, NATS JetStream, Milvus, MinIO, and etcd only through supported native metrics endpoints or
maintained exporters with pinned images and bounded resources. TCP reachability alone may be used for diagnostic
visibility but must not be presented as dependency correctness. Exporters must use least-privilege credentials stored
in Kubernetes Secrets.

## Alert Policy

### Severity and delivery

| Severity | Meaning | Default persistence | Delivery |
|---|---|---:|---|
| `critical` | active user impact, no serving replica, imminent data-volume exhaustion, or monitoring blind spot | 2-5 minutes | Feishu at all times |
| `warning` | sustained degradation or capacity risk requiring planned intervention | 10-15 minutes | Feishu on weekdays 09:00-19:00 Asia/Shanghai |
| `info` | trend or diagnostic condition without immediate action | 30 minutes | Alertmanager and Grafana only |

Every notifying alert must include `severity`, `service`, `environment`, `component`, `summary`, `description`,
`dashboard_url`, and `runbook_url`. Descriptions state observed impact and current value without including credentials
or unbounded label data.

### Initial alert catalogue

#### Availability

- `StratumPublicEndpointDown` (`critical`): public probe fails for 3 minutes.
- `StratumBackendUnavailable` (`critical`): no ready backend replica for 3 minutes.
- `StratumFrontendUnavailable` (`critical`): no ready frontend replica for 3 minutes.
- `StratumTargetMissing` (`warning`): an expected application target is absent or down for 10 minutes.

#### HTTP RED

- `StratumHighHTTP5xxRate` (`warning`): 5xx ratio exceeds 5% for 10 minutes and the request count is at least 20 over
  the evaluation window.
- `StratumCriticalHTTP5xxRate` (`critical`): 5xx ratio exceeds 20% for 5 minutes with the same minimum-volume guard.
- `StratumHighHTTPP95Latency` (`warning`): P95 exceeds 2 seconds for 15 minutes with sufficient histogram samples.
- `StratumCriticalHTTPP95Latency` (`critical`): P95 exceeds 5 seconds for 5 minutes with sufficient samples.

Health and metrics endpoints are excluded from user-traffic latency and error calculations. Thresholds are starting
values for the test environment and must be reviewed after at least two weeks of retained evidence.

#### Workloads and host

- deployment unavailable, Pod crash loop, frequent restarts, long pending, node not ready;
- sustained node CPU above 85%, memory availability below 10%, filesystem or inode availability below 15%;
- container memory near limit and sustained CPU throttling;
- time drift or exporter disappearance when supported by the stack.

#### Capacity and persistence

- PVC usage above 80% (`warning`) and 90% (`critical`);
- predicted filesystem or PVC exhaustion within 4 days (`warning`) or 24 hours (`critical`), only when the prediction
  has enough samples and a positive growth slope;
- PostgreSQL connection saturation and storage growth;
- Redis memory pressure;
- NATS JetStream storage, consumer lag, and unavailable stream replicas where exported;
- etcd leader or commit failures, MinIO availability/capacity, and Milvus exporter-supported health signals.

#### Monitoring health

- Prometheus target or rule evaluation failures;
- Prometheus configuration reload failure;
- Alertmanager cluster/config/notification failure;
- Feishu adapter send failures or sustained queue growth;
- Blackbox Exporter unavailable;
- external probe workflow stale or repeatedly failing.

### Noise control

- Group by `alertname`, `service`, `environment`, and `severity`, not by Pod UID or instance unless required for action.
- Wait 30 seconds for initial grouping, group follow-up alerts for 5 minutes, and repeat critical alerts every 4 hours.
- Send resolved notifications.
- Inhibit component-cause alerts while the public endpoint symptom is firing.
- Inhibit Pod alerts when its node is unavailable and replica alerts when the owning deployment is unavailable.
- Use `for` durations to absorb transient rollout and scrape blips.
- Deployment workflows create a bounded maintenance silence only around expected disruptive changes, record its owner
  and expiry, and always remove it in cleanup. They must never create an unbounded silence.

## Feishu Delivery

Alertmanager sends a stable, minimal webhook payload to a small in-cluster adapter. The adapter converts grouped
firing and resolved alerts into Feishu interactive-card messages. This boundary is preferred over embedding Feishu
protocol details into alert rules or relying on an unversioned third-party image.

The adapter must:

- verify and parse bounded Alertmanager payloads;
- apply a request-body limit and server timeouts;
- send with a bounded execution budget, finite exponential-backoff retries, and no retry on permanent 4xx failures;
- expose request, delivery, failure, retry, queue, and latency metrics;
- avoid logging webhook URLs, request bodies, or Feishu response bodies;
- support graceful shutdown and wait for in-flight sends;
- return failure to Alertmanager when delivery did not succeed so Alertmanager can retry;
- render only allowlisted alert fields and URLs.

`FEISHU_WEBHOOK_URL` is injected from a GitHub Actions Secret into a Kubernetes Secret. It is never stored in Helm
values, rendered artifacts, logs, test fixtures, or Grafana annotations.

## External Synthetic Probe

A scheduled GitHub Actions workflow probes the public health contract from outside K3s. It has no cluster dependency
and uses a separate Feishu webhook secret. The workflow:

- runs every five minutes and on manual dispatch;
- uses a bounded request timeout and checks HTTP status plus the stable JSON contract;
- records no response body unless it is reduced to a safe fixed diagnostic;
- sends an alert only after consecutive failures to reduce Internet-transient noise;
- sends a resolved message after recovery;
- maintains state without putting credentials or sensitive response data in artifacts;
- exposes staleness through a repository-visible check or companion heartbeat mechanism.

GitHub scheduled workflows are not a hard real-time pager and may be delayed. They are an independent fallback for a
test service, not a production availability monitor.

## Dashboards

Provision dashboards through labeled ConfigMaps consumed by the `kps` Grafana sidecar. The first iteration provides:

1. Service overview: public availability, request rate, error ratio, latency, replicas, active alerts, and latest
   deployment identity.
2. HTTP performance: route-level RED metrics with bounded route labels and health-route exclusion.
3. Resources: node and container CPU, memory, filesystem, network, restarts, and PVC capacity.
4. Dependencies: PostgreSQL, Redis, NATS, Milvus, MinIO, and etcd signals that are actually exported.

Panels link to relevant alerts and runbooks. Dashboards do not imply collection coverage for missing exporters; missing
data is visible as missing coverage, not rendered as zero or healthy.

## Deployment and Rollback

The deploy workflow will:

1. validate Helm rendering, Prometheus rules, Alertmanager configuration, dashboard JSON, secret references, and
   pinned image/chart versions;
2. establish the Feishu Secret without printing its value;
3. install or upgrade the pinned `kube-prometheus-stack` and Blackbox chart using `--atomic --wait`;
4. apply Stratum monitoring resources and adapter deployment;
5. wait for CR reconciliation, targets, and rules to become healthy;
6. run smoke and notification tests;
7. report resource identity and safe status only.

Rollback uses Helm release history and version-controlled configuration. It does not delete Prometheus PVCs, Grafana
state, CRDs, or monitoring history. Replacing the existing release requires a preflight ownership inventory and a
backup of non-secret user-created Grafana resources before repository configuration becomes authoritative.

## Validation Strategy

### Static and unit validation

- `helm lint` and `helm template` against the remote values;
- `promtool check rules` and `promtool test rules` for all custom rules;
- `amtool check-config` for the rendered Alertmanager configuration;
- JSON parsing and dashboard schema checks;
- shell tests for deployment and external-probe failure propagation;
- Go unit and HTTP tests for the Feishu adapter, including retry, timeout, 4xx, 5xx, malformed payload, redaction, and
  graceful shutdown behavior;
- secret scanning across tracked files and rendered test output.

### Remote acceptance

Remote acceptance is evidence-driven and non-destructive:

- inventory the existing release and resources before ownership changes;
- prove expected targets and rules through the Prometheus API;
- inject a namespaced test `PrometheusRule`, verify firing and resolved delivery, then delete it;
- use a dedicated test probe target or bounded failure injection instead of stopping the public service when possible;
- query each dashboard's core expressions against Prometheus;
- trigger the external workflow in test mode and verify its failure and recovery messages;
- run repository risk guardrails and the system E2E mode selected by the risk classifier;
- verify attestation and clean all temporary resources.

No completion claim is allowed if a selected capability is skipped, notification delivery is inferred rather than
observed, or the current source does not match the verification attestation.

## Operational Documentation

Each notifying alert has a concise Chinese runbook containing:

- user-visible impact and urgency;
- dashboard and safe query links;
- likely causes ordered by evidence value;
- read-only diagnostic commands;
- bounded mitigation and rollback steps;
- escalation criteria;
- resolution verification;
- post-incident evidence to retain.

The monitoring overview runbook also documents upgrades, configuration reloads, silences, Feishu test delivery,
credential rotation, retention, restoration, and how to distinguish application failure from monitoring failure.

## Deferred Work

- AI and business telemetry, including LLM latency, token cost, Agent execution, evaluation quality, and tenant-level
  product signals.
- Production-grade multi-region external availability monitoring.
- Burn-rate paging until the service has an agreed SLO and sufficient retained traffic evidence.
- High-availability Prometheus, Alertmanager, and application replicas; the remote environment remains single-node.
- Automated remediation. Alerts provide diagnosis and bounded operator actions only.

## Risks and Boundaries

- A single-node test cluster has unavoidable correlated failures. Monitoring improves detection, not availability.
- Cluster-local Blackbox probes cannot prove Internet reachability from arbitrary users.
- GitHub scheduled workflows can be delayed or disabled after repository inactivity.
- Initial static thresholds may be noisy or insensitive; review them against two weeks of evidence before tightening.
- Dependency exporters add credentials and resource cost. Install only maintained exporters that provide actionable
  signals and document unsupported gaps.
- Existing remote monitoring state may contain manual resources not represented in Git. Inventory and preserve them
  before making Git authoritative.
