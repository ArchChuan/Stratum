# Workflow Backend Product Contracts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the immutable input schema, paged catalog/history queries, run ownership, object authorization, structured validation, and safe display events required by the visual workflow product.

**Architecture:** Extend the Workflow domain rather than adding a parallel read model. All tenant data remains in tenant schemas and every repository method carries `tenantID`; application services enforce member ownership and admin capabilities before handlers render responses. Existing run snapshots and monotonic event sequences remain authoritative.

**Tech Stack:** Go 1.25, Gin, pgx v5, PostgreSQL tenant schema, testify

---

## Tasks

### Task 1: Define And Validate Immutable Workflow Inputs

**Files:**

- Modify: `internal/workflow/domain/workflow.go`
- Modify: `internal/workflow/domain/workflow_test.go`
- Modify: `internal/workflow/application/service.go`

- [ ] **Step 1: Write failing domain tests for supported input fields**

Add table-driven tests that construct `InputSchema{TaskLabel: "任务", Fields: ...}` and assert acceptance of `short_text`, `long_text`, `number`, `single_select`, `multi_select`, `boolean`, and `date`. Add failures for duplicate keys, reserved key `task`, missing option values, invalid defaults, and more than `MaxWorkflowInputFields`.

```go
func TestValidateInputSchemaRejectsDuplicateFieldKeys(t *testing.T) {
    schema := domain.InputSchema{Fields: []domain.InputField{
        {Key: "region", Label: "区域", Type: domain.InputFieldShortText},
        {Key: "region", Label: "市场", Type: domain.InputFieldShortText},
    }}
    require.ErrorIs(t, domain.ValidateInputSchema(schema), domain.ErrInvalidInputSchema)
}
```

- [ ] **Step 2: Run the domain test and verify RED**

Run: `go test ./internal/workflow/domain -run 'TestValidateInputSchema' -count=1`
Expected: FAIL because `InputSchema` and `ValidateInputSchema` do not exist.

- [ ] **Step 3: Add the minimal schema types and validation**

Define `InputFieldType`, `InputOption`, `InputField`, and `InputSchema`. Add `InputSchema` to both `Definition` and `Version`, clone it in `cloneInputSchema`, validate it in `NewDefinition`, `UpdateDraft`, and `Publish`, and introduce `ErrInvalidInputSchema`.

```go
type InputSchema struct {
    TaskLabel       string       `json:"task_label"`
    TaskDescription string       `json:"task_description,omitempty"`
    Fields          []InputField `json:"fields,omitempty"`
}

type InputField struct {
    Key         string         `json:"key"`
    Label       string         `json:"label"`
    Type        InputFieldType `json:"type"`
    Required    bool           `json:"required,omitempty"`
    Description string         `json:"description,omitempty"`
    Default     any            `json:"default,omitempty"`
    Options     []InputOption  `json:"options,omitempty"`
}
```

- [ ] **Step 4: Add failing runtime input-validation tests**

Test `ValidateRunInput(schema, input)` for required task, wrong numeric/boolean/list values, unknown option values, unknown fields, and a valid mixed input. The returned error must expose field-keyed issues without embedding the submitted value.

- [ ] **Step 5: Implement `InputValidationError` and run tests**

Implement `InputIssue{Field, Code, Message}` and `InputValidationError{Issues}`. Run: `go test ./internal/workflow/domain -count=1`
Expected: PASS.

- [ ] **Step 6: Commit the domain contract**

```bash
git add internal/workflow/domain/workflow.go internal/workflow/domain/workflow_test.go
git commit -m '[feat](workflow): define immutable run inputs'
```

### Task 2: Persist Input Schemas, Ownership, And Timing

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql`
- Modify: `pkg/storage/postgres/tenant_schema_test.go`
- Modify: `internal/workflow/infrastructure/persistence/store.go`
- Modify: `internal/workflow/infrastructure/persistence/store_integration_test.go`

- [ ] **Step 1: Write a failing historical-schema order test**

Assert that these backfills occur before dependent indexes or queries:

```sql
ALTER TABLE workflow_definitions ADD COLUMN IF NOT EXISTS draft_input_schema_json JSONB NOT NULL DEFAULT '{"task_label":"任务","fields":[]}';
ALTER TABLE workflow_versions ADD COLUMN IF NOT EXISTS input_schema_json JSONB NOT NULL DEFAULT '{"task_label":"任务","fields":[]}';
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS created_by TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_workflow_runs_created_by_created
    ON workflow_runs (created_by, created_at DESC, id DESC);
```

- [ ] **Step 2: Run the schema test and verify RED**

Run: `go test ./pkg/storage/postgres -run Workflow -count=1`
Expected: FAIL because the columns and ownership index are absent.

- [ ] **Step 3: Add idempotent tenant DDL**

Add each new column to `CREATE TABLE`, immediately follow with `ADD COLUMN IF NOT EXISTS`, then add indexes after backfill. Do not create a numbered public migration.

- [ ] **Step 4: Update repository JSON handling**

Marshal schema values before passing them to pgx, persist `created_by`, and scan all public run timestamps. Update every explicit column list; do not use `SELECT *`.

- [ ] **Step 5: Add real PostgreSQL lifecycle assertions**

Extend `TestPgStoreStage1ALifecycleAndTenantIsolation` to publish and read schema, create a run with `CreatedBy`, and assert a different tenant cannot see it. Add an upgrade-order test that provisions a simulated historical tenant schema.

- [ ] **Step 6: Run persistence verification**

Run: `go test ./internal/workflow/infrastructure/persistence ./pkg/storage/postgres -count=1`
Expected without `STRATUM_TEST_POSTGRES_URL`: unit/schema tests PASS and integration package reports the required environment failure. With test PostgreSQL configured, all integration tests PASS.

- [ ] **Step 7: Commit persistence changes**

```bash
git add pkg/storage/postgres/tenant_schema.sql pkg/storage/postgres/tenant_schema_test.go internal/workflow/infrastructure/persistence/store.go internal/workflow/infrastructure/persistence/store_integration_test.go
git commit -m '[feat](workflow): persist input and run ownership'
```

### Task 3: Add Paged Definition, Version, And Run Queries

**Files:**

- Modify: `internal/workflow/domain/port/repositories.go`
- Modify: `internal/workflow/application/service.go`
- Modify: `internal/workflow/application/service_test.go`
- Modify: `internal/workflow/infrastructure/persistence/store.go`
- Modify: `internal/workflow/infrastructure/persistence/store_integration_test.go`
- Modify: `pkg/constants/workflow.go`

- [ ] **Step 1: Write failing port/application tests**

Exercise `ListDefinitions`, `ListVersions`, and `ListRuns` with normalized page values, name/status filters, stable ordering, and member ownership filtering.

```go
type ListRunsQuery struct {
    ActorID     string
    IsAdmin     bool
    DefinitionID string
    Status      domain.RunStatus
    Page        int
    PageSize    int
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/workflow/application -run 'Test.*List' -count=1`
Expected: FAIL because list queries and repository ports do not exist.

- [ ] **Step 3: Add query ports and summary types**

Add focused `DefinitionQueryRepository`, `VersionQueryRepository`, and `RunQueryRepository` interfaces. Return summary DTOs from application, not infrastructure rows. Normalize pagination with `constants.DefaultPageSize` and `constants.MaxPageSize`.

- [ ] **Step 4: Implement stable tenant queries**

Use `ORDER BY updated_at DESC, id DESC` for definitions, `version_no DESC` for versions, and `created_at DESC, id DESC` for runs. Count and list inside `execTenant`. For members, include `created_by=$actor` in both count and list SQL; never fetch all rows and filter in memory.

- [ ] **Step 5: Verify filter and tenant isolation in PostgreSQL**

Create two users and two tenants, then assert member queries cannot count or list another user's or tenant's runs while admin queries list both users in one tenant.

- [ ] **Step 6: Run and commit**

Run: `go test ./internal/workflow/... -count=1`
Expected: PASS.

```bash
git add internal/workflow pkg/constants/workflow.go
git commit -m '[feat](workflow): query workflow catalog and history'
```

### Task 4: Enforce Object-Level Run Authorization

**Files:**

- Create: `internal/workflow/application/authorization.go`
- Create: `internal/workflow/application/authorization_test.go`
- Modify: `internal/workflow/application/service.go`
- Modify: `internal/workflow/application/control.go`
- Modify: `internal/workflow/domain/workflow.go`
- Modify: `api/middleware/error_mapping.go`
- Modify: `api/middleware/error_mapping_workflow_test.go`

- [ ] **Step 1: Write failing ownership tests**

Cover member get, event read, cancel own, cancel other, admin get/control any tenant run, missing actor, and repository lookup failure. Lookup failures must fail closed.

```go
func TestAuthorizeRunMemberCannotReadAnotherUsersRun(t *testing.T) {
    run := &domain.Run{CreatedBy: "user-a"}
    require.ErrorIs(t, authorizeRun(run, Actor{UserID: "user-b", Role: "member"}, RunActionRead), domain.ErrNotFound)
}
```

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/workflow/application -run AuthorizeRun -count=1`
Expected: FAIL because the authorization policy does not exist.

- [ ] **Step 3: Implement a deterministic policy**

Members may read/events/cancel only when `run.CreatedBy == actor.UserID`. Admin and owner may read and control any run in the already-resolved tenant. Return `ErrNotFound` for cross-user reads to avoid object disclosure and `ErrForbidden` for an authenticated user attempting an unsupported action.

- [ ] **Step 4: Apply policy before every read/control**

Pass `Actor{UserID, Role}` into application commands for get, events, cancel, pause, resume, approval, and manual resolution. Do not authorize in handlers or repositories.

- [ ] **Step 5: Map and test errors**

Map `ErrForbidden` to 403 while preserving `ErrNotFound` as 404 and concurrency errors as 409.

- [ ] **Step 6: Run and commit**

Run: `go test ./internal/workflow/application ./api/middleware -count=1`
Expected: PASS.

```bash
git add internal/workflow/application internal/workflow/domain/workflow.go api/middleware
git commit -m '[fix](workflow): enforce run object authorization'
```

### Task 5: Expose Product HTTP Contracts

**Files:**

- Modify: `api/http/dto/workflow.go`
- Create: `api/http/dto/workflow_test.go`
- Modify: `api/http/handler/workflow_handler.go`
- Modify: `api/http/handler/workflow_handler_test.go`
- Modify: `api/http/router.go`
- Modify: `api/http/router_workflow_rbac_test.go`
- Modify: `api/http/contract_test.go`
- Add or update: `api/http/testdata/contracts/*.golden.json`

- [ ] **Step 1: Write failing handler/router tests**

Specify the contracts:

```text
GET /workflows?query=&page=1&page_size=20
GET /workflows/:id/versions?page=1&page_size=20
GET /workflow-runs?definition_id=&status=&page=1&page_size=20
GET /workflow-runs/:id
GET /workflow-runs/:id/events?after_sequence=0
GET /workflow-runs/:id/events/stream?after_sequence=0
```

Expect `{workflows|versions|runs,total,page,page_size}` for collections. Member run reads must no longer carry a blanket admin middleware; application authorization decides ownership.

- [ ] **Step 2: Verify RED**

Run: `go test ./api/http/... -run 'Workflow|Contract' -count=1`
Expected: FAIL because collection routes are missing and current run reads require admin.

- [ ] **Step 3: Add DTOs and thin handlers**

Parse pagination/status filters, extract both tenant and user/role, call application services, and render stable envelopes. Extend create/update requests with `input_schema`. Extend start requests to use `{task, fields}` while preserving `idempotency_key`.

- [ ] **Step 4: Return structured validation issues**

Teach the error middleware to render `InputValidationError` and graph validation details as `{"error":"...","issues":[...]}` without changing the frozen generic error shape for unrelated errors.

- [ ] **Step 5: Update contract goldens and run HTTP tests**

Run: `go test ./api/http/... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit HTTP contracts**

```bash
git add api/http api/middleware
git commit -m '[feat](workflow): expose workflow product APIs'
```

### Task 6: Persist Safe Output And Tool Display Events

**Files:**

- Modify: `internal/workflow/domain/port/repositories.go`
- Modify: `internal/workflow/application/service.go`
- Modify: `internal/workflow/application/dag_scheduler_test.go`
- Modify: `internal/workflow/infrastructure/executor/registry.go`
- Modify: `api/wiring/workflow.go`
- Modify: `api/wiring/workflow_test.go`
- Modify: `pkg/constants/workflow.go`

- [ ] **Step 1: Write failing event tests**

Assert monotonic `workflow.node_output_delta` events with a bounded text payload and completed `workflow.node_tool_step` events containing only `tool_name`, `duration_ms`, and `summary`. Assert secrets and raw argument/result fields are absent.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/workflow/application ./api/wiring -run 'OutputDelta|ToolStep' -count=1`
Expected: FAIL because node execution has no display-event callback.

- [ ] **Step 3: Add callback ports**

Extend `NodeExecutionRequest` with `OnOutputDelta func(string) error`. Use AgentService `ExecuteStream` in the wiring ACL and append bounded delta events through the Workflow event repository. Summarize returned tool calls only after completion; do not persist arguments or raw results.

- [ ] **Step 4: Bound and batch output**

Add named constants for maximum event text and flush interval. Coalesce tokens before persistence so each model token does not cause a database write. Propagate append failures and stop execution rather than showing unpersisted output as durable.

- [ ] **Step 5: Verify ordering, redaction, and failure propagation**

Run: `go test ./internal/workflow/... ./api/wiring -count=1`
Expected: PASS.

- [ ] **Step 6: Commit events**

```bash
git add internal/workflow api/wiring/workflow.go api/wiring/workflow_test.go pkg/constants/workflow.go
git commit -m '[feat](workflow): stream safe node display events'
```

### Task 7: Complete Backend E2E And Guardrails

**Files:**

- Modify: `scripts/test-workflow-e2e.sh`
- Modify: `docs/agent/product.md` only if the implemented contract changes current project facts

- [ ] **Step 1: Add a real-chain E2E case**

Use two member users and one admin in one test tenant. Create and publish a workflow with an input schema, assert invalid input creates no run, start as member A, deny member B detail/events/cancel, allow member A cancel, and allow admin list/control.

- [ ] **Step 2: Add SSE resume assertions**

Capture an event sequence, reconnect with `after_sequence`, and assert later events are returned once with strictly increasing IDs.

- [ ] **Step 3: Run focused verification**

Run: `go vet ./internal/workflow/... ./api/http/...`
Expected: PASS.

Run: `go test -race -timeout 30s ./internal/workflow/... ./api/http/... ./api/middleware/...`
Expected: PASS.

- [ ] **Step 4: Run real E2E and risk guard**

Run: `bash scripts/test-workflow-e2e.sh` with the documented test services.
Expected: PASS with no token or key printed.

Run: `make risk-guardrails`
Expected: `risk regression guard: passed`.

- [ ] **Step 5: Commit E2E coverage**

```bash
git add scripts/test-workflow-e2e.sh docs/agent/product.md
git commit -m '[test](workflow): verify product authorization chain'
```
