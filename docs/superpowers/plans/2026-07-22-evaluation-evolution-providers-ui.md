# Evaluation Evolution Providers and Center Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the shared evaluation foundation to Skill, Agent, MCP, and Knowledge, then deliver a tenant-admin evaluation and evolution center with human-gated decisions and real end-to-end evidence.

**Architecture:** `internal/evaluation` defines and owns the consumer-side ports in `internal/evaluation/domain/port`; thin `api/wiring` adapters implement those ports. Immutable revisions are resolved before execution and no provider reads mutable configuration after assignment. The frontend uses one typed evaluation model/API/hooks and a dense responsive center, while the existing Skill panel becomes a compact entry backed by the same endpoints.

**Tech Stack:** Go `1.25.12` from `go.mod`; React `18.3`, Ant Design `5.20`, Vite `6.4`, TypeScript, React Router `6.26`, Axios, Zod, and Vitest from `web/package.json`; Gin, pgx/PostgreSQL tenant schemas, existing Opik/OTel and encrypted MinIO adapters.

---

## File Map

- `internal/evaluation/domain/port/evaluation.go`: existing shared `ResourceAdapter.ExecuteRevision`, `CandidateCreator.LoadOptimizableSnapshot/CreateCandidate`, `RevisionRepository`, and query contracts; extend only here when a provider capability is missing.
- `internal/skill/application/version_service.go`: preserve existing Skill revision semantics while exposing the shared adapter contract.
- `api/wiring/evaluation.go`: existing Skill adapter (`skillCandidateManager`) and registration; add thin Agent/MCP/Knowledge adapters here. Provider `domain/port` packages must not declare evaluation consumer-side interfaces.
- `pkg/storage/postgres/tenant_schema.sql`: shared `resource_revisions` storage owned by evaluation; provider tables are not duplicated and no history table is deleted.
- `web/src/modules/evaluation/model/evaluation.ts`: four-kind schemas, safe summaries, pages, timeline, gates, and decisions.
- `web/src/modules/evaluation/api/evaluation.api.ts`: all center reads and commands through `web/src/services/client.ts`.
- `web/src/modules/evaluation/hooks/useEvaluationCenter.ts`: cancellation-safe query/action orchestration.
- `web/src/modules/evaluation/pages/EvaluationCenterPage.tsx`: page shell and first-viewport filters/action.
- `web/src/modules/evaluation/components/{EvaluationOverview,ResourceTable,RunDrawer,CandidateDrawer,ExperimentDrawer,TimelineDrawer}.tsx`: focused components, each under 200 lines.
- `web/src/modules/evaluation/routes.tsx`: authenticated center route.
- `web/src/app/layout/menu.config.tsx`, `web/src/app/router.tsx`: administrator menu and route registration.
- `web/src/modules/skill/pages/SkillWorkspacePage.tsx`, `web/src/modules/evaluation/components/SkillEvaluationPanel.tsx`: compact resource-scoped entry/link.
- `e2e/evaluation-evolution/*` and `scripts/e2e/evaluation-evolution.sh`: real API/browser/database/Opik/Collector/MinIO workflow.

## Delivery Rules

- Work strictly task-by-task with a fresh implementation subagent, then a spec-compliance review and a quality review before the next task.
- Delivery mapping: foundation package 1 is complete; Task 1 is package 2 (Skill), Task 2 is package 3 (Agent), Task 3 is package 4 (MCP), Task 4 is package 5 (Knowledge), and Tasks 5-7 together are package 6 (frontend UI plus E2E verification).
- Every implementation task starts with a failing test and ends with a focused test plus a commit.
- Use `bash scripts/quality/risk-regression-guard.sh --explain` before the first code change and `make risk-guardrails` before the final task commit.
- Admin identity is read from authenticated request context; command bodies never carry actor identity. Cross-tenant IDs return not found.
- Do not expose raw prompts, tool arguments, retrieved content, credentials, or encrypted payloads. UI strings are Chinese; success notices last at most two seconds and errors remain visible.

### Task 1: Skill Shared-Center Adapter and Compact Entry

**Files:**

- Modify: `internal/evaluation/domain/port/evaluation.go` (reuse existing `ResourceAdapter` and `CandidateCreator`; add only missing shared methods and update mocks)
- Modify: `internal/skill/application/version_service.go`
- Modify: `api/wiring/evaluation.go` (reuse existing `skillCandidateManager`; do not register a second Skill adapter)
- Test: `internal/skill/application/version_service_test.go`
- Create: `api/wiring/evaluation_skill_adapter_test.go`
- Modify: `web/src/modules/evaluation/components/SkillEvaluationPanel.tsx`
- Modify: `web/src/modules/skill/pages/SkillWorkspacePage.tsx`
- Test: `web/src/modules/evaluation/components/SkillEvaluationPanel.test.tsx`

- [ ] **Step 1: Write failing adapter tests**

Add table-driven tests proving a published Skill revision resolves by tenant/resource/revision, an unpublished revision is rejected, candidate patches only alter instructions and safe runtime parameters, and the returned summary contains no secret-shaped keys. Use the existing `internal/skill/application/version_service_test.go` fixture style and assert the adapter delegates without importing Skill infrastructure into evaluation.

- [ ] **Step 2: Run the focused tests and verify failure**

Run: `go test ./internal/skill/application ./api/wiring -run 'EvaluationSkill|Candidate|PublishedRevision' -count=1`

Expected: FAIL with undefined `ResourceAdapter.ResolveRevision`/`SafeSummary` and missing `EvaluationHandler` mock expectations for the Skill adapter.

- [ ] **Step 3: Implement the Skill adapter and wiring**

Extend `internal/evaluation/domain/port/evaluation.go` with these exact shared methods, implemented by the evaluation application caller and exercised by mocks in `internal/evaluation/application/*_test.go`:

```go
ResolveRevision(context.Context, string, domain.ResourceRef) (domain.ResourceRevision, error)
SafeSummary(context.Context, string, domain.ResourceRef) (map[string]any, error)
```

Reuse the existing `skillCandidateManager.LoadOptimizableSnapshot/CreateCandidate` and `ResourceAdapter.ExecuteRevision` registration in `api/wiring/evaluation.go`; the Skill wiring implementation calls `VersionService`. Validate `ResourceKindSkill`, require tenant ID on every call, and reject candidate patches containing permissions, secret, destination, or requirements changes. Do not create a provider-side evaluation port.

- [ ] **Step 4: Migrate the Skill panel to a center link**

Keep existing create/run behavior compatible, replace optimization/experiment duplication with a compact status summary and a `打开评测与进化中心` link carrying `kind=skill` and `resource_id`. Preserve admin gating and published-revision warning. Add tests for member read access, admin command visibility, and link query parameters.

- [ ] **Step 5: Run tests and commit**

Run: `go test ./internal/skill/application ./api/wiring -run 'EvaluationSkill|Candidate|PublishedRevision' -count=1` and `npm --prefix web run test -- --run src/modules/evaluation/components/SkillEvaluationPanel.test.tsx`

Expected: PASS with no raw payload assertions failing.

```bash
  git add internal/evaluation/domain/port internal/skill/application api/wiring/evaluation.go api/wiring/evaluation_skill_adapter_test.go \
  web/src/modules/evaluation/components/SkillEvaluationPanel.tsx \
  web/src/modules/skill/pages/SkillWorkspacePage.tsx
git commit -m "feat(evaluation): connect Skill to shared center"
```

### Task 2: Agent Immutable Revision, Adapter, and Bounded Candidate

**Files:**

- Create: `internal/agent/domain/agent_revision.go` (snapshot value object only)
- Create: `internal/agent/domain/agent_revision_test.go`
- Modify: `internal/agent/application/agent_service.go` (snapshot/execute calls used by wiring)
- Create: `api/wiring/evaluation_agent_adapter.go`
- Modify: `api/wiring/evaluation.go`
- Modify: `internal/evaluation/domain/port/evaluation.go` only if a shared adapter method is missing; update all generated/manual mocks

- [ ] **Step 1: Add failing domain and service tests**

Before writing the tests, run `rg -n 'type AgentService|func \(.*AgentService.*(Execute|Get|Create)|ResourceAdapter|CandidateCreator' internal/agent internal/evaluation api/wiring`. Use the discovered service methods and these shared signatures as the contract (adding them to `internal/evaluation/domain/port/evaluation.go` only if absent):

```go
ExecuteRevision(context.Context, string, domain.ResourceRef, domain.EvalCase) (port.ExecutionResult, error)
LoadOptimizableSnapshot(context.Context, string, domain.ResourceRef) (map[string]any, error)
CreateCandidate(context.Context, string, domain.ResourceRef, domain.CandidatePatch) (domain.ResourceRef, error)
```

Test deterministic snapshot hashes for prompt/model/max-iterations/binding state, rejection of a new Skill/MCP/Knowledge binding or permission widening, and candidate idempotency. Test that a revision resolves only for its tenant and that failed object persistence aborts the transaction.

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/agent/domain ./internal/agent/application ./internal/agent/infrastructure/persistence -run 'Revision|Candidate|TenantIsolation' -count=1`

Expected: FAIL with undefined `AgentRevision`, missing snapshot service methods, and missing adapter implementations for the shared `ResourceAdapter`/`CandidateCreator` symbols.

- [ ] **Step 3: Implement immutable Agent snapshots through evaluation ownership**

Snapshot only existing authorized bindings and safe execution parameters. Call the existing evaluation `RevisionRepository`/`RevisionObjectStore` from the evaluation application service; do not add an Agent revision repository or Agent-owned table. The Agent adapter only loads a snapshot and executes it. Use row locks and idempotency keys already provided by evaluation persistence.

- [ ] **Step 4: Implement the evaluation adapter**

The wiring adapter resolves the exact revision before invoking the existing Agent execution application. Candidate generation accepts prompt/model/max-iterations/binding-enabled changes only and returns a safe diff. Provider errors propagate as failed evaluation evidence; they never become empty successful candidates.

- [ ] **Step 5: Verify schema order and commit**

Run: `go test ./internal/agent/... ./pkg/storage/postgres -run 'Revision|Candidate|TenantIsolation|TenantSchema' -count=1`

Expected: PASS; integration tests skip only when `TEST_DATABASE_URL` is absent.

```bash
git add internal/agent api/wiring/evaluation_agent_adapter.go api/wiring/evaluation.go internal/evaluation/domain/port/evaluation.go
git commit -m "feat(evaluation): add Agent revisions and bounded candidates"
```

### Task 3: MCP Revision, Contract Evaluator, and Adapter

**Files:**

- Create: `internal/mcp/domain/mcp_revision_test.go`
- Create: `internal/mcp/application/contract_evaluator.go`
- Create: `internal/mcp/application/contract_evaluator_test.go`
- Create: `api/wiring/evaluation_mcp_adapter.go`
- Modify: `api/wiring/evaluation.go`
- Modify: `internal/evaluation/domain/port/evaluation.go` only if `ExecuteRevision`/candidate contracts need a shared extension

- [ ] **Step 1: Write failing MCP tests**

Test revision content includes non-secret server identity, encrypted runtime reference, enabled tools, timeout, bounded retries, and a hash of discovered tool schemas. Test evaluator cases for unavailable tool, malformed output, schema drift, timeout, retry exhaustion, and successful invocation. Assert credentials and transport destinations never enter summaries.

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/mcp/... -run 'Revision|Contract|SchemaDrift' -count=1`

Expected: FAIL with undefined `mcpContractSnapshot`, missing `EvaluateContract(context.Context, mcpContractSnapshot, domain.EvalCase) (port.ExecutionResult, error)`, and no `CandidateCreator` implementation in `api/wiring/evaluation_mcp_adapter.go`.

- [ ] **Step 3: Implement contract evaluator against evaluation-owned revisions**

Define private `mcpContractSnapshot` in the adapter package and serialize it as the `Payload` of evaluation's `domain.ResourceRevision`; never persist it in MCP. Implement `ExecuteRevision(context.Context, string, domain.ResourceRef, domain.EvalCase) (port.ExecutionResult, error)` and `CandidateCreator.LoadOptimizableSnapshot/CreateCandidate` in `api/wiring/evaluation_mcp_adapter.go`, delegating contract checks to `internal/mcp/application/contract_evaluator.go`. Contract evaluation must record safe status, latency, and error category; it must not log/render upstream bodies. Candidate patches may change only existing tool enablement, timeout within configured bounds, and finite retry policy.

- [ ] **Step 4: Wire the adapter and reuse shared migration checks**

Register a thin adapter in `api/wiring/evaluation.go`; do not add MCP revision tables or DDL. Extend the existing evaluation tenant-schema tests only when a shared column is required, proving old tenant schemas provision twice without losing MCP rows.

- [ ] **Step 5: Run and commit**

Run: `go test ./internal/mcp/... ./api/wiring -run 'Revision|Contract|SchemaDrift|TenantIsolation' -count=1`

Expected: PASS.

```bash
git add internal/mcp api/wiring/evaluation_mcp_adapter.go api/wiring/evaluation.go internal/evaluation/domain/port/evaluation.go
git commit -m "feat(evaluation): evaluate MCP contracts safely"
```

### Task 4: Knowledge Revision and Retrieval Evaluator

**Files:**

- Create: `internal/knowledge/domain/knowledge_revision_test.go`
- Create: `internal/knowledge/application/retrieval_evaluator.go`
- Create: `internal/knowledge/application/retrieval_evaluator_test.go`
- Create: `api/wiring/evaluation_knowledge_adapter.go`
- Modify: `api/wiring/evaluation.go`
- Modify: `internal/evaluation/domain/port/evaluation.go` only if a shared adapter method is missing

- [ ] **Step 1: Write failing retrieval tests**

Test immutable workspace/document-set/content hash and embedding/reranking identity. Test relevance, citation correctness, no-answer behavior, score threshold, top-K, reranking, and query-rewrite candidate bounds. Assert retrieved document content is held in evaluator memory only and never returned by center queries.

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/knowledge/... -run 'Revision|Retrieval|Citation|NoAnswer' -count=1`

Expected: FAIL with undefined `knowledgeRetrievalSnapshot`, missing `EvaluateRetrieval(context.Context, knowledgeRetrievalSnapshot, domain.EvalCase) (port.ExecutionResult, error)`, and no `CandidateCreator` implementation in `api/wiring/evaluation_knowledge_adapter.go`.

- [ ] **Step 3: Implement evaluator against evaluation-owned revisions**

Define private `knowledgeRetrievalSnapshot` in the adapter package and serialize it as evaluation `domain.ResourceRevision.Payload`; never persist it in Knowledge. Implement `ExecuteRevision(context.Context, string, domain.ResourceRef, domain.EvalCase) (port.ExecutionResult, error)` and `CandidateCreator.LoadOptimizableSnapshot/CreateCandidate` in `api/wiring/evaluation_knowledge_adapter.go`, delegating retrieval checks to `internal/knowledge/application/retrieval_evaluator.go`. Candidate generation may alter top-K, threshold, reranking, and query-rewrite settings only; it cannot add/delete/rewrite documents or change tenant ownership. Dependency failures produce failed samples and preserve stable serving.

- [ ] **Step 4: Wire and reuse evaluation migration-order tests**

Register the adapter and extend only the shared evaluation migration tests when required; do not add a Knowledge revision table. Test historical tenant provisioning and cross-tenant lookup behavior.

- [ ] **Step 5: Run and commit**

Run: `go test ./internal/knowledge/... ./api/wiring -run 'Revision|Retrieval|Citation|NoAnswer|TenantIsolation' -count=1`

Expected: PASS.

```bash
git add internal/knowledge api/wiring/evaluation_knowledge_adapter.go api/wiring/evaluation.go internal/evaluation/domain/port/evaluation.go
git commit -m "feat(evaluation): add Knowledge retrieval evaluator"
```

Tasks 5 and 6 together are delivery package 6 from design section 12: the frontend client/page. Task 7 is package 6's verification and evidence closeout, not a new product package.

### Task 5: Unified Frontend Center Model, API, Hooks, Routes, and Permissions

This task extends the existing `api/http/dto/evaluation.go`, `api/http/handler/evaluation_handler.go`, `api/http/router.go`, `api/http/testdata/contracts`, `internal/evaluation/application/query_service.go`, and `api/wiring/evaluation.go`. It must not recreate routes or handlers already delivered by the foundation.

**Files:**

- Modify: `web/src/modules/evaluation/model/evaluation.ts`
- Modify: `web/src/modules/evaluation/model/evaluation.test.ts`
- Modify: `web/src/modules/evaluation/api/evaluation.api.ts` (extend existing create/run/optimization/experiment/feedback client)
- Create: `web/src/modules/evaluation/api/evaluation.api.test.ts`
- Create: `web/src/modules/evaluation/hooks/useEvaluationCenter.ts`
- Create: `web/src/modules/evaluation/hooks/useEvaluationCenter.test.ts`
- Create: `web/src/modules/evaluation/routes.tsx`
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/app/layout/menu.config.tsx`
- Modify: `web/src/app/layout/__tests__/menu.config.test.tsx`

- [ ] **Step 1: Write failing model/API/permission tests**

Add Zod fixtures for all four `ResourceKind` values, overview/pages/timeline, safe candidate diffs, experiment gates, and frozen `{"error":"..."}` errors. Mock the existing Axios client and assert GETs use cursors/filters, commands omit actor identity, and member users can read while only tenant admins see command controls.

- [ ] **Step 2: Verify failure**

Run: `npm --prefix web run test -- --run src/modules/evaluation/model/evaluation.test.ts src/modules/evaluation/api/evaluation.api.test.ts src/app/layout/__tests__/menu.config.test.tsx`

Expected: FAIL because the unified schemas, API methods, route, and menu entry are absent.

- [ ] **Step 3: Extend the existing model/API/hooks**

Extend existing Zod models and `evaluationApi` methods for overview, resources, suites, runs, candidates, experiments, timeline, reject, pause, promote, and rollback. In parallel extend existing DTO/handler/router/contract tests for four `ResourceKind` values and query filters; call the existing `QueryService` methods rather than introducing another query service. Use `client.ts` only, cancellation flags in every async effect, and error extraction that preserves the Chinese frozen error message. Expose `canManageEvaluation` from authenticated tenant role; never infer admin from resource payloads.

- [ ] **Step 4: Register route and menu**

Add `/evaluations` to private routes and `评测与进化` to the tenant navigation. Keep the entry visible to tenant members for read access; hide new-evaluation and decision commands for non-admins. Update open-key resolution and route tests.

- [ ] **Step 5: Run frontend tests and commit**

Run: `npm --prefix web run test -- --run src/modules/evaluation/model/evaluation.test.ts src/modules/evaluation/api/evaluation.api.test.ts src/modules/evaluation/hooks/useEvaluationCenter.test.ts src/app/layout/__tests__/menu.config.test.tsx`

Expected: PASS with no `localStorage` token access and no raw `fetch` usage.

```bash
git add web/src/modules/evaluation web/src/app/router.tsx web/src/app/layout
git commit -m "feat(web): add evaluation center client model"
```

The HTTP extension is complete only when `go test ./api/http -run 'Evaluation|Contract|RBAC' -count=1` passes and the existing create-suite, enqueue-run, optimization, experiment, and feedback contract fixtures remain unchanged except for the four-kind enum expansion.

### Task 6: Evaluation Center Page, Drawers, and Responsive UX

**Files:**

- Create: `web/src/modules/evaluation/pages/EvaluationCenterPage.tsx`
- Create: `web/src/modules/evaluation/components/EvaluationOverview.tsx`
- Create: `web/src/modules/evaluation/components/ResourceTable.tsx`
- Create: `web/src/modules/evaluation/components/RunDrawer.tsx`
- Create: `web/src/modules/evaluation/components/CandidateDrawer.tsx`
- Create: `web/src/modules/evaluation/components/ExperimentDrawer.tsx`
- Create: `web/src/modules/evaluation/components/TimelineDrawer.tsx`
- Create: `web/src/modules/evaluation/pages/EvaluationCenterPage.test.tsx`
- Create: `web/src/modules/evaluation/components/ResourceTable.test.tsx`
- Create: `web/src/modules/evaluation/components/ExperimentDrawer.test.tsx`

- [ ] **Step 1: Write failing page and interaction tests**

Test first viewport contains only resource type, status, and `新建评测`; resource table has no more than five columns; empty/search states use the required Chinese text; members can open facts but cannot invoke decisions; admins must confirm reject/pause/promote/rollback; disabled promotion states the missing sample/duration/guardrail.

- [ ] **Step 2: Verify failure**

Run: `npm --prefix web run test -- --run src/modules/evaluation/pages/EvaluationCenterPage.test.tsx src/modules/evaluation/components/ResourceTable.test.tsx src/modules/evaluation/components/ExperimentDrawer.test.tsx`

Expected: FAIL because the page and focused components do not exist.

- [ ] **Step 3: Implement dense responsive center**

Use Ant Design Table, Drawer, Tabs/Collapse, Tag, Alert, Empty, and Modal.confirm. Separate observed facts, configured guardrails, system recommendation, and administrator decision into labeled sections. Use at most five columns, no nested cards, fixed action areas, and mobile full-screen drawers with vertically stacked diffs/metrics. Keep every component below 200 lines and no viewport-scaled font sizing.

- [ ] **Step 4: Verify build and accessibility states**

Run: `npm --prefix web run test -- --run src/modules/evaluation` and `npm --prefix web run build`

Expected: PASS; build emits no TypeScript/lint errors introduced by the center.

- [ ] **Step 5: Commit the frontend experience**

```bash
git add web/src/modules/evaluation
git commit -m "feat(web): deliver evaluation and evolution center"
```

### Task 7: Real E2E, Quality Guards, and Failure Evidence

**Files:**

- Create: `e2e/evaluation-evolution/fixtures.ts`
- Create: `e2e/evaluation-evolution/center.spec.ts`
- Create: `e2e/evaluation-evolution/failure-modes.spec.ts`
- Create: `scripts/e2e/evaluation-evolution.sh`
- Modify: `Makefile`
- Modify: `docs/agent/project.md`

- [ ] **Step 1: Define isolated services and redaction checks**

Use `stratum-e2e-development` to start isolated Opik, OTEL Collector, MinIO, and PostgreSQL services with `TEST_DATABASE_URL`. Generate a unique fixture tenant and inject the test LLM provider/API key through an environment variable or secret file; never look up or hard-code a production tenant name. The fixture must redact credentials and fail if logs or responses contain bearer credentials, raw payloads, or upstream bodies.

- [ ] **Step 2: Add real API/browser scenarios**

Cover this lifecycle matrix for each of Skill, Agent, MCP, and Knowledge: `published baseline -> baseline run -> bounded candidate -> candidate run -> canary -> feedback attribution -> promote or rollback`.

For every resource assert successful execution, cross-tenant not-found, member read/admin command separation, encrypted payload round trip without plaintext response, and an audit decision. Add shared scenarios for tool invocation, LLM failure, evaluation failure, hard security stop, Opik outage, MinIO outage, provider/dependency outage, duplicate idempotency command, and stable serving continuation. Assert recommendation is visible but never invokes promotion automatically.

- [ ] **Step 3: Run targeted E2E and quality guards**

Run: `bash scripts/quality/risk-regression-guard.sh --explain`; `bash scripts/e2e/evaluation-evolution.sh`; `go test -race ./internal/evaluation/... ./internal/agent/... ./internal/mcp/... ./internal/knowledge/...`; `npm --prefix web run test -- --run src/modules/evaluation`; `make risk-guardrails`.

Expected: all targeted checks pass. If the documented existing `vitest/globals`, TypeScript 6 `baseUrl`, or govulncheck environment blockers remain, record exact command/output and do not mask them as feature failures.

- [ ] **Step 4: Add final evidence and commit**

Record service versions, database migration order result, scenario names, skipped prerequisites, and redaction results in `docs/e2e/evaluation-evolution.md`. Remove temporary processes and secrets before commit.

```bash
git add e2e/evaluation-evolution scripts/e2e/evaluation-evolution.sh Makefile docs/agent/project.md docs/e2e/evaluation-evolution.md
git commit -m "test(evaluation): verify four-resource evolution center end to end"
```

## Self-Review Checklist

- [ ] Four resources have immutable revisions, bounded candidates, provider adapters, and focused failure tests.
- [ ] Shared center has all query/command routes, tenant member/admin permissions, safe projections, and frozen errors.
- [ ] Human gates remain explicit; recommendation and safety stop never auto-promote.
- [ ] Frontend has Chinese responsive dense tables/drawers, <=5 columns, <=200-line components, and Skill compact entry.
- [ ] Tenant DDL uses history-compatible ordering and never drops legacy tables.
- [ ] Real Opik/Collector/MinIO/PostgreSQL/API/browser scenarios cover success, failure, security, attribution, rollback, and isolation.
- [ ] Run `rg -n 'revision_repository|Drop(Table|Collection)' internal api web e2e scripts`; expected: no newly introduced provider-owned revision stores or destructive cleanup. Run `rg -n 'TODO|TBD' internal api web e2e scripts`; expected: no implementation placeholders in changed source.
- [ ] Run `rg -n 'api_key|access_token|credential|secret|raw payload' docs/superpowers/plans/2026-07-22-evaluation-evolution-providers-ui.md`; expected: matches only security requirements in this plan, never literal values or source snippets.
- [ ] Run `rg -n 'go test ./api/http|npm --prefix web run test|make risk-guardrails|evaluation-evolution.sh' docs/superpowers/plans/2026-07-22-evaluation-evolution-providers-ui.md`; expected: every implementation task has an executable verification command.
