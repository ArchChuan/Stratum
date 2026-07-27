# Remote Acceptance Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make refreshed chat history, managed Agent metadata, guest onboarding, structured diagnostics, and the remote Demo Opik evidence path satisfy the accepted product contracts.

**Architecture:** Add an explicit persisted message-visibility dimension while keeping internal history available to Agent execution; redact the platform-managed prompt at the HTTP mapper; derive diagnostic actions deterministically; make guest provisioning idempotent with a PostgreSQL transient retry boundary. Deploy pinned Opik and an OTEL Collector as prerequisites of the Stratum Demo release and verify the complete write/read path.

**Tech Stack:** Go 1.25, pgx v5, Gin, PostgreSQL tenant schemas, Helm 3, Kubernetes/K3s, OpenTelemetry Collector, Opik 2.1.32, Bash, GitHub Actions.

---

## Task 1: Persist and enforce chat message visibility

**Files:**

- Modify: `internal/agent/domain/agent.go`
- Modify: `internal/agent/application/agent.go`
- Modify: `internal/agent/infrastructure/persistence/chat_store.go`
- Modify: `pkg/storage/postgres/tenant_schema.sql`
- Modify: `api/http/handler/chat_handler.go`
- Test: `internal/agent/application/react_agent_test.go`
- Test: `internal/agent/infrastructure/persistence/chat_store_test.go`
- Test: `internal/agent/infrastructure/persistence/chat_store_integration_test.go`
- Test: `api/http/handler/chat_handler_test.go`
- Test: `pkg/storage/postgres/tenant_schema_test.go`

- [ ] **Step 1: Write failing tests for explicit internal visibility and HTTP filtering**

Add an internal summary to the handler fixture and assert only the user request and final answer are returned. Extend the
Agent persistence assertion to require `Visibility == domain.ChatMessageVisibilityInternal`. Extend repository SQL mocks
to require a `visibility` insert argument and scan column.

- [ ] **Step 2: Run focused tests and verify RED**

Run: `go test ./api/http/handler ./internal/agent/application ./internal/agent/infrastructure/persistence ./pkg/storage/postgres -run 'Message|Chat|TenantSchema' -count=1`

Expected: failures because `ChatMessage.Visibility`, visibility constants, and the schema column do not exist and the
handler still returns the internal row.

- [ ] **Step 3: Implement the minimal visibility contract**

Add:

```go
const (
    ChatMessageVisibilityUser = "user"
    ChatMessageVisibilityInternal = "internal"
)
```

Add `Visibility string` to `ChatMessage`, default empty values to `user` at persistence, store/read the column, mark only
the tool summary as internal, and skip non-user visibility in `ChatHandler.ListMessages`. Add tenant DDL:

```sql
ALTER TABLE chat_messages
    ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'user';
ALTER TABLE chat_messages DROP CONSTRAINT IF EXISTS chat_messages_visibility_check;
ALTER TABLE chat_messages ADD CONSTRAINT chat_messages_visibility_check
    CHECK (visibility IN ('user', 'internal'));
```

- [ ] **Step 4: Run focused and integration tests and verify GREEN**

Run the focused command from Step 2, then the repository's PostgreSQL integration test command when its test database is
available. Expected: all selected tests pass, historical rows read as `user`, and internal rows remain available to Agent
history but absent from HTTP output.

- [ ] **Step 5: Commit**

```bash
git add api/http/handler/chat_handler.go api/http/handler/chat_handler_test.go internal/agent pkg/storage/postgres
git commit -m '[fix](agent): hide internal chat observations'
```

## Task 2: Redact managed prompts and persist deterministic diagnostic actions

**Files:**

- Modify: `api/http/handler/agent_dto.go`
- Modify: `internal/agent/domain/system_assistant.go`
- Test: `api/http/handler/system_assistant_handler_test.go`
- Test: `internal/agent/domain/system_assistant_test.go`

- [ ] **Step 1: Write failing mapper and report tests**

Assert `dtoToResponse` returns an empty `SystemPrompt` when `ID == domain.SystemAssistantID` or `SystemKey ==
domain.SystemAssistantKey`, while a custom Agent preserves its prompt. Assert two `evidence_unavailable` gaps yield exactly
one non-empty recommended action mentioning Opik and OTEL.

- [ ] **Step 2: Run focused tests and verify RED**

Run: `go test ./api/http/handler ./internal/agent/domain -run 'ManagedAgent|DiagnosticReport' -count=1`

Expected: managed prompt is exposed and `RecommendedActions` is empty.

- [ ] **Step 3: Implement redaction and bounded action mapping**

Redact only the domain-managed identity in `dtoToResponse`. Add a small deterministic map/helper in the domain package,
deduplicate actions, apply existing count/string bounds, and map `DiagnosticGapUnavailable` to an Opik/OTEL dependency
health action. Unknown errors add no invented action.

- [ ] **Step 4: Run tests and verify GREEN**

Run the command from Step 2 and `go test ./api/http/... -count=1`. Expected: all pass and frozen response shapes remain
unchanged apart from the managed prompt value.

- [ ] **Step 5: Commit**

```bash
git add api/http/handler/agent_dto.go api/http/handler/system_assistant_handler_test.go internal/agent/domain
git commit -m '[fix](agent): protect managed prompt and diagnose evidence gaps'
```

## Task 3: Make guest provisioning idempotent and transient-failure bounded

**Files:**

- Create: `internal/iam/infrastructure/persistence/postgres_retry.go`
- Create: `internal/iam/infrastructure/persistence/postgres_retry_test.go`
- Modify: `internal/iam/infrastructure/persistence/onboard_repo.go`
- Modify: `internal/iam/infrastructure/persistence/onboard_repo_test.go`
- Modify: `pkg/constants/iam.go`

- [ ] **Step 1: Write failing classification, retry, and replay tests**

Table-test SQLSTATE `40001`, `40P01`, and connection-class `08xxx` as retryable; constraint and validation states are not.
Use pgx mocks to prove a retryable transaction failure retries within the attempt limit, a permanent error stops after one
attempt, cancellation stops backoff, and replay of the same `githubID` selects the existing user and ensures membership.

- [ ] **Step 2: Run IAM persistence tests and verify RED**

Run: `go test ./internal/iam/infrastructure/persistence -run 'Guest|PostgresRetry' -count=1`

Expected: missing classifier/retry helper and non-idempotent insert behavior fail.

- [ ] **Step 3: Implement minimal bounded retry and idempotent SQL**

Define named attempt/backoff constants in `pkg/constants/iam.go`. Generate the guest identity once in application code as
today. In the repository, wrap the whole transaction in a context-aware retry loop. Use an insert conflict path that returns
the existing guest ID for the same synthetic identity, then idempotently insert membership. Log only operation stage,
attempt, SQLSTATE, and wrapped error metadata; never log tokens or credentials.

- [ ] **Step 4: Verify rollback, retry exhaustion, and focused package GREEN**

Run: `go test ./internal/iam/... -run 'Guest|PostgresRetry' -count=1` and the available PostgreSQL integration test. Expected:
all pass, retries are bounded, and no duplicate durable guest is created.

- [ ] **Step 5: Commit**

```bash
git add internal/iam pkg/constants/iam.go
git commit -m '[fix](iam): make guest provisioning retry-safe'
```

## Task 4: Deploy pinned Opik and OTEL Collector in Demo

**Files:**

- Create: `helm/opik/values-demo.yaml`
- Create: `k8s/opik-otel-collector.yaml`
- Modify: `helm/values-demo.yaml`
- Modify: `.github/workflows/deploy.yml`
- Modify: `scripts/quality/check-deployment-safety-test.sh`
- Modify: `scripts/quality/check-helm-image-rendering-test.sh`
- Modify: `docs/deployment/k3s-demo.md`

- [ ] **Step 1: Write failing deployment contract checks**

Require Opik chart version `2.1.32`, an explicit `opik` namespace/release, resource requests/limits, persistent storage,
backend readiness wait, a non-empty in-cluster `config.opikUrl`, collector OTLP receiver/exporter configuration, and failure
propagation before the Stratum Helm step.

- [ ] **Step 2: Run deployment checks and verify RED**

Run: `bash scripts/quality/check-deployment-safety-test.sh && bash scripts/quality/check-helm-image-rendering-test.sh`

Expected: fail because no Opik release/values/collector exists and Demo `opikUrl` is empty.

- [ ] **Step 3: Add the pinned separate release and collector**

Create a minimal official-chart override for the single-node Demo, with one replica, PVCs, explicit resources, no public
ingress, and secrets sourced from Kubernetes Secrets where required. In CI: add/update the official Opik repo, upgrade the
pinned chart into `opik`, wait for dependencies/backend, apply the versioned collector manifest, wait for collector
readiness, then deploy Stratum with `OPIK_URL=http://opik-backend.opik.svc.cluster.local:8080`. The collector exports to
Opik's private OTLP endpoint. Every command remains blocking and propagates failure.

- [ ] **Step 4: Render and validate GREEN**

Run the Step 2 checks, `helm lint helm`, `helm template stratum ./helm -f helm/values-demo.yaml -f
helm/values-demo-remote-http.yaml`, and render/lint the pinned Opik chart with the repository override. Expected: all pass,
all workload images are pinned according to repository supply-chain rules, and no secret appears in rendered ConfigMaps.

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/deploy.yml helm k8s/opik-otel-collector.yaml scripts/quality docs/deployment/k3s-demo.md
git commit -m '[fix](deploy): enable Opik evidence in demo'
```

## Task 5: Full verification and remote acceptance

**Files:**

- Modify only failing implementation/tests discovered by the verification evidence.
- Create temporary `tmp-*` scripts only when needed; remove them before completion.

- [ ] **Step 1: Run static and focused verification**

Run `gofmt` on changed Go files, `go vet ./...`, `go test -short ./...`, Helm/deployment checks, `git diff --check`, and
`make risk-guardrails`. Expected: zero failures.

- [ ] **Step 2: Run race and integration verification**

Run `go test -v -race -timeout 30s` for affected packages and the Opik 2.1.32 integration flow. Expected: zero races and
the execution evidence mapping tests pass.

- [ ] **Step 3: Verify real local user journeys**

Start required infrastructure/backend/frontend. Execute a tool-using managed-assistant conversation, reload messages via
HTTP and browser, and assert two visible messages while the database retains the internal summary. Verify member Agent GET
returns an empty managed prompt. Exercise guest login repeatedly and verify unique users/memberships and successful token
issuance. Stop all processes and remove temporary files.

- [ ] **Step 4: Deploy and verify the remote Demo**

After CI deployment, use SSH read-only checks to prove Opik dependencies/backend/collector are ready. Execute a new Agent
run through the public API, then verify `/api/agents/executions` returns 200 and contains the execution. Confirm refreshed
chat history and managed prompt redaction through the remote API/browser. Query only aggregate database evidence and do not
print credentials or tokens.

- [ ] **Step 5: Record final evidence and commit any verification-only documentation**

Update the design/operational documentation only with stable verified facts, run the knowledge-deposition gate, and ensure
`git status --short` contains no temporary artifacts. If no documentation changes are needed, do not create an empty commit.
