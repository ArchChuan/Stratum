# Built-in Platform Assistant Phase 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let tenant administrators create or update Agent, Skill draft, credential-free MCP, and Knowledge workspace configuration through typed, reviewable, conflict-safe proposals.

**Architecture:** The managed assistant can create proposal records but cannot call resource write services. A deterministic ProposalService owns validation, authorization, state, idempotency, baseline checks, audit events, and atomic claims; thin wiring adapters invoke the owning context's application service and return a safe read-back result.

**Tech Stack:** Go 1.25.12, PostgreSQL/pgx JSONB, Gin, React 18, TypeScript/Zod, Ant Design 5, Vitest, Playwright, existing Agent ReAct/SSE and tenant RBAC.

---

## File Structure

### Proposal Core

- `internal/agent/domain/resource_change_proposal.go`: states, typed envelopes, payloads, events, errors, and transitions.
- `internal/agent/domain/resource_change_proposal_test.go`: transition table and strict payload validation.
- `internal/agent/domain/port/resource_change_proposal.go`: repository, authorization, baseline, and per-resource Applier ports.
- `internal/agent/application/resource_change_proposal_service.go`: create/review/cancel/confirm/apply orchestration.
- `internal/agent/application/resource_change_proposal_service_test.go`: authorization, stale, expiry, claim, retry, and result tests.
- `internal/agent/infrastructure/persistence/resource_change_proposal_repo.go`: tenant-scoped PostgreSQL state transitions.
- `internal/agent/infrastructure/persistence/resource_change_proposal_repo_test.go`: pgxmock SQL/rollback behavior.
- `internal/agent/infrastructure/persistence/resource_change_proposal_repo_integration_test.go`: concurrency and isolation.
- `pkg/storage/postgres/tenant_schema.sql`: proposal and append-only event tables.

### Resource Adapters

- `api/wiring/resource_change_proposal.go`: thin Agent/Skill/MCP/Knowledge adapters and baseline resolvers.
- `api/wiring/resource_change_proposal_test.go`: input mapping, no cross-context SQL, safe read-back.
- `internal/mcp/application/mcp_service.go`: preserve existing protected values on permitted update.
- `internal/mcp/application/mcp_service_secret_test.go`: proposal adapter cannot add/replace credentials.

### Agent Tool And API

- `internal/agent/application/system_assistant_tools.go`: admin-only proposal tool.
- `internal/agent/application/system_assistant_tools_test.go`: member invisibility and strict Schema.
- `internal/agent/application/graph/react.go`: fixed proposal-tool dispatch.
- `api/http/handler/resource_change_proposal_handler.go`: read, edit, cancel, confirm endpoints.
- `api/http/handler/resource_change_proposal_handler_test.go`: transport/RBAC/error tests.
- `api/http/dto/resource_change_proposal.go`: safe typed request/response DTOs.
- `api/http/router.go`: static proposal routes under JWT, tenant, active, and admin guards.
- `api/http/contract_test.go` and `api/http/testdata/contracts/*.golden.json`: frozen endpoint contracts.
- `pkg/observability/provider.go`: extend the metrics interface with bounded proposal observations.
- `pkg/observability/prometheus.go`: implement review duration, confirmation, conflict, failure, and rework metrics.
- `pkg/observability/observability_test.go`: verify proposal metric labels stay bounded.

### Frontend And E2E

- `web/src/modules/agent/model/proposal.ts`: discriminated-union proposal schemas.
- `web/src/modules/agent/api/proposal.api.ts`: read/edit/cancel/confirm operations.
- `web/src/modules/agent/components/ResourceChangeProposalCard.tsx`: chat summary and status.
- `web/src/modules/agent/pages/ResourceChangeProposalPage.tsx`: dedicated review page.
- `web/src/modules/agent/hooks/useResourceChangeProposal.ts`: load/edit/confirm and terminal-state behavior.
- `web/src/modules/agent/routes.tsx`: protected proposal review route.
- `web/src/modules/agent/**/__tests__/*.test.tsx`: field-diff, status, role, and mobile tests.
- `test/e2e/system_assistant_proposal_test.go`: real DB/API/application concurrency harness.
- `web/e2e/system-assistant-proposal.spec.ts`: real browser review and confirmation.
- `docs/agent/agent.md`, `docs/agent/agent-chat-flow.md`, `docs/SPEC.md`: current-state documentation.

## Task 1: Define Strict Proposal Types And State Transitions

**Files:**

- Create: `internal/agent/domain/resource_change_proposal.go`
- Create: `internal/agent/domain/resource_change_proposal_test.go`
- Create: `internal/agent/domain/port/resource_change_proposal.go`

- [ ] **Step 1: Write the failing transition table**

```go
func TestProposalTransitionTable(t *testing.T) {
 cases := []struct{ from, to ProposalStatus; allowed bool }{
  {StatusDraft, StatusReadyForReview, true},
  {StatusReadyForReview, StatusConfirmed, true},
  {StatusConfirmed, StatusApplying, true},
  {StatusApplying, StatusApplied, true},
  {StatusReadyForReview, StatusStale, true},
  {StatusApplying, StatusUnknownOutcome, true},
  {StatusApplied, StatusApplying, false},
  {StatusStale, StatusConfirmed, false},
 }
 for _, tc := range cases {
  require.Equal(t, tc.allowed, CanTransition(tc.from, tc.to))
 }
}
```

Add tests for expiry, one operation per proposal, create without resource ID, update requiring resource ID/baseline, unknown JSON fields, and every prohibited operation.

- [ ] **Step 2: Run domain tests and verify failure**

Run: `go test ./internal/agent/domain -run Proposal -count=1`

Expected: FAIL because proposal types do not exist.

- [ ] **Step 3: Define states, resource kinds, operations, and errors**

```go
type ProposalStatus string
const (
 StatusDraft ProposalStatus = "draft"
 StatusReadyForReview ProposalStatus = "ready_for_review"
 StatusConfirmed ProposalStatus = "confirmed"
 StatusApplying ProposalStatus = "applying"
 StatusApplied ProposalStatus = "applied"
 StatusInvalid ProposalStatus = "invalid"
 StatusStale ProposalStatus = "stale"
 StatusExpired ProposalStatus = "expired"
 StatusFailed ProposalStatus = "failed"
 StatusUnknownOutcome ProposalStatus = "unknown_outcome"
 StatusCancelled ProposalStatus = "cancelled"
)
```

Define only `agent`, `skill_draft`, `mcp_config`, `knowledge_workspace` and `create`, `update`. Add stable sentinel errors matching the design error codes.

- [ ] **Step 4: Define discriminated payloads without secret fields**

Use separate structs and strict decoding:

```go
type MCPConfigChange struct {
 Name         string          `json:"name"`
 Version      string          `json:"version"`
 Transport    string          `json:"transport"`
 Command      string          `json:"command,omitempty"`
 Args         []string        `json:"args,omitempty"`
 URL          string          `json:"url,omitempty"`
 Capabilities []string        `json:"capabilities,omitempty"`
 TimeoutSec   int             `json:"timeoutSec"`
 Retry        *MCPRetryChange `json:"retry,omitempty"`
}

type MCPRetryChange struct {
 Enabled        bool    `json:"enabled"`
 MaxRetries     int     `json:"maxRetries"`
 InitialDelayMs int64   `json:"initialDelayMs"`
 MaxDelayMs     int64   `json:"maxDelayMs"`
 BackoffFactor  float64 `json:"backoffFactor"`
}
```

Do not include `env`, `headers`, bearer token, API-key value, OAuth client secret, delete, publish, upload, or tool execution fields. `DecodePayload` uses `json.Decoder.DisallowUnknownFields()` and verifies EOF.

- [ ] **Step 5: Define ports**

```go
type ProposalRepo interface {
 Create(ctx context.Context, proposal domain.ResourceChangeProposal, event domain.ProposalEvent) error
 Get(ctx context.Context, id string) (domain.ResourceChangeProposal, error)
 UpdateDraft(ctx context.Context, proposal domain.ResourceChangeProposal, event domain.ProposalEvent) error
 Cancel(ctx context.Context, id, actor string, at time.Time) error
 Confirm(ctx context.Context, id, actor string, at time.Time) error
 ClaimApplying(ctx context.Context, id, actor string, at time.Time) (domain.ResourceChangeProposal, error)
 Finish(ctx context.Context, id string, status domain.ProposalStatus, result domain.ApplyResult, event domain.ProposalEvent) error
 ListEvents(ctx context.Context, id string) ([]domain.ProposalEvent, error)
}
```

Add `ProposalAuthorizer`, `BaselineResolver`, and one `ResourceChangeApplier` interface with typed envelope input and safe result output.

- [ ] **Step 6: Run domain tests**

Run: `go test ./internal/agent/domain -run Proposal -count=1`

Expected: PASS.

- [ ] **Step 7: Commit proposal contracts**

```bash
git add internal/agent/domain
git commit -m "[feat](agent): define resource proposal contracts"
```

## Task 2: Persist Tenant-scoped Proposals And Audit Events

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql`
- Create: `internal/agent/infrastructure/persistence/resource_change_proposal_repo.go`
- Create: `internal/agent/infrastructure/persistence/resource_change_proposal_repo_test.go`
- Create: `internal/agent/infrastructure/persistence/resource_change_proposal_repo_integration_test.go`

- [ ] **Step 1: Write failing repository and integration tests**

Cover create+event atomicity, JSON marshal failure, rollback, cross-tenant miss, expired confirmation, concurrent confirm, concurrent claim, terminal immutability, and append-only event order.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/agent/infrastructure/persistence -run ResourceChangeProposal -count=1`

Expected: FAIL because tables and repository do not exist.

- [ ] **Step 3: Add tenant-only DDL**

```sql
CREATE TABLE IF NOT EXISTS resource_change_proposals (
    id TEXT PRIMARY KEY,
    conversation_id UUID,
    proposer_id TEXT NOT NULL,
    confirmer_id TEXT NOT NULL DEFAULT '',
    resource_kind TEXT NOT NULL CHECK (resource_kind IN ('agent','skill_draft','mcp_config','knowledge_workspace')),
    resource_id TEXT NOT NULL DEFAULT '',
    operation TEXT NOT NULL CHECK (operation IN ('create','update')),
    baseline_fingerprint TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL,
    safe_summary JSONB NOT NULL DEFAULT '{}',
    status TEXT NOT NULL,
    result JSONB NOT NULL DEFAULT '{}',
    error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    confirmed_at TIMESTAMPTZ,
    applied_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS resource_change_proposal_events (
    id UUID PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    proposal_id TEXT NOT NULL REFERENCES resource_change_proposals(id) ON DELETE CASCADE,
    actor_id TEXT NOT NULL DEFAULT '',
    from_status TEXT NOT NULL DEFAULT '',
    to_status TEXT NOT NULL,
    detail JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Add indexes for `(status, expires_at, created_at)` and `(proposal_id, created_at, id)`. Follow every new column with compatibility `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` only when historical order requires it.

- [ ] **Step 4: Implement every method through `execTenant`**

Marshal payload/result/event detail before entering SQL and pass `string(b)` to JSONB parameters. `Confirm` uses `WHERE status='ready_for_review' AND expires_at>NOW()`. `ClaimApplying` uses one `UPDATE ... WHERE status='confirmed' RETURNING ...`; zero rows maps to expired/already-claimed/terminal via a follow-up read in the same transaction.

- [ ] **Step 5: Run unit and real PostgreSQL tests**

Run: `go test ./internal/agent/infrastructure/persistence -run ResourceChangeProposal -count=1`

Run: `STRATUM_TEST_POSTGRES_URL=... go test ./internal/agent/infrastructure/persistence -run ResourceChangeProposalIntegration -count=1`

Expected: PASS; exactly one concurrent claimant wins and tenant B cannot read tenant A's proposal.

- [ ] **Step 6: Commit persistence**

```bash
git add pkg/storage/postgres/tenant_schema.sql internal/agent/infrastructure/persistence
git commit -m "[feat](agent): persist tenant resource proposals"
```

## Task 3: Orchestrate Review, Confirmation, Conflict, And Apply

**Files:**

- Create: `internal/agent/application/resource_change_proposal_service.go`
- Create: `internal/agent/application/resource_change_proposal_service_test.go`
- Modify: `internal/agent/domain/port/resource_change_proposal.go`
- Modify: `pkg/observability/provider.go`
- Modify: `pkg/observability/prometheus.go`
- Modify: `pkg/observability/observability_test.go`

- [ ] **Step 1: Write failing service tests**

Test member create/confirm denial, admin create, invalid safe record without raw rejected payload, draft edit, expiry, baseline stale, authorization changed after confirmation, exact-one claim, known failure, unknown outcome, successful read-back, and persistence failure propagation.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/agent/application -run ResourceChangeProposal -count=1`

Expected: FAIL because `ResourceChangeProposalService` does not exist.

- [ ] **Step 3: Implement create and review**

```go
type CreateProposalInput struct {
 TenantID, ConversationID, ActorID string
 Kind domain.ResourceKind
 Operation domain.ResourceOperation
 ResourceID string
 Payload json.RawMessage
}
```

Authorize admin/owner before baseline or resource reads. Strictly decode payload, run kind-specific validation, resolve an opaque baseline fingerprint for updates, set a constant-bounded expiry, and store `ready_for_review`. If secret-like keys or unknown fields are detected, persist only an `invalid` safe summary/error event; never persist rejected raw payload.

- [ ] **Step 4: Implement confirm and apply**

`ConfirmAndApply` must:

1. authorize current actor;
2. confirm the ready proposal;
3. claim `confirmed -> applying` atomically;
4. re-authorize;
5. recompute and compare the baseline;
6. call exactly one Applier selected by hard-coded resource kind;
7. persist `applied`, `failed`, `stale`, or `unknown_outcome` plus safe read-back.

Never retry in this method. A known pre-side-effect transient failure may leave a separately explicit retryable failure classification, but no retry endpoint is added in phase 2.

- [ ] **Step 5: Add bounded proposal metrics**

Emit bounded-label counters/histograms for proposal kind, operation, terminal outcome, review duration, stale conflict, and number of draft edits before confirmation. Never use tenant, actor, resource ID, proposal ID, payload value, or error text as metric labels.

- [ ] **Step 6: Run service tests**

Run: `go test ./internal/agent/application -run ResourceChangeProposal -count=1`

Expected: PASS with no Applier call after any failed authorization or stale baseline.

- [ ] **Step 7: Commit orchestration**

```bash
git add internal/agent/application internal/agent/domain/port pkg/observability
git commit -m "[feat](agent): orchestrate resource proposals"
```

## Task 4: Add Agent And Skill Draft Appliers

**Files:**

- Create: `api/wiring/resource_change_proposal.go`
- Create: `api/wiring/resource_change_proposal_test.go`
- Modify: `api/wiring/agent.go`
- Modify: `internal/skill/application/version_service.go`
- Modify: `internal/skill/application/version_service_test.go`

- [ ] **Step 1: Write failing adapter tests**

Agent tests cover create/update mapping, system Agent rejection, baseline hash changes, and safe read-back. Skill tests cover create draft, update existing draft only, content-hash baseline, and rejection when only a published revision exists and no editable draft is present.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./api/wiring ./internal/skill/application -run 'ProposalAgent|ProposalSkill' -count=1`

Expected: FAIL because adapters and an atomic Skill draft update use case do not exist.

- [ ] **Step 3: Implement the Agent adapter**

Map `AgentChange` to existing `AgentService.Create/Update`. Compute baseline SHA-256 over a canonical JSON projection of mutable fields. Reject any target with `SystemKey != ""`. Read back through `AgentService.Get` and return only ID, name, description, model, limits, relation IDs, and updated fingerprint.

- [ ] **Step 4: Add an atomic Skill draft update use case**

Add to `VersionService`:

```go
func (s *VersionService) UpdateDraftBundle(
 ctx context.Context, skillID, expectedContentHash string, in UpdateDraftBundleInput,
) (SkillWorkspaceView, error)
```

The repository performs `UPDATE ... WHERE skill_id=$1 AND status='draft' AND content_hash=$2`; zero rows maps to stale/not-found after a same-transaction read. It updates capability, activation contract, instructions, and requirements together, recomputes one content hash, and never publishes.

- [ ] **Step 5: Implement the Skill adapter**

Create delegates to `CreateSkillDraft`. Update requires the proposal baseline hash and delegates to `UpdateDraftBundle`. Safe read-back excludes internal generation metadata not needed by the review UI.

- [ ] **Step 6: Run adapter and Skill tests**

Run: `go test ./api/wiring ./internal/skill/application ./internal/skill/infrastructure/persistence -run 'ProposalAgent|ProposalSkill|UpdateDraftBundle' -count=1`

Expected: PASS with stale writes rejected.

- [ ] **Step 7: Commit Agent and Skill adapters**

```bash
git add api/wiring internal/skill
git commit -m "[feat](agent): apply agent and skill proposals"
```

## Task 5: Add Credential-safe MCP And Knowledge Appliers

**Files:**

- Modify: `api/wiring/resource_change_proposal.go`
- Modify: `api/wiring/resource_change_proposal_test.go`
- Modify: `internal/mcp/application/mcp_service.go`
- Modify: `internal/mcp/application/mcp_service_secret_test.go`
- Modify: `internal/knowledge/application/workspace_service.go`
- Modify: `internal/knowledge/application/workspace_service_test.go`

- [ ] **Step 1: Write failing MCP safety tests**

Test credential-free create, rejection of `auth/env/headers` unknown fields, update preserving existing sensitive values, update unable to replace auth type, no secret in baseline/result/error, and connection timeout classified as unknown outcome only when the manager reports the request may have taken effect.

- [ ] **Step 2: Write failing Knowledge tests**

Test create/update mapping, immutable workspace name on update, config fingerprint stale detection, no document upload path, and safe read-back of workspace/config only.

- [ ] **Step 3: Run tests and verify failure**

Run: `go test ./api/wiring ./internal/mcp/application ./internal/knowledge/application -run 'ProposalMCP|ProposalKnowledge' -count=1`

Expected: FAIL because adapters and focused safe methods do not exist.

- [ ] **Step 4: Implement MCP safe application**

Create constructs `ServerConfig{Auth:&AuthConfig{Type:AuthTypeNone}}` with empty Env/Headers. Update loads the stored config, overlays only fields present in `MCPConfigChange`, keeps stored transport/auth class where required, and calls existing `MCPService.UpdateServer`, whose `mergeProtectedConfig` preserves sensitive values. The safe result uses `dto.NewMCPServerConfigResponse` semantics without importing HTTP DTOs into application code.

- [ ] **Step 5: Implement Knowledge application**

Create delegates to `WorkspaceService.CreateWorkspace`. Update addresses the immutable existing name and delegates to `UpdateWorkspace` with `Name:""`; never call ingest or document services. Fingerprint canonical name/description/config JSON.

- [ ] **Step 6: Run MCP/Knowledge tests**

Run: `go test ./api/wiring ./internal/mcp/application ./internal/knowledge/application -run 'ProposalMCP|ProposalKnowledge' -count=1`

Expected: PASS; fixture scans contain no literal credential values.

- [ ] **Step 7: Commit MCP and Knowledge adapters**

```bash
git add api/wiring internal/mcp/application internal/knowledge/application
git commit -m "[feat](agent): apply safe platform resource proposals"
```

## Task 6: Let Only Admin System Assistants Create Proposal Drafts

**Files:**

- Modify: `internal/agent/application/system_assistant_tools.go`
- Modify: `internal/agent/application/system_assistant_tools_test.go`
- Modify: `internal/agent/application/agent.go`
- Modify: `internal/agent/application/graph/react.go`
- Modify: `internal/agent/application/graph/react_test.go`
- Modify: `internal/agent/application/agent_service.go`

- [ ] **Step 1: Write failing exposure and injection tests**

Assert the tool is absent for ordinary Agents and members, present for admin/owner system-assistant runs, accepts no actor/tenant/status fields, and stores the authenticated conversation/actor from execution context.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./internal/agent/application/... -run ProposalTool -count=1`

Expected: FAIL because the proposal tool does not exist.

- [ ] **Step 3: Add the fixed tool contract**

```go
const ToolProposeResourceChange = "stratum_propose_resource_change"
```

The input Schema is a `oneOf` discriminated by resource kind and operation. It contains only the phase-2 payload fields. Add `ProposalCreateFn` to `ExecutionConfig`; its closure captures tenant, user, conversation and role from authenticated execution context.

- [ ] **Step 4: Add hard-coded dispatch and artifacts**

`graph/react.go` dispatches only the exact constant, calls `ProposalService.Create`, and returns proposal ID, safe summary, expiry, and `ready_for_review`/`invalid`. Append a `resourceChangeProposal` execution artifact so sync/SSE/history use the phase-1 artifact channel.

- [ ] **Step 5: Run Agent tool tests**

Run: `go test ./internal/agent/application/... -run ProposalTool -count=1`

Expected: PASS; member/ordinary-agent tool catalogs contain no proposal tool.

- [ ] **Step 6: Commit proposal tool**

```bash
git add internal/agent/application
git commit -m "[feat](agent): propose governed resource changes"
```

## Task 7: Expose Review, Cancel, And Confirm APIs

**Files:**

- Create: `api/http/dto/resource_change_proposal.go`
- Create: `api/http/handler/resource_change_proposal_handler.go`
- Create: `api/http/handler/resource_change_proposal_handler_test.go`
- Modify: `api/http/router.go`
- Modify: `api/http/contract_test.go`
- Create/Modify: `api/http/testdata/contracts/*.golden.json`

- [ ] **Step 1: Write failing handler tests**

Cover GET/PATCH/cancel/confirm, member denial, missing tenant/user, stale, expired, concurrent confirm, invalid payload, unknown outcome, and no secret-shaped fields in any response.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./api/http/handler -run ResourceChangeProposal -count=1`

Expected: FAIL because handler/routes do not exist.

- [ ] **Step 3: Define safe DTOs and thin handlers**

Register before parameterized Agent routes:

```text
GET   /resource-change-proposals/:id
PATCH /resource-change-proposals/:id
POST  /resource-change-proposals/:id/cancel
POST  /resource-change-proposals/:id/confirm
```

All routes require JWT, current tenant, active tenant, and admin/owner. Handlers bind, derive tenant/user from context, call service, and render. PATCH can change payload while `ready_for_review`; service recomputes validation, safe summary, baseline, and expiry.

- [ ] **Step 4: Map stable errors**

Map forbidden to 403, missing to 404, invalid to 400, stale/expired/already-claimed to 409, known apply failure to 422 or 502 based on cause, and unknown outcome to 409 with manual-reconciliation wording. Preserve `{"error":"..."}`.

- [ ] **Step 5: Update and verify contract goldens**

Run: `go test ./api/http -run Contract -count=1 -update`

Review all golden changes, then run:

Run: `go test ./api/http ./api/http/handler -run 'Contract|ResourceChangeProposal' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit proposal API**

```bash
git add api/http
git commit -m "[feat](api): add resource proposal review endpoints"
```

## Task 8: Build The Dedicated Review Page

**Files:**

- Create: `web/src/modules/agent/model/proposal.ts`
- Create: `web/src/modules/agent/api/proposal.api.ts`
- Create: `web/src/modules/agent/hooks/useResourceChangeProposal.ts`
- Create: `web/src/modules/agent/components/ResourceChangeProposalCard.tsx`
- Create: `web/src/modules/agent/pages/ResourceChangeProposalPage.tsx`
- Modify: `web/src/modules/agent/components/ChatMessageList.tsx`
- Modify: `web/src/modules/agent/routes.tsx`
- Create: `web/src/modules/agent/**/__tests__/*Proposal*.test.tsx`

- [ ] **Step 1: Write failing frontend tests**

Test discriminated payload parsing, card navigation, field-level diff, impact text, editable ready state, admin-only confirm, cancel confirmation, stale/expired/failed/unknown terminal states, secret-field absence, duplicate-click loading lock, and mobile wrapping.

- [ ] **Step 2: Run tests and verify failure**

Run: `cd web && npm test -- --run ResourceChangeProposal ProposalCard`

Expected: FAIL because models and UI do not exist.

- [ ] **Step 3: Implement models, API, and hook**

Use a Zod discriminated union keyed by `resourceKind` and `operation`. The hook exposes `proposal`, `events`, `loading`, `saving`, `confirming`, `canceling`, `saveDraft`, `confirm`, and `cancel`; terminal states disable all mutation controls. Errors use permanent `message.error({duration:0})`.

- [ ] **Step 4: Implement card and review page**

The chat card shows resource kind, operation, target, status, expiry, and “审阅变更”. The unframed review page shows a `Descriptions`/diff table, impact band, event timeline, editable fields for ready proposals, and one primary confirmation command. Do not expose credentials, upload, delete, publish, or tool execution controls.

- [ ] **Step 5: Run frontend verification**

Run: `cd web && npm test -- --run ResourceChangeProposal ProposalCard`

Run: `make fe-lint && make fe-build`

Expected: all commands PASS.

- [ ] **Step 6: Commit review UI**

```bash
git add web/src/modules/agent
git commit -m "[feat](frontend): review resource proposals"
```

## Task 9: Verify Concurrency, Failure, Security, And Browser Chains

**Files:**

- Create: `test/e2e/system_assistant_proposal_test.go`
- Create: `web/e2e/system-assistant-proposal.spec.ts`
- Modify: `docs/agent/agent.md`
- Modify: `docs/agent/agent-chat-flow.md`
- Modify: `docs/SPEC.md`

- [ ] **Step 1: Activate `stratum-e2e-development`**

Read the skill completely and use its real API, backend, PostgreSQL, browser, cleanup, and evidence workflow. Do not print tokens, stored MCP credentials, or raw proposal payloads containing tenant content.

- [ ] **Step 2: Add real backend scenarios**

Cover successful create/update for all four kinds, member denial, system Agent target denial, stale baseline, expired proposal, two concurrent confirm requests, persistence failure, Applier known failure, MCP unknown outcome, tenant A/B isolation, and server restart while confirmed/applying.

- [ ] **Step 3: Add security regression cases**

Attempt payloads containing `token`, `apiKey`, `Authorization`, `password`, `env`, `headers`, delete, publish, upload, arbitrary URL method, SQL, forged tenant/user, and ordinary-Agent proposal-tool calls. Assert rejection before resource service calls and scan DB/trace/log fixtures for the submitted marker secrets.

- [ ] **Step 4: Add browser scenarios**

At desktop/mobile widths, create a proposal from chat, open review, inspect diff/events, edit, confirm once, and observe applied read-back. Separately verify stale, failed, unknown outcome, member visibility, and no overlapping controls.

- [ ] **Step 5: Run focused risk and E2E gates**

Run: `make risk-guardrails`

Run: `make tool-permission-test`

Run: `STRATUM_TEST_POSTGRES_URL=... go test -v -count=1 ./test/e2e -run SystemAssistantProposal`

Run: `cd web && npx playwright test e2e/system-assistant-proposal.spec.ts`

Expected: all commands PASS; CI fails rather than skips when PostgreSQL is missing.

- [ ] **Step 6: Run full verification**

Run: `go vet ./...`

Run: `go test -short ./...`

Run: `go test -v -race -timeout 30s ./...`

Run: `make fe-lint && make fe-build`

Expected: all commands PASS and all started processes are cleaned up.

- [ ] **Step 7: Update current-state documentation**

Document proposal states, typed payloads, baseline strategy, adapter ownership, MCP credential boundary, routes, UI, errors, audit events, unknown-outcome handling, and E2E commands. Remove wording that describes phase 2 as future work only after the real chain passes.

- [ ] **Step 8: Commit E2E and documentation**

```bash
git add test/e2e web/e2e docs/agent docs/SPEC.md
git commit -m "[test](agent): verify resource proposal workflow"
```

## Phase 2 Completion Gate

- [ ] Run `git diff origin/main --check`.
- [ ] Run `make risk-guardrails`, `make tool-permission-test`, race tests, frontend checks, and both E2E suites with fresh output.
- [ ] Confirm every proposal repository method enters tenant context before SQL.
- [ ] Confirm authorization is checked before reads, at confirm, and again before apply.
- [ ] Confirm concurrent confirmation/application produces one Applier call.
- [ ] Confirm stale proposals cannot apply and terminal proposals cannot transition backward.
- [ ] Confirm rejected secret-shaped payloads are not persisted even as invalid raw payloads.
- [ ] Confirm MCP create is `AuthTypeNone`; update preserves but cannot replace credentials.
- [ ] Confirm no delete, Skill publish, candidate deployment, MCP execution, or Knowledge upload path exists.
- [ ] Confirm `unknown_outcome` cannot be retried through API or UI.
