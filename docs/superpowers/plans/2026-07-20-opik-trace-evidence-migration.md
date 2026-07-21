# Opik Trace Evidence Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Opik and encrypted object storage the only runtime sources for Agent execution observability and evaluation evidence, with no runtime reads or writes to `agent_executions`, `agent_tool_traces`, or `agent_trace_events`.

**Architecture:** Agent and Evaluation application packages consume narrow evidence ports implemented by an Opik REST adapter. OTel Collector exports retained traces to Opik over OTLP/HTTP, while an optional MinIO adapter stores encrypted payloads and returns opaque references. PostgreSQL continues to own transactional feedback, experiment, optimization, checkpoint, and approval state.

**Tech Stack:** Go 1.25, OpenTelemetry 1.40, OTel Collector Contrib 0.96/0.102, Opik REST/OTLP HTTP, MinIO Go SDK, PostgreSQL, Gin.

---

## Task 1: Configuration And Evidence Errors

**Files:**

- Modify: `config/config.go`
- Modify: `config/config_test.go`
- Modify: `.env.example`
- Modify: `pkg/constants/agent.go`
- Modify: `api/middleware/error_mapping.go`
- Modify: `api/middleware/middleware_test.go`
- Create: `internal/agent/domain/evidence.go`

- [ ] **Step 1: Write failing configuration and HTTP mapping tests**

Add tests asserting `OPIK_URL`, `OPIK_PROJECT`, `OPIK_WORKSPACE`, `OPIK_API_KEY`, `TRACE_PAYLOAD_*` settings load without exposing secrets, and that evidence unavailable/not found/invalid errors map to `503/404/502` with the frozen `{"error":"..."}` shape.

- [ ] **Step 2: Run the tests and confirm the missing fields/error mappings fail**

Run: `go test ./config ./api/middleware -run 'Opik|Evidence' -count=1`

- [ ] **Step 3: Add configuration and domain errors**

Define:

```go
var (
    ErrEvidenceUnavailable = errors.New("trace evidence unavailable")
    ErrEvidenceNotFound    = errors.New("trace evidence not found")
    ErrEvidenceInvalid     = errors.New("trace evidence invalid")
)

type OpikConfig struct {
    URL, Project, Workspace, APIKey string
    Timeout time.Duration
}

type TracePayloadConfig struct {
    Enabled bool
    Endpoint, AccessKey, SecretKey, Bucket string
    UseTLS bool
}
```

Use project constants for timeout and response-size limits. Add only non-secret examples to `.env.example`.

- [ ] **Step 4: Run focused tests**

Run: `go test ./config ./api/middleware -count=1`

## Task 2: Agent Evidence Port And Opik DTO Mapping

**Files:**

- Create: `internal/agent/domain/port/trace_evidence.go`
- Create: `internal/agent/infrastructure/opik/client.go`
- Create: `internal/agent/infrastructure/opik/dto.go`
- Create: `internal/agent/infrastructure/opik/mapper.go`
- Create: `internal/agent/infrastructure/opik/client_test.go`
- Create: `internal/agent/infrastructure/opik/mapper_test.go`

- [ ] **Step 1: Write failing mapper and HTTP contract tests using `httptest.Server`**

Cover execution pagination, tenant plus business-trace filtering, Opik trace-ID resolution, Span hierarchy mapping,
Tool/LLM classification, usage/cost/status mapping, response-size rejection, `404`, `5xx`, timeout, malformed JSON, and
cross-tenant evidence rejection.

- [ ] **Step 2: Run and observe compile failure for the missing port/client**

Run: `go test ./internal/agent/infrastructure/opik -count=1`

- [ ] **Step 3: Define the consumer-side port**

```go
type TraceEvidenceProvider interface {
    ListExecutions(context.Context, domain.ListOptions) ([]domain.ExecutionRecord, int64, error)
    ToolObservations(context.Context, string, string) ([]domain.ToolObservation, error)
    TraceEvents(context.Context, string, string) ([]domain.AgentTraceEvent, error)
    Resolve(context.Context, string, string) (domain.TraceEvidence, error)
    ResolveBatch(context.Context, string, []string) (map[string]domain.TraceEvidence, error)
}
```

`Resolve` arguments are `tenantID, businessTraceID`. `domain.TraceEvidence` contains resource assignments and aggregate
latency, status, tokens, cost, experiment, and security fields without importing Opik DTOs.

- [ ] **Step 4: Implement bounded Opik REST calls and mapping**

Use `/api/v1/private/traces`, then `/api/v1/private/spans?trace_id=<opik-id>`. Always filter and validate
`opik.metadata.stratum.tenant_id` and `opik.metadata.stratum.trace_id`. Apply project/workspace headers, optional
authorization, timeout, response body limits, bounded page size, and domain error translation.

- [ ] **Step 5: Run focused tests**

Run: `go test ./internal/agent/infrastructure/opik -count=1`

## Task 3: Complete Opik-Queryable OTel Contract

**Files:**

- Modify: `internal/agent/application/agent.go`
- Modify: `internal/agent/application/graph/react.go`
- Modify: `internal/agent/application/react_agent_test.go`
- Modify: `internal/llmgateway/infrastructure/gateway.go`
- Modify: `internal/llmgateway/infrastructure/gateway_test.go`

- [ ] **Step 1: Add failing SpanRecorder tests for all query metadata aliases and final root aggregates**

Require aliases including:

```text
opik.metadata.stratum.tenant_id
opik.metadata.stratum.trace_id
opik.metadata.stratum.execution_id
opik.metadata.stratum.resource_manifest
opik.metadata.stratum.experiment_assignments
opik.metadata.stratum.provider_type
opik.metadata.stratum.resource_revision_id
opik.metadata.stratum.status
```

The ended root span must contain final duration, status, tokens, and cost rather than only start-time attributes.

- [ ] **Step 2: Run focused tests and confirm missing attributes fail**

Run: `go test ./internal/agent/application ./internal/llmgateway/infrastructure -run 'Opik|Evidence|OTel' -count=1`

- [ ] **Step 3: Add standards plus Opik metadata aliases**

Use a small helper that emits both names for fields queried by Stratum. Set final root attributes before `End()`. Keep
all raw content behind the existing opt-in gate and never place large serialized payloads in metadata.

- [ ] **Step 4: Run focused tests**

Run: `go test ./internal/agent/application ./internal/agent/application/graph ./internal/llmgateway/infrastructure -count=1`

## Task 4: Switch Agent Runtime And APIs Off PostgreSQL Observation Stores

**Files:**

- Modify: `internal/agent/application/agent_service.go`
- Modify: `internal/agent/application/agent.go`
- Modify: `internal/agent/application/execution_store.go`
- Modify: `internal/agent/domain/port/repository.go`
- Modify: `internal/agent/application/agent_service_test.go`
- Modify: `internal/agent/application/react_agent_test.go`
- Modify: `api/wiring/agent.go`
- Modify: `api/wiring/wiring_test.go`
- Modify: `api/http/handler/agent_crud_handler.go`
- Modify: `api/http/handler/agent_exec_handler_test.go`

- [ ] **Step 1: Write failing tests proving execution succeeds without observation repositories and APIs call evidence provider**

Tests must assert no `ExecutionRepo`, `ToolTraceRepo`, or `TraceEventRepo` method is invoked. Keep checkpoint and approval
tests unchanged.

- [ ] **Step 2: Run tests and confirm current PostgreSQL wiring/reads fail the contract**

Run: `go test ./internal/agent/application ./api/wiring ./api/http/handler -run 'Evidence|ObservationStore' -count=1`

- [ ] **Step 3: Replace three repositories with one evidence provider**

Remove `ExecStore`, `ToolTraceStore`, and `TraceEventStore` from `AgentServiceDeps` and `wiring.Agent`. Add
`EvidenceProvider port.TraceEvidenceProvider`. Stop attaching trace stores and stop the end-of-execution PostgreSQL
writes. Keep in-memory `AgentResult` observations long enough to finish Span attributes and the immediate response.

- [ ] **Step 4: Preserve public APIs through Opik mapping**

`ListExecutions`, `ListToolTraces`, and `ListTraceEvents` delegate to the evidence provider and return explicit evidence
errors. Do not add a PostgreSQL fallback.

- [ ] **Step 5: Run Agent/API tests**

Run: `go test ./internal/agent/... ./api/http/handler ./api/wiring -count=1`

## Task 5: Move Feedback Attribution And Online Metrics To Evidence Provider

**Files:**

- Modify: `internal/evaluation/domain/port/evaluation.go`
- Modify: `internal/evaluation/domain/experiment.go`
- Modify: `internal/evaluation/application/feedback_service.go`
- Modify: `internal/evaluation/application/feedback_service_test.go`
- Modify: `internal/evaluation/infrastructure/persistence/feedback_repository.go`
- Modify: `internal/evaluation/infrastructure/persistence/feedback_repository_integration_test.go`
- Modify: `api/wiring/evaluation.go`
- Modify: `api/http/dto/evaluation.go`
- Modify: `api/http/handler/evaluation_handler_test.go`

- [ ] **Step 1: Write failing feedback validation and bulk-observation tests**

Cover correct revision attribution, revision mismatch, missing evidence, Opik unavailable, tenant mismatch, stable/canary
grouping, token/cost/latency/status metrics, and post-hoc security feedback. Assert persistence SQL contains no references
to the three observation tables.

- [ ] **Step 2: Run tests and confirm repository coupling fails**

Run: `go test ./internal/evaluation/... ./api/http/handler -run 'Feedback|Observation|Evidence' -count=1`

- [ ] **Step 3: Split transactional repository from evidence reads**

Change `FeedbackRepository` to record validated feedback, load active experiment, list feedback rows for a stage, and
save no observation evidence. Inject an Evaluation-side evidence reader through a thin wiring adapter to the Agent-side
provider.

- [ ] **Step 4: Validate evidence before persistence and bulk join metrics**

The application resolves resource assignment before `Record`, rejects mismatches, then bulk-resolves all feedback trace
IDs for stage evaluation. It must abort the rollout decision on any unavailable/invalid evidence rather than calculating
partial metrics.

- [ ] **Step 5: Run Evaluation tests**

Run: `go test ./internal/evaluation/... ./api/wiring ./api/http/handler -count=1`

## Task 6: Encrypted MinIO Payload Evidence

**Files:**

- Modify: `go.mod`
- Modify: `go.sum`
- Create: `internal/agent/domain/port/trace_payload.go`
- Create: `internal/agent/infrastructure/objectstore/minio.go`
- Create: `internal/agent/infrastructure/objectstore/minio_test.go`
- Modify: `internal/agent/application/graph/react.go`
- Modify: `internal/agent/application/agent.go`
- Modify: `internal/agent/application/react_agent_test.go`
- Modify: `api/wiring/agent.go`

- [ ] **Step 1: Add the MinIO SDK with the repository dependency toolchain**

Run: `go get github.com/minio/minio-go/v7@latest`

- [ ] **Step 2: Write failing encryption/redaction/failure-isolation tests**

Use a fake object client. Assert object bytes do not contain plaintext or sensitive values, decrypt correctly with the
platform AES key, have tenant-scoped opaque keys, and return reference/hash/size. Assert storage failure leaves Agent
execution successful and raw content absent from Span attributes.

- [ ] **Step 3: Implement the payload port and adapter**

```go
type TracePayloadStore interface {
    Put(context.Context, TracePayload) (TracePayloadRef, error)
}
```

Reuse `pkg/crypto` AES-256-GCM and `pkg/observability` redaction. Do not reuse Milvus' MinIO bucket. Create/check the
dedicated evidence bucket at startup only when payload capture is enabled.

- [ ] **Step 4: Add asynchronous, bounded payload capture to LLM and Tool spans**

Capture only when configured. Store `payload_ref`, SHA-256, size, and status on the relevant Span. Never fail the Agent
run or fall back to raw attributes.

- [ ] **Step 5: Run focused and race tests**

Run: `go test -race ./internal/agent/... -count=1`

## Task 7: Collector And Deployment Integration

**Files:**

- Modify: `otel-collector-config.yaml`
- Modify: `k8s/tracing.yaml`
- Modify: `docker-compose.yml`
- Modify: `docker-compose.prod.yml`
- Modify: `helm/values.yaml`
- Modify: `helm/values-demo.yaml`
- Modify: `helm/values-demo-local.yaml`
- Modify: `helm/templates/deployment.yaml`
- Modify: `pkg/observability/collector_config_test.go`

- [ ] **Step 1: Extend failing collector contracts**

Require `otlphttp/opik`, endpoint/header environment expansion, traces pipeline export, existing 100% policies, and no
literal API key. Add Helm render assertions for endpoint/project/workspace secret wiring.

- [ ] **Step 2: Run contract tests and confirm missing exporter fails**

Run: `go test ./pkg/observability -run Collector -count=1`

- [ ] **Step 3: Configure OTLP/HTTP export to Opik**

Use `${env:OPIK_OTLP_ENDPOINT}`, `projectName`, `Comet-Workspace`, and authorization environment values. Preserve Jaeger
as a non-authoritative exporter. Ensure missing Opik configuration is a deliberate deployment choice, not a checked-in
credential or silent invalid endpoint.

- [ ] **Step 4: Validate both Collector versions and Helm rendering**

Run the `validate` command with Collector Contrib `0.96.0` and `0.102.0`, then run the repository Helm quality scripts.

## Task 8: Runtime-Zero-Dependency Guard And Historical Compatibility

**Files:**

- Create: `scripts/quality/no-agent-observation-table-runtime-test.sh`
- Modify: `scripts/quality/arch-guard.sh`
- Modify: `pkg/storage/postgres/tenant_schema_test.go`
- Delete: `internal/agent/infrastructure/persistence/execution_store.go`
- Delete: `internal/agent/infrastructure/persistence/trace_event_store.go`
- Delete: `internal/agent/infrastructure/persistence/tool_trace_store.go`
- Modify/Delete: associated persistence tests

- [ ] **Step 1: Write a failing source guard**

Permit the three table names only in tenant DDL, migration/history documentation, and explicit schema tests. Fail if
runtime Go packages reference them.

- [ ] **Step 2: Run the guard and confirm current repositories fail**

Run: `bash scripts/quality/no-agent-observation-table-runtime-test.sh`

- [ ] **Step 3: Remove obsolete runtime repositories and ports**

Delete implementations and test fixtures after all consumers use Opik. Keep table DDL unchanged and document it as
read-only historical compatibility pending the physical-removal migration.

- [ ] **Step 4: Run architecture and schema tests**

Run: `bash scripts/quality/arch-guard.sh && go test ./pkg/storage/postgres ./internal/agent/... -count=1`

## Task 9: Real Opik/Collector/MinIO End-To-End Verification

**Files:**

- Create temporarily, then delete: `internal/agent/application/tmp-opik-e2e_test.go`
- Modify: `docs/agent/observability.md`
- Modify: `docs/architecture/EVOLUTION.md`

- [ ] **Step 1: Start isolated pinned Opik, Collector, and MinIO services**

Use unique container/network/volume names. Do not print secrets. Wait on health endpoints rather than fixed sleeps.

- [ ] **Step 2: Run deterministic Agent scenarios**

Execute success, Tool call, LLM failure, evaluation, canary experiment, security event, and payload-capture scenarios.
Verify exact Span parent IDs, Opik metadata, resource assignment, costs, retained traces, and encrypted MinIO object.

- [ ] **Step 3: Exercise real Stratum APIs and Evaluation flow**

Verify execution listing and trace detail read Opik, feedback attribution succeeds, metrics use Opik evidence, tenant
cross-access is rejected, and no rows are inserted into the three observation tables.

- [ ] **Step 4: Verify failure behavior**

Stop Opik: Agent execution still succeeds; Trace APIs and feedback return `503`. Stop MinIO: Agent execution still
succeeds and Span records payload storage failure without raw content.

- [ ] **Step 5: Clean all temporary resources**

Delete temporary tests, containers, networks, volumes, and generated credentials. Confirm no task-specific process or
container remains.

## Task 10: Full Verification And Table Decision

**Files:**

- Modify: `docs/superpowers/plans/2026-07-20-opik-trace-evidence-migration.md`

- [ ] **Step 1: Run focused tests without cache**

Run: `go test ./internal/agent/... ./internal/evaluation/... ./api/... ./pkg/observability ./config -count=1`

- [ ] **Step 2: Run complete verification**

Run: `stratum-verify go-full`

- [ ] **Step 3: Check worktree and artifacts**

Run: `git diff --check`, inspect `git status --short`, source guard output, running processes, and task containers.

- [ ] **Step 4: Record the physical table decision**

If real E2E and runtime source guard pass, report that the tables are no longer runtime dependencies but remain as
read-only historical storage. Physical `DROP TABLE` requires the separately approved data disposition/migration phase;
otherwise report the exact parity gap and keep the tables.
