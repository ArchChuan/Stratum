# Platform Assistant Completion Audit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close every remaining evidence and observability gap in the approved platform-assistant Phase 1, Phase 2, and remote-remediation plans without weakening tenant, credential, or model boundaries.

**Architecture:** Keep the production assistant and proposal architecture unchanged. Add one persisted proposal edit counter and bounded metric, replace fake cross-domain success tests with production repositories/application services, and add a full local browser chain driven by deterministic process-local LLM/MCP stubs through the real Stratum HTTP server. Remote verification remains fail-closed when legal administrator/provider prerequisites are absent.

**Tech Stack:** Go 1.25.12, PostgreSQL/pgx, Gin, Prometheus, React 18, TypeScript/Zod, Playwright, Docker Compose, deterministic OpenAI-compatible and MCP test servers, GitHub Actions.

---

## File Structure

### Production Counter And Metric

- `internal/agent/domain/resource_change_proposal.go`: add the server-owned edit count.
- `internal/agent/infrastructure/persistence/resource_change_proposal_repo.go`: scan and atomically increment edit count.
- `internal/agent/infrastructure/persistence/resource_change_proposal_repo_test.go`: SQL and rollback contract.
- `internal/agent/application/resource_change_proposal_service.go`: emit the final rework observation.
- `internal/agent/application/resource_change_proposal_service_test.go`: service-level count and metric behavior.
- `pkg/storage/postgres/tenant_schema.sql`: idempotent historical-tenant upgrade.
- `pkg/storage/postgres/tenant_schema_test.go`: DDL order and compatibility assertions.
- `pkg/observability/provider.go`: metric interface.
- `pkg/observability/prometheus.go`: bounded histogram.
- `pkg/observability/observability_test.go`: label and observation contract.
- `api/http/dto/resource_change_proposal.go`: read-only edit count response.
- `web/src/modules/agent/model/proposal.ts`: parse the count.
- `web/src/modules/agent/pages/ResourceChangeProposalPage.tsx`: show audit information.

### Real Go E2E

- `test/e2e/system_assistant_proposal_real_test.go`: production repositories, four application services, adapters, proposal service, PostgreSQL state, and failure matrix.
- `test/e2e/platform_assistant_fixture_test.go`: isolated public/tenant rows, real service graph, deterministic MCP server, cleanup, and safe assertions.
- `test/e2e/system_assistant_proposal_test.go`: retain state/concurrency coverage, remove duplicated fake success coverage.
- `scripts/test-platform-assistant-e2e.sh`: ephemeral PostgreSQL execution that fails rather than skips.
- `.github/workflows/ci.yml`: blocking platform-assistant E2E job.

### Real Browser Chain

- `test/e2e/cmd/platform-assistant-stubs/main.go`: deterministic OpenAI-compatible model and streamable HTTP MCP test processes.
- `web/e2e/support/real-platform-assistant.ts`: create temporary guest/admin session, configure test provider, perform safe SQL queries, and exact cleanup.
- `web/e2e/system-assistant-real.spec.ts`: chat proposal creation, review/edit/refresh/confirm, DB read-back, role gate, terminal states, desktop/mobile overflow.
- `web/playwright.real.config.ts`: full-server browser matrix without business API interception.
- `scripts/test-platform-assistant-browser-e2e.sh`: start dependencies, stubs, backend and frontend; wait by readiness; always clean up.
- `.github/workflows/ci.yml`: blocking real browser job.

### Remote And Completion Evidence

- `scripts/e2e/platform-assistant-remote-verify.sh`: no-secret public/SSH checks and structured prerequisite classification.
- `scripts/quality/check-deployment-safety-test.sh`: ensure the verifier cannot silently pass configured-chain omissions.
- `docs/audits/platform-assistant-completion-2026-07-27.md`: requirement-to-evidence matrix.
- `docs/agent/agent.md`, `docs/agent/agent-chat-flow.md`, `docs/SPEC.md`, `docs/deployment/k3s-demo.md`: verified current state and remote prerequisites.

## Task 1: Persist Draft Edit Count And Emit Rework Metrics

**Files:** production counter and metric files listed above.

- [ ] **Step 1: Write failing domain, schema, repository, service, and metric tests**

Add assertions equivalent to:

```go
func TestProposalDraftEditCountIsServerOwned(t *testing.T) {
    proposal := ResourceChangeProposal{EditCount: 2}
    require.Equal(t, 2, proposal.EditCount)
    require.NotContains(t, string(mustJSON(AgentChange{})), "editCount")
}

func TestResourceProposalMetricsUseOnlyBoundedLabels(t *testing.T) {
    metrics := NewPrometheusMetrics(zap.NewNop())
    metrics.RecordResourceProposalDraftEdits("agent", "update", 2)
    // Gather and assert only kind/operation labels exist and histogram count is one.
}
```

Repository expectations must require `edit_count=edit_count+1` in the same `UPDATE` that persists edited payload,
baseline, status and timestamp. Schema tests must assert the create-table column appears before:

```sql
ALTER TABLE resource_change_proposals
    ADD COLUMN IF NOT EXISTS edit_count INT NOT NULL DEFAULT 0;
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./internal/agent/domain ./internal/agent/application ./internal/agent/infrastructure/persistence \
  ./pkg/storage/postgres ./pkg/observability -run 'Proposal.*Edit|ResourceProposalMetrics|TenantSchema' -count=1
```

Expected: FAIL because `EditCount` and `RecordResourceProposalDraftEdits` do not exist and SQL does not increment the value.

- [ ] **Step 3: Implement the minimal production contract**

Add `EditCount int` only to the proposal envelope/response, not any model-generated payload. Extend `proposalColumns`,
`scanProposal`, insert arguments, and `UpdateDraft`. After a successful update, increment the returned proposal value.
Record the histogram once after claim, using the claimed persisted count, for every deterministic terminal outcome.

Extend `MetricsProvider` and `NoopMetrics`:

```go
RecordResourceProposalDraftEdits(kind, operation string, count int)
```

Create a histogram named `system_assistant_resource_proposal_draft_edits` with labels `kind,operation` and bounded buckets
`0,1,2,3,5,8`. Render `已调整 N 次` in proposal audit information when `N > 0`.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run the command from Step 2, then:

```bash
stratum-verify frontend-test
```

Expected: all tests PASS; no secret or identifier appears as a metric label.

- [ ] **Step 5: Commit**

```bash
git add internal/agent pkg/storage/postgres pkg/observability api/http/dto web/src/modules/agent
git commit -m '[feat](agent): measure proposal review rework'
```

## Task 2: Replace Fake Four-resource Success Coverage With Real Services

**Files:** real Go E2E files and script listed above.

- [ ] **Step 1: Write a failing fixture test that demands production service types**

The fixture must provision public and two tenant schemas, insert only `E2E-` users/tenants/memberships, and construct:

```go
agentRepo := agentpersist.NewPgAgentRepo(pool)
agentSvc := agentapp.NewAgentService(agentapp.AgentServiceDeps{
    Registry: agentapp.NewRegistry(agentRepo, agentapp.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
    Logger: zap.NewNop(),
})
skillSvc := skillapp.NewVersionService(skillpersist.NewPgSkillRevisionRepo(pool), zap.NewNop())
workspaceSvc := knowledgeapp.NewWorkspaceService(knowledgepersist.NewWorkspaceRepo(pool), nil, zap.NewNop())
manager := mcpinfra.NewClientManager(zap.NewNop(), nil, pool)
registry := mcpinfra.NewMCPToolRegistry(manager, zap.NewNop())
mcpSvc := mcpapp.NewMCPService(mcpinfra.ToolRegistryAsPort(registry), mcpinfra.ServerManagerAsPort(manager), zap.NewNop())
adapters := wiring.NewResourceChangeProposalAdapters(agentSvc, skillSvc, mcpSvc, workspaceSvc)
```

Use `internal/mcp/infrastructure/testserver.New(t)` for credential-free `streamable-http` create/update. The test must fail
until all production constructors and context requirements are correctly satisfied; do not replace a failing service with the old fake Applier.

- [ ] **Step 2: Run the real-service test and verify RED**

Run:

```bash
STRATUM_TEST_POSTGRES_URL=postgres://stratum:stratum@localhost:5432/stratum?sslmode=disable \
  go test -v ./test/e2e -run TestSystemAssistantProposalRealServices -count=1
```

Expected: FAIL initially on missing fixture/wiring or a real service contract, proving the new test is not the old fake path.

- [ ] **Step 3: Implement isolated real-service create/update journeys**

For each kind, create via ProposalService, confirm exactly once, read back through the owning service, create an update
proposal from the real baseline, confirm it, and read back the changed field:

```text
Agent: name/description/model/limits and managed-target rejection
Skill draft: description/instructions/content hash, never published
MCP: streamable-http URL/version/timeout, AuthTypeNone on create, stored credential markers preserved but absent from proposal/result
Knowledge: immutable name, description/embedding config, no document rows
```

Assert all SQL-facing contexts use `tenantdb.WithTenant`, tenant B cannot read tenant A resources/proposals, and every
result fingerprint matches a fresh safe projection.

- [ ] **Step 4: Add the real failure and recovery matrix**

Use production repo/service state and narrow port fault injection only at the failing boundary to cover:

- expired confirmation persists `expired` plus event;
- stale baseline does not call the owning service;
- known pre-side-effect error ends `failed`;
- possible external side effect ends `unknown_outcome`;
- two concurrent confirmations produce one durable resource;
- rebuilding ProposalService after `confirmed` resumes one claim;
- stale `applying` after restart becomes `unknown_outcome` without another call;
- invalid secret markers never occur in proposal, event, result, trace fixture, or safe error text.

- [ ] **Step 5: Add a fail-closed script and CI job**

`scripts/test-platform-assistant-e2e.sh` must create an ephemeral PostgreSQL container with `mktemp`/random port, export
`STRATUM_TEST_POSTGRES_URL` and `REQUIRE_PLATFORM_ASSISTANT_E2E=1`, run both platform-assistant Go E2E suites, and remove
only its exact container in a trap. In CI, missing PostgreSQL must `t.Fatal`, never `t.Skip`.

- [ ] **Step 6: Run real E2E and regression tests**

Run:

```bash
bash scripts/test-platform-assistant-e2e.sh
go test ./api/wiring ./internal/agent/... ./internal/skill/... ./internal/mcp/... ./internal/knowledge/... -count=1
```

Expected: PASS with production success paths and no leaked process/schema.

- [ ] **Step 7: Commit**

```bash
git add test/e2e scripts/test-platform-assistant-e2e.sh .github/workflows/ci.yml
git commit -m '[test](agent): exercise real proposal services'
```

## Task 3: Drive Chat-to-Apply Through The Real Browser And Database

**Files:** stub, support, Playwright, script, and CI files listed above.

- [ ] **Step 1: Write the failing real Playwright scenario**

Create `system-assistant-real.spec.ts` with `test.skip(process.env.REAL_PLATFORM_ASSISTANT_E2E !== '1', ...)`, but make
the dedicated script/CI always set the variable. The main test must not call `page.route()` for any business endpoint.
It must:

1. create a guest through real `/auth/guest`;
2. promote only that UUID membership to admin in the test database;
3. rotate through real `/auth/refresh`;
4. configure the process-local provider and system-assistant model through real APIs;
5. create a conversation and send a prompt that causes `stratum_propose_resource_change`;
6. open the proposal card and review page;
7. edit, refresh, verify persistence, confirm once, and verify `applied` plus owning-resource DB state.

Run first with no stubs/backend and verify it fails on readiness rather than passing through intercepts.

- [ ] **Step 2: Implement deterministic process-local stubs**

`test/e2e/cmd/platform-assistant-stubs/main.go` must bind only to `127.0.0.1` and expose:

- OpenAI-compatible model listing and chat completion endpoints; first response emits the exact proposal tool call, second
  response returns a concise final answer. It must never log request bodies or Authorization.
- streamable HTTP MCP initialize/list-tools endpoints for backend E2E reuse.
- `/readyz` and an in-memory call-count endpoint containing only bounded counters.

The command receives ports and expected tool name from flags; no credentials are read or emitted.

- [ ] **Step 3: Implement real session and safe database helpers**

Extend the existing `real-workflow.ts` pattern. Validate every tenant/user/proposal/resource ID against a UUID-or-fixed-ID
allowlist before interpolating SQL. Helpers may update only the newly created guest membership and may query only the
current test tenant schema. Never return token/key values from helpers or print HTTP bodies containing them.

- [ ] **Step 4: Add terminal, role, refresh, and viewport cases**

At `mobile-390` and `desktop-1440`, execute the main path using the real mobile drawer/navigation. Add real API/DB seeded
terminal proposals for `stale`, `expired`, `failed`, and `unknown_outcome`; assert reason text and no confirm/retry button.
Create a separate member session and assert route denial matches backend 403. After PATCH, reload the browser and assert
payload plus `editCount=1` before confirm. Check DOM overflow and canvas-free pixel content; screenshots must contain no secret markers.

- [ ] **Step 5: Add the lifecycle script**

`scripts/test-platform-assistant-browser-e2e.sh` must:

```text
validate required local commands
start exact Docker Compose dependencies
start the deterministic stub on a random localhost port
start Stratum with QWEN_BASE_URL pointing at the stub and test-only AES/JWT values held in environment
wait for /readyz using bounded polling
run Playwright with REAL_PLATFORM_ASSISTANT_E2E=1 for mobile-390 and desktop-1440
stop only processes started by the script
remove exact temporary files and test data
propagate any cleanup or test failure
```

Do not use fixed sleeps or expose environment values in `set -x` output.

- [ ] **Step 6: Add a blocking CI job and run it locally**

Add a dedicated job with pinned service images/Chromium setup and explicit timeout. Run:

```bash
bash scripts/test-platform-assistant-browser-e2e.sh
```

Expected: both viewports PASS, real backend access logs show proposal GET/PATCH/confirm, DB assertions pass, and no process remains.

- [ ] **Step 7: Commit**

```bash
git add test/e2e/cmd web/e2e web/playwright.real.config.ts scripts/test-platform-assistant-browser-e2e.sh .github/workflows/ci.yml
git commit -m '[test](agent): verify real assistant browser chain'
```

## Task 4: Add Remote Prerequisite-aware Verification

**Files:** remote verifier, deployment guard, and deployment documentation.

- [ ] **Step 1: Write failing shell contract tests**

Extend the deployment safety test to require that the remote verifier checks, without printing values:

```text
public /api/health
five bounded guest login attempts
member GET managed Agent with empty prompt
member GET /agents/executions returns 200
Opik backend and collector Ready
baseline_projection and edit_count columns exist
aggregate admin/owner count
aggregate configured-provider count
```

Require a three-state configured-chain result: `passed`, `failed`, or `prerequisite_missing`. The script must exit nonzero
for `failed`; `prerequisite_missing` is reported distinctly and cannot be rendered as passed.

- [ ] **Step 2: Run guard and verify RED**

Run:

```bash
bash scripts/quality/check-deployment-safety-test.sh
```

Expected: FAIL because the verifier does not exist.

- [ ] **Step 3: Implement the no-secret verifier**

Use `curl --fail-with-body` only with response bodies redirected to protected temporary files; parse tokens in memory and
never print them. SSH commands are read-only aggregate `kubectl`/SQL checks. If both a legal admin/owner and provider exist,
run the configured assistant chain using that legitimate session mechanism; otherwise print only:

```json
{"configuredChain":"prerequisite_missing","missing":["tenant_admin","tenant_provider"]}
```

Do not modify membership, tenant settings, Provider keys, or remote resources to manufacture the prerequisite.

- [ ] **Step 4: Run local contract and remote no-credential layer**

Run:

```bash
bash scripts/quality/check-deployment-safety-test.sh
bash scripts/e2e/platform-assistant-remote-verify.sh http://101.200.181.141:6879 root@101.200.181.141
```

Expected for the current Demo: no-credential layer PASS; configured chain reports `prerequisite_missing`, not success or failure.

- [ ] **Step 5: Commit**

```bash
git add scripts/e2e scripts/quality/check-deployment-safety-test.sh docs/deployment/k3s-demo.md
git commit -m '[test](deploy): verify assistant prerequisites explicitly'
```

## Task 5: Produce The Requirement Matrix And Run Every Completion Gate

**Files:** audit and current-state documentation.

- [ ] **Step 1: Build a requirement-to-evidence matrix**

For every checklist item in Phase 1 Completion Gate, Phase 2 Completion Gate, and Remote Acceptance Task 5, record:

```text
requirement | authoritative code | test/gate | runtime evidence | result | boundary
```

No row may use “covered by tests” without naming the exact test and what cross-layer behavior it proves. Mark the configured
remote chain `prerequisite_missing` until legitimate configuration exists; do not collapse it into complete.

- [ ] **Step 2: Run focused and security gates**

Run with fresh output:

```bash
make risk-guardrails
make tool-permission-test
bash scripts/test-platform-assistant-e2e.sh
bash scripts/test-platform-assistant-browser-e2e.sh
go test ./api/http ./api/http/handler ./api/wiring ./internal/agent/... -count=1
```

Expected: PASS and no skipped platform-assistant E2E.

- [ ] **Step 3: Run full backend and frontend gates**

Run:

```bash
stratum-verify go-full
go test -v -race -timeout 30s ./internal/agent/... ./api/wiring ./api/http/... ./test/e2e
make fe-lint
make fe-build
stratum-verify frontend-test
git diff origin/main --check
```

Expected: PASS; `govulncheck` has no reachable vulnerability and all temporary processes are stopped.

- [ ] **Step 4: Update documentation and knowledge report**

Document only verified current behavior. Generate the required ignored report under
`tmp/knowledge-deposition/2026-07-27/`; include `none` if the completion work produces no new durable candidate. Do not write
to Obsidian during implementation.

- [ ] **Step 5: Request code review and resolve findings**

Use `superpowers:requesting-code-review` and `review-it`. Fix every confirmed blocking finding with a RED test first, then
rerun affected and full gates.

- [ ] **Step 6: Commit final evidence**

```bash
git add docs/audits docs/agent docs/SPEC.md
git commit -m '[docs](agent): record platform assistant completion evidence'
```

- [ ] **Step 7: Ship through PR and CD**

Use `ship-it`: push `fix/platform-assistant-completion-audit`, open a PR with What/Why/HowToTest, wait for all required CI,
merge to `main`, wait for Build and Deploy, run the remote verifier against the deployed merge commit, and remove the exact
feature worktree only after the knowledge report is preserved in the primary ignored directory.

## Completion Gate

- [ ] Every Phase 1, Phase 2, and remote-remediation checklist row has direct evidence or an explicit unsatisfied legal prerequisite.
- [ ] Four successful resource create/update paths use production application services and PostgreSQL repositories.
- [ ] Browser chat-to-apply path uses real HTTP and DB with no business API interception at desktop and mobile widths.
- [ ] Expiry, stale, known failure, unknown outcome, repeat confirmation, restart recovery, member denial, cross-tenant access, and secret marker cases are blocking tests.
- [ ] Proposal `edit_count` upgrades historical schemas idempotently and emits bounded metrics.
- [ ] No delete, Skill publish, MCP execution, Knowledge upload, credential replacement, shared model fallback, or forged remote authority exists.
- [ ] All unit, integration, race, browser, risk, frontend, CI, CD, and remote no-credential checks pass with fresh evidence.
- [ ] The configured remote chain is not called complete until a legitimate tenant admin and Provider exist and the real run is observed in Opik.
