# Evaluation Evolution Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the tenant-safe shared revision, query, and human-gated experiment foundation used by Skill, Agent, MCP, and Knowledge evaluation loops.

**Architecture:** `internal/evaluation` owns shared revision metadata, evidence queries, recommendations, and experiment commands. Resource payloads are encrypted through a generic `pkg/storage/objectstore` adapter, while provider-specific snapshot and execution behavior remains behind consumer-side ports wired in `api/wiring`. Existing evaluation endpoints stay compatible while new list and timeline APIs expose safe summaries only.

**Tech Stack:** Go 1.22+, Gin, pgx v5, PostgreSQL tenant schemas, MinIO, AES-256 application encryption, existing tenantdb transaction helpers, table-driven Go tests.

---

## Program Sequence

This is plan 1 of 6 for the approved first release:

1. shared foundation (this plan);
2. Skill adapter and compact entry;
3. Agent immutable revisions and adapter;
4. MCP immutable revisions and adapter;
5. Knowledge immutable revisions and adapter;
6. tenant center frontend and real E2E.

Do not start provider plans until this plan's ports, API contracts, and migration-order tests pass.

## File Map

- `internal/evaluation/domain/resource.go`: supported resource kinds, immutable revision metadata, safe summaries, and validation.
- `internal/evaluation/domain/decision.go`: human-gated experiment actions and decision records.
- `internal/evaluation/domain/experiment.go`: recommendation and safety-stop calculation only; no automatic stable promotion.
- `internal/evaluation/domain/port/evaluation.go`: revision, center-query, and decision repository contracts.
- `internal/evaluation/application/revision_service.go`: atomic encrypted snapshot creation and lookup.
- `internal/evaluation/application/query_service.go`: overview, lists, and resource timeline use cases.
- `internal/evaluation/application/experiment_service.go`: pause, promote, rollback, and safety-stop commands.
- `internal/evaluation/infrastructure/persistence/revision_repository.go`: tenant-scoped revision metadata persistence.
- `internal/evaluation/infrastructure/persistence/query_repository.go`: bounded center queries.
- `internal/evaluation/infrastructure/persistence/experiment_repository.go`: locked idempotent state transitions and decision audit rows.
- `pkg/storage/objectstore/encrypted.go`: context-neutral encrypted MinIO put/get implementation.
- `internal/agent/infrastructure/objectstore/minio.go`: compatibility wrapper over the generic encrypted store.
- `pkg/storage/postgres/tenant_schema.sql`: history-compatible tenant DDL.
- `api/http/dto/evaluation.go`: four-kind validation, query filters, and command DTOs.
- `api/http/handler/evaluation_handler.go`: thin query and command handlers.
- `api/http/router.go`: read/member and command/admin routes.
- `api/wiring/evaluation.go`: construct repositories/services without raw SQL.

### Task 1: Freeze Four-Kind Domain Contracts

**Files:**

- Create: `internal/evaluation/domain/resource.go`
- Create: `internal/evaluation/domain/resource_test.go`
- Modify: `internal/evaluation/domain/evaluation.go`
- Modify: `api/http/dto/evaluation.go`
- Create: `api/http/dto/evaluation_test.go`

- [ ] **Step 1: Write failing table-driven resource-kind and revision validation tests**

Use `internal/evaluation/domain/assertion_test.go` as the test style template. Add tests proving all four kinds validate, an unknown kind fails, IDs are required, and safe summaries reject secret-shaped keys:

```go
func TestResourceKindValidate(t *testing.T) {
 tests := []struct {
  name string
  kind domain.ResourceKind
  wantErr bool
 }{
  {name: "skill", kind: domain.ResourceKindSkill},
  {name: "agent", kind: domain.ResourceKindAgent},
  {name: "mcp", kind: domain.ResourceKindMCP},
  {name: "knowledge", kind: domain.ResourceKindKnowledge},
  {name: "unknown", kind: "workflow", wantErr: true},
 }
 for _, tt := range tests {
  t.Run(tt.name, func(t *testing.T) {
   err := tt.kind.Validate()
   if (err != nil) != tt.wantErr { t.Fatalf("Validate() error = %v", err) }
  })
 }
}
```

- [ ] **Step 2: Run the focused tests and verify failure**

Run: `go test ./internal/evaluation/domain ./api/http/dto -run 'ResourceKind|ResourceRevision|EvaluationResource' -count=1`

Expected: FAIL because the new kinds, revision type, and DTO acceptance do not exist.

- [ ] **Step 3: Add the minimal domain types and validation**

Move `ResourceKind` and `ResourceRef` from `evaluation.go` into `resource.go`, keeping JSON fields unchanged. Implement this closed enum and immutable metadata shape:

```go
const (
 ResourceKindSkill     ResourceKind = "skill"
 ResourceKindAgent     ResourceKind = "agent"
 ResourceKindMCP       ResourceKind = "mcp"
 ResourceKindKnowledge ResourceKind = "knowledge"
)

type ResourceRevision struct {
 ID               string         `json:"id"`
 ResourceKind     ResourceKind   `json:"resource_kind"`
 ResourceID       string         `json:"resource_id"`
 ParentRevisionID string         `json:"parent_revision_id,omitempty"`
 Source           RevisionSource `json:"source"`
 Status           RevisionStatus `json:"status"`
 ContentHash      string         `json:"content_hash"`
 PayloadRef       string         `json:"-"`
 PayloadHash      string         `json:"-"`
 SafeSummary      map[string]any `json:"safe_summary"`
 CreatedBy        string         `json:"created_by"`
 CreatedAt        time.Time      `json:"created_at"`
}
```

`ResourceKind.Validate` must accept only the four constants. `ResourceRef.Validate` must call it. `ResourceRevision.Validate` must require IDs, source, status, content hash, payload reference/hash, and reject safe-summary keys normalized to `password`, `token`, `api_key`, `apikey`, `authorization`, `secret`, `access_token`, or `refresh_token`.

Update all evaluation DTO `oneof=skill` tags to `oneof=skill agent mcp knowledge`.

- [ ] **Step 4: Run domain and DTO tests**

Run: `go test ./internal/evaluation/domain ./api/http/dto -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the domain contract**

```bash
git add internal/evaluation/domain/resource.go internal/evaluation/domain/resource_test.go \
  internal/evaluation/domain/evaluation.go api/http/dto/evaluation.go api/http/dto/evaluation_test.go
git commit -m "feat(evaluation): define shared resource revisions"
```

### Task 2: Extract a Generic Encrypted Object Store

**Files:**

- Create: `pkg/storage/objectstore/encrypted.go`
- Create: `pkg/storage/objectstore/encrypted_test.go`
- Modify: `internal/agent/infrastructure/objectstore/minio.go`
- Modify: `internal/agent/infrastructure/objectstore/minio_test.go`
- Test: `internal/agent/infrastructure/objectstore/minio_integration_test.go`

- [ ] **Step 1: Write failing encrypted put/get tests**

Use `internal/agent/infrastructure/objectstore/minio_test.go` as the mock pattern. The fake client must implement `PutObject` and `GetObject`. Assert plaintext never reaches stored bytes, metadata contains the plaintext SHA-256, `Get` returns the original JSON bytes, and hash/ciphertext tampering fails.

```go
func TestEncryptedStoreRoundTrip(t *testing.T) {
 client := newMemoryObjectClient()
 store := objectstore.NewEncryptedStore(client, "evaluation", [32]byte{1})
 ref, err := store.Put(context.Background(), objectstore.Payload{
  TenantID: "tenant-1", Namespace: "revision", ID: "revision-1",
  Value: map[string]any{"instructions": "safe prompt"},
 })
 if err != nil { t.Fatal(err) }
 if bytes.Contains(client.body, []byte("safe prompt")) { t.Fatal("plaintext persisted") }
 raw, err := store.Get(context.Background(), ref)
 if err != nil { t.Fatal(err) }
 if !bytes.Contains(raw, []byte("safe prompt")) { t.Fatalf("unexpected payload: %s", raw) }
}
```

- [ ] **Step 2: Verify tests fail before extraction**

Run: `go test ./pkg/storage/objectstore ./internal/agent/infrastructure/objectstore -count=1`

Expected: FAIL because `pkg/storage/objectstore` and `Get` do not exist.

- [ ] **Step 3: Implement the generic store and Agent compatibility wrapper**

Define context-neutral types:

```go
type Payload struct { TenantID, Namespace, ID string; Value any }
type Reference struct { URI, SHA256 string; SizeBytes int64 }
type Store interface {
 Put(context.Context, Payload) (Reference, error)
 Get(context.Context, Reference) ([]byte, error)
 Delete(context.Context, Reference) error
}
```

`EncryptedStore.Put` JSON-encodes, hashes, encrypts with `pkg/crypto`, and writes `application/octet-stream`. Object keys are safe tenant/namespace/ID segments plus UUIDv7. `Get` accepts only `object://<configured-bucket>/...`, reads bounded ciphertext, decrypts, verifies SHA-256, and returns bytes. Use the existing Agent store's sanitization before passing trace payload values into the generic store so trace behavior does not change.

Keep `internal/agent/infrastructure/objectstore.Store.Put(port.TracePayload)` as a thin adapter returning `port.TracePayloadRef`; do not make Agent import evaluation.

- [ ] **Step 4: Run unit and real MinIO tests**

Run: `go test ./pkg/storage/objectstore ./internal/agent/infrastructure/objectstore -count=1`

Expected: PASS. If `STRATUM_E2E_MINIO_ENDPOINT` is absent, the integration test must skip with its existing explicit reason.

- [ ] **Step 5: Commit the storage extraction**

```bash
git add pkg/storage/objectstore internal/agent/infrastructure/objectstore
git commit -m "refactor(storage): share encrypted object payloads"
```

### Task 3: Add History-Compatible Revision and Decision DDL

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql`
- Modify: `pkg/storage/postgres/tenant_schema_test.go`
- Modify: `pkg/storage/postgres/tenant_schema_safety_test.go`
- Modify: `pkg/storage/postgres/tenant_schema_integration_test.go`

- [ ] **Step 1: Add failing schema-order tests**

Use `pkg/storage/postgres/tenant_schema_test.go` as the template. Assert `resource_revisions` and `experiment_decisions` exist; every dependent index appears after all `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` backfills; no table stores a plaintext `payload JSONB`; and the existing Skill tables are not dropped.

Add a real PostgreSQL integration case that creates an old tenant schema without the new tables, runs tenant provisioning twice, and verifies both runs succeed with existing Skill evaluation rows preserved.

- [ ] **Step 2: Verify the schema tests fail**

Run: `go test ./pkg/storage/postgres -run 'TenantSchema.*(Revision|Decision|Upgrade|Safety)' -count=1`

Expected: FAIL because the tables are absent.

- [ ] **Step 3: Add DDL in safe order**

Add `resource_revisions` with unique `(resource_kind, resource_id, id)`, content/payload hashes, object reference, safe summary, creator, timestamps, and constrained source/status. Add `experiment_decisions` with experiment FK, action, actor type/ID, prior/new status, metrics JSONB, reason, idempotency key, and timestamp.

Add missing experiment columns using this order before indexes or queries use them:

```sql
ALTER TABLE evaluation_experiments ADD COLUMN IF NOT EXISTS state_version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE evaluation_experiments ADD COLUMN IF NOT EXISTS recommendation TEXT NOT NULL DEFAULT 'hold';
ALTER TABLE evaluation_experiments ADD COLUMN IF NOT EXISTS safety_stopped BOOL NOT NULL DEFAULT false;
```

Use `CHECK` constraints for four resource kinds on newly created tables. Do not add a constraint to historical tables until the integration test proves existing values are compatible.

- [ ] **Step 4: Run schema and migration guards**

Run: `go test ./pkg/storage/postgres -count=1`

Run: `bash scripts/quality/check-migration-boundaries-test.sh`

Expected: PASS for both; integration tests may skip only when their documented PostgreSQL test DSN is absent.

- [ ] **Step 5: Commit the tenant DDL**

```bash
git add pkg/storage/postgres/tenant_schema.sql pkg/storage/postgres/tenant_schema_test.go \
  pkg/storage/postgres/tenant_schema_safety_test.go pkg/storage/postgres/tenant_schema_integration_test.go
git commit -m "feat(evaluation): persist resource revisions and decisions"
```

### Task 4: Persist Atomic Encrypted Revisions

**Files:**

- Modify: `internal/evaluation/domain/port/evaluation.go`
- Create: `internal/evaluation/application/revision_service.go`
- Create: `internal/evaluation/application/revision_service_test.go`
- Create: `internal/evaluation/infrastructure/persistence/revision_repository.go`
- Create: `internal/evaluation/infrastructure/persistence/revision_repository_integration_test.go`

- [ ] **Step 1: Write service failure-order tests**

Use `internal/evaluation/application/optimization_service_test.go` as the fake style. Cover successful creation, object-store failure, repository failure with object cleanup, duplicate idempotency return, cross-kind validation, and no safe-summary secret keys.

The service input and port contracts are:

```go
type CreateRevisionInput struct {
 ResourceKind domain.ResourceKind
 ResourceID, ParentRevisionID, CreatedBy, IdempotencyKey string
 Source domain.RevisionSource
 Payload any
 SafeSummary map[string]any
}

type RevisionRepository interface {
 Create(context.Context, string, domain.ResourceRevision, string) (domain.ResourceRevision, bool, error)
 Get(context.Context, string, domain.ResourceRef) (domain.ResourceRevision, bool, error)
}
```

- [ ] **Step 2: Verify tests fail**

Run: `go test ./internal/evaluation/application ./internal/evaluation/infrastructure/persistence -run 'Revision' -count=1`

Expected: FAIL because the service and repository are absent.

- [ ] **Step 3: Implement object-first, metadata-second creation**

Hash canonical JSON before upload. Store the encrypted payload first, then insert metadata and idempotency key in the tenant transaction. If the insert fails, call `Delete` on the generic object store with a bounded cleanup context; return the insert error even if cleanup also fails, joining the cleanup error for diagnostics. A duplicate idempotency key returns the existing revision and `created=false`.

Repository SQL must execute only through `tenantdb.ExecTenant`; it must never accept schema names from callers.

- [ ] **Step 4: Run unit, integration, and leak checks**

Run: `go test ./internal/evaluation/application ./internal/evaluation/infrastructure/persistence -run 'Revision' -count=1`

Run: `rg -n '(password|token|api_key|authorization|secret)' internal/evaluation --glob '*.go'`

Expected: tests PASS; search hits are only redaction/validation constants and test fixtures with synthetic values.

- [ ] **Step 5: Commit revision persistence**

```bash
git add internal/evaluation/domain/port/evaluation.go internal/evaluation/application/revision_service.go \
  internal/evaluation/application/revision_service_test.go \
  internal/evaluation/infrastructure/persistence/revision_repository.go \
  internal/evaluation/infrastructure/persistence/revision_repository_integration_test.go
git commit -m "feat(evaluation): store encrypted immutable revisions"
```

### Task 5: Separate Recommendations from Human Experiment Commands

**Files:**

- Create: `internal/evaluation/domain/decision.go`
- Create: `internal/evaluation/domain/decision_test.go`
- Modify: `internal/evaluation/domain/experiment.go`
- Modify: `internal/evaluation/domain/experiment_test.go`
- Modify: `internal/evaluation/application/experiment_service.go`
- Modify: `internal/evaluation/application/experiment_service_test.go`
- Modify: `internal/evaluation/infrastructure/persistence/experiment_repository.go`
- Create: `internal/evaluation/infrastructure/persistence/experiment_command_integration_test.go`

- [ ] **Step 1: Write failing human-gate state tests**

Use the current experiment tests as the template. Prove good metrics return `promote` as a recommendation without changing stable revision or completing the experiment. Prove a security violation immediately marks the experiment safety-stopped and sets canary percent to zero. Prove only an admin `Promote` command completes the experiment. Cover pause, rollback, stale `state_version`, duplicate idempotency, and commands on terminal states.

```go
func TestGoodMetricsRecommendButDoNotPromote(t *testing.T) {
 experiment := runningExperiment()
 next, recommendation := experiment.Recommend(goodMetrics(), experiment.Policy)
 if recommendation != DecisionPromote { t.Fatalf("recommendation = %s", recommendation) }
 if next.Status != ExperimentRunning { t.Fatalf("status = %s", next.Status) }
}
```

- [ ] **Step 2: Verify focused tests fail**

Run: `go test ./internal/evaluation/domain ./internal/evaluation/application -run 'Experiment|HumanGate|SafetyStop' -count=1`

Expected: FAIL because current `Decide` automatically completes at 100 percent and command methods are absent.

- [ ] **Step 3: Implement recommendation and explicit commands**

Replace automatic promotion behavior with `Recommend`. It may advance recommended canary stages only after evidence gates, but never set `ExperimentCompleted`. Hard guardrail violations return `DecisionRollback` with `SafetyStopped=true` and stage zero.

Add application methods with an `ExperimentCommand` containing actor, reason, idempotency key, and expected state version:

```go
func (s *ExperimentService) Pause(ctx context.Context, tenantID, id string, cmd ExperimentCommand) (domain.Experiment, error)
func (s *ExperimentService) Promote(ctx context.Context, tenantID, id string, cmd ExperimentCommand) (domain.Experiment, error)
func (s *ExperimentService) Rollback(ctx context.Context, tenantID, id string, cmd ExperimentCommand) (domain.Experiment, error)
```

Promotion requires the current persisted recommendation to be `promote`, the experiment to be running, and no safety stop. Repository commands lock the experiment row, compare `state_version`, insert `experiment_decisions`, update deployment, and commit atomically.

- [ ] **Step 4: Run state and transaction tests**

Run: `go test ./internal/evaluation/domain ./internal/evaluation/application ./internal/evaluation/infrastructure/persistence -run 'Experiment|Decision|Command' -count=1`

Expected: PASS, including two concurrent command attempts where one succeeds and one returns the stale-state domain error.

- [ ] **Step 5: Commit human gates**

```bash
git add internal/evaluation/domain/decision.go internal/evaluation/domain/decision_test.go \
  internal/evaluation/domain/experiment.go internal/evaluation/domain/experiment_test.go \
  internal/evaluation/application/experiment_service.go internal/evaluation/application/experiment_service_test.go \
  internal/evaluation/infrastructure/persistence/experiment_repository.go \
  internal/evaluation/infrastructure/persistence/experiment_command_integration_test.go
git commit -m "feat(evaluation): enforce human experiment gates"
```

### Task 6: Add Center Queries and Resource Timeline

**Files:**

- Modify: `internal/evaluation/domain/port/evaluation.go`
- Create: `internal/evaluation/application/query_service.go`
- Create: `internal/evaluation/application/query_service_test.go`
- Create: `internal/evaluation/infrastructure/persistence/query_repository.go`
- Create: `internal/evaluation/infrastructure/persistence/query_repository_integration_test.go`

- [ ] **Step 1: Write failing bounded-query tests**

Use `internal/evaluation/infrastructure/persistence/suite_repository_integration_test.go` as the tenant integration template. Cover overview counts, resource-kind/status filters, descending stable ordering, maximum page size, a timeline containing revision/run/candidate/experiment/decision events, safe summaries only, and the same IDs in two tenant schemas returning different rows.

Define one query contract rather than one repository per tab:

```go
type CenterFilter struct {
 ResourceKind domain.ResourceKind
 ResourceID, Status, Cursor string
 Limit int
}
type CenterQueryRepository interface {
 Overview(context.Context, string) (domain.EvaluationOverview, error)
 ListResources(context.Context, string, CenterFilter) (domain.ResourcePage, error)
 ListSuites(context.Context, string, CenterFilter) (domain.SuitePage, error)
 ListRuns(context.Context, string, CenterFilter) (domain.RunPage, error)
 ListCandidates(context.Context, string, CenterFilter) (domain.CandidatePage, error)
 ListExperiments(context.Context, string, CenterFilter) (domain.ExperimentPage, error)
 Timeline(context.Context, string, domain.ResourceKind, string, CenterFilter) (domain.TimelinePage, error)
}
```

- [ ] **Step 2: Verify query tests fail**

Run unit tests:

`go test ./internal/evaluation/application ./internal/evaluation/infrastructure/persistence -run 'Center|Timeline|List.*Evaluation' -count=1`

Run PostgreSQL integration tests:

`go test -tags=integration ./internal/evaluation/infrastructure/persistence -run 'Center|Timeline|List.*Evaluation' -count=1`

Expected: FAIL because query types and repository are absent. Once the integration test compiles, it must explicitly skip when
`TEST_DATABASE_URL` is absent.

- [ ] **Step 3: Implement bounded keyset queries**

Clamp limits to `1..100`, default `20`. Encode cursors from `(created_at,id)` rather than accepting SQL fragments or offsets. Validate filters before repository calls. Query resource rows from `resource_revisions` joined to deployments and latest run aggregates. Timeline uses `UNION ALL` over safe event projections and never selects payload references, raw case outputs, feedback outcomes, or decision metric bodies.

- [ ] **Step 4: Run query and isolation tests**

Run unit tests:

`go test ./internal/evaluation/application ./internal/evaluation/infrastructure/persistence -run 'Center|Timeline|List.*Evaluation' -count=1`

Run PostgreSQL integration tests:

`go test -tags=integration ./internal/evaluation/infrastructure/persistence -run 'Center|Timeline|List.*Evaluation' -count=1`

Expected: PASS. When `TEST_DATABASE_URL` is absent, the integration test must explicitly skip with its documented missing-DSN
reason; the unit command still runs normally.

- [ ] **Step 5: Commit center queries**

```bash
git add internal/evaluation/domain/port/evaluation.go internal/evaluation/application/query_service.go \
  internal/evaluation/application/query_service_test.go \
  internal/evaluation/infrastructure/persistence/query_repository.go \
  internal/evaluation/infrastructure/persistence/query_repository_integration_test.go
git commit -m "feat(evaluation): query center evidence and timeline"
```

### Task 7: Expose Compatible HTTP Queries and Commands

**Files:**

- Modify: `api/http/dto/evaluation.go`
- Modify: `api/http/handler/evaluation_handler.go`
- Modify: `api/http/handler/evaluation_handler_test.go`
- Modify: `api/http/router.go`
- Create: `api/http/router_evaluation_rbac_test.go`
- Modify: `api/http/contract_test.go`
- Create: `api/http/testdata/contracts/get_evaluations_overview.golden.json` and one fixture per new route via `make record-contracts`
- Modify: `api/wiring/evaluation.go`

- [ ] **Step 1: Write failing handler and authorization tests**

Use `api/http/handler/evaluation_handler_test.go` for handler fakes and `api/http/router_workflow_rbac_test.go` as the role-test template. Cover all seven GET routes, four command routes, filter propagation, malformed cursor, member read access, member command denial, inactive-admin denial, cross-tenant not-found, and the frozen `{"error":"..."}` envelope.

Command bodies use:

```json
{
  "reason": "候选离线与在线指标均通过，人工确认晋级",
  "idempotency_key": "promotion-request-1",
  "expected_state_version": 3
}
```

- [ ] **Step 2: Verify HTTP tests fail**

Run: `go test ./api/http/... -run 'Evaluation|ExperimentCommand|EvaluationCenter' -count=1`

Expected: FAIL with missing handler methods and routes.

- [ ] **Step 3: Add thin handlers, routes, and wiring**

Handlers bind DTOs, read tenant/user IDs from context, invoke one application method, and render. They do not calculate metrics or issue SQL. Register member-readable GET routes and active-admin command routes exactly as approved in the design. Keep every existing endpoint and response field unchanged.

Construct `RevisionService`, `QueryService`, and the expanded `ExperimentService` in `api/wiring/evaluation.go`; SQL remains in persistence packages.

- [ ] **Step 4: Update and run API contracts**

Run: `make record-contracts`

Inspect the diff and confirm it adds only the new routes/schemas without changing existing response bodies. Then run:

Run: `go test ./api/http/... ./api/wiring -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the API surface**

```bash
git add api/http/dto/evaluation.go api/http/handler/evaluation_handler.go \
  api/http/handler/evaluation_handler_test.go api/http/router.go api/http/router_evaluation_rbac_test.go \
  api/http/contract_test.go api/http/testdata/contracts api/wiring/evaluation.go
git commit -m "feat(api): expose evaluation evolution center"
```

### Task 8: Foundation Verification and Plan Checkpoint

**Files:**

- Modify: `docs/superpowers/plans/2026-07-22-evaluation-evolution-foundation.md` (checkbox status only)
- Create: `docs/superpowers/plans/2026-07-22-evaluation-evolution-skill.md` after the foundation contracts pass

- [ ] **Step 1: Run focused correctness suites**

Run:

```bash
go test ./internal/evaluation/... ./pkg/storage/objectstore ./pkg/storage/postgres ./api/http/... ./api/wiring -count=1
```

Expected: PASS, with only documented external-service skips.

- [ ] **Step 2: Run repository risk and architecture guards**

Run:

```bash
bash scripts/quality/risk-regression-guard.sh --explain
make risk-guardrails
```

Expected: PASS. The explanation must identify tenant DDL, logs/errors, external dependency, and deployment/supply-chain checks touched by this change.

- [ ] **Step 3: Run full Go verification**

Run:

```bash
go vet ./...
go test -short ./...
go test -race -timeout 30s ./...
```

Expected: PASS. If the full race suite exceeds the repository's established timeout for an integration package, record the exact package and rerun that package with its repository-approved timeout; do not omit it silently.

- [ ] **Step 4: Inspect secrets, schema order, and worktree boundary**

Run:

```bash
git diff origin/main...HEAD --check
bash scripts/quality/check-migration-boundaries.sh
git status --short
```

Expected: no whitespace errors, no public migration referencing tenant tables, and no untracked temporary scripts or runtime payloads.

- [ ] **Step 5: Commit only the completed plan status**

```bash
git add docs/superpowers/plans/2026-07-22-evaluation-evolution-foundation.md
git commit -m "docs(evaluation): complete foundation plan"
```

Stop at this checkpoint. Review the actual foundation contracts and runtime evidence before writing the Skill adapter plan; do not guess later provider signatures ahead of the shared implementation.
