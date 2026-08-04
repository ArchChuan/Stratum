# Deprecate Platform MCP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the external `platform-mcp` MCP server subsystem entirely and restore the system assistant's three platform tools (`stratum_search_official_docs`, `stratum_diagnose_tenant`, `stratum_propose_resource_change`) as in-process agent tools, cleaning up IAM delegation tokens, the internal mTLS control plane, Helm/manifests, tenant schema seeds, monitoring rules, e2e scaffolding, and docs.

**Architecture:** The backend already owns the real capabilities (`officialdocs.Search`, `systemAssistantDiagnosticAdapter`, `ResourceChangeProposalService`) and only used platform-mcp as an out-of-process MCP hop. We reverse phase-1 (commit `ee29e39a`, PR #193's "route platform tools through shared MCP") by restoring the pre-phase-1 internal tool definitions + execution callbacks, then delete every platform-mcp-specific artifact: `cmd/platform-mcp`, `internal/platformmcp`, `pkg/platformmcp`, IAM token exchange/delegation/replay, internal HTTP control plane (incl. the callerless MCP forward handler), platform MCP metrics, Helm templates + values, tenant-schema seeds + `mcp_invocation_jtis`, monitoring rules, e2e stubs, and docs.

**Tech Stack:** Go 1.25 (Gin, pgx, Prometheus client), Helm, tenant schema SQL, bash e2e scripts. No frontend changes (frontend has no platform-mcp references).

---

## Key evidence / baseline facts (verified 2026-08-04)

- Pre-phase-1 in-process implementation exists in git at `ee29e39a^`:
  - `internal/agent/domain/system_assistant_tools.go` (argument parsers, size guards)
  - `internal/agent/application/system_assistant_tools.go` (tool definitions + proposal schema + parse helpers)
  - `internal/agent/application/agent.go` `ExecutionConfig` fields `OfficialDocsSearchFn`/`DiagnosticFn`/`ProposalCreateFn`/`InternalToolResultGuardFn` + options `WithOfficialDocsSearchFn`/`WithDiagnosticFn`/`withProposalCreateFn`/`withInternalToolResultGuard`
  - `internal/agent/application/graph/react.go` switch branches for the three tool names
- Surviving pieces to reuse (already in current code): `domain.BoundCitations`, `domain.BoundDiagnosticEvidence`, `domain.SystemAssistantToolArtifact`, `domain.ResourceChangeProposalArtifact`, `domain.DiagnosticRequest/Evidence/Area`, `pkg/constants.SystemAssistantToolTimeout/SystemAssistantToolMaxJSONBytes/SystemAssistantQueryMaxRunes/SystemAssistantAreasMaxCount`, `react.go` `GovernedAssistant`/`platformMCPArtifact`/`isPlatformMCPTool`/`classifyToolProvider` special-case (needs de-coupling from `pkg/platformmcp`), `api/wiring/agent.go` `deps.OfficialDocsSearch = officialdocs.Search` and `a.DiagnosticProvider`.
- The system assistant agent row + platform MCP server row + tool links are seeded in `pkg/storage/postgres/tenant_schema.sql` (idempotent DO blocks + `ON CONFLICT DO NOTHING`), with `mcp_invocation_jtis` replay table.
- The internal router (`api/http/internal_router.go`) is the only consumer of IAM token exchange/delegation; its `MCPForward` endpoint has **zero production callers** (`SetForwardHTTPTransport` never called; `GetOwnerNode`/`ForwardToolCall` only referenced by dead handler). Node ownership heartbeat/failover in `internal/mcp/infrastructure/client_manager.go` is separate and stays.
- `NewPrometheusMetrics` registers reaper metrics for every process; removing the platform-mcp binary removes its always-zero `reaper_last_cycle_timestamp_seconds` series and the permanent `StratumReaperDown` alert on `job="stratum-platform-mcp"`. The backend startup-window alert noise is a separate rule-level issue, explicitly out of scope for this plan (tracked at the end).

## File structure map

### Deleted wholesale

- `cmd/platform-mcp/` (main.go, runtime.go, config.go, observability.go + tests)
- `internal/platformmcp/` (server/, application/, infrastructure/)
- `pkg/platformmcp/` (contract.go, claims.go)
- `api/wiring/platform_mcp.go`, `api/wiring/platform_mcp_transport.go`, `api/wiring/platform_mcp_test.go`
- `api/http/internal_router.go`, `api/http/handler/mcp_forward_handler.go`, `api/http/handler/platform_assistant_capability_handler.go` (if only used by internal router), `api/http/handler/observed_mcp_token_exchange_handler.go`
- `api/middleware/mtls_identity.go`, `api/middleware/delegation_jwt.go` (+ tests)
- `internal/iam/application/mcp_token_exchange.go`, `internal/iam/infrastructure/token/delegation.go`, `internal/iam/infrastructure/persistence/mcp_token_replay_repo.go` (+ tests)
- `internal/iam/domain/port/mcp_exchange.go`, `internal/iam/domain/port/delegation_token.go`
- `internal/mcp/infrastructure/forward.go`, `internal/mcp/infrastructure/mcpnode/` (verify no other consumers)
- `cmd/server/internal_server.go` (+ test)
- `helm/templates/platform-mcp-deployment.yaml`, `platform-mcp-service.yaml`, `platform-mcp-servicemonitor.yaml`, `platform-mcp-prometheusrule.yaml`, `platform-mcp-networkpolicy.yaml`, `platform-mcp-serviceaccount.yaml`, `internal-certificates.yaml`
- `scripts/e2e/system-stateful*.sh` platform-mcp sections; `test/e2e/cmd/platform-assistant-stubs/`
- `scripts/quality/check-platform-mcp-rendering-test.sh` (verify and update or delete)

### Restored / reworked

- Create `internal/agent/domain/system_assistant_tools.go` (restore from `ee29e39a^`, adapt imports)
- Create `internal/agent/application/system_assistant_tools.go` (restore + adapt)
- Modify `internal/agent/application/graph/react.go` (in-process exec branches; remove `pkg/platformmcp` import)
- Modify `internal/agent/application/agent.go` (ExecutionConfig fields + options)
- Modify `internal/agent/application/agent_service.go` (system assistant branch; remove platform MCP tool filter/guard; simplify bindings)
- Modify `internal/agent/application/system_assistant_profile.go` (drop MCPToolIDs handling for system assistant)
- Modify `api/wiring/agent.go` (wire in-process callbacks)
- Modify `api/wiring/system_assistant.go` (keep diagnostic adapter; remove platform-MCP-only pieces)
- Modify `api/wiring/wiring.go` (remove `buildPlatformMCP` registration + `PlatformMCP` field)
- Modify `cmd/server/runtime.go` (remove internal HTTP server registration)
- Modify `config/config.go` + `config/config_test.go` (remove `InternalAPI`)
- Modify `internal/mcp/infrastructure/client.go`, `client_manager.go`, `api/wiring/mcp.go` (remove managed transport/invocation credential plumbing)
- Modify `pkg/storage/postgres/tenant_schema.sql` + tests (cleanup seeds + replay table)
- Modify `pkg/observability/prometheus.go` + tests (remove platform MCP metrics)
- Modify `helm/values.yaml`, `helm/values-prod.yaml` (remove `platformMCP` section)
- Modify `docs/agent/*`, `docs/operations/alerts/*`, superpowers specs/plans (mark deprecated)
- Rewrite `internal/agent/application/system_assistant_mcp_test.go` → in-process expectations

---

## Task 1: Restore domain-side system assistant tool parsers

**Files:**

- Create: `internal/agent/domain/system_assistant_tools.go`
- Test: `internal/agent/domain/system_assistant_tools_test.go`

- [ ] **Step 1: Restore the file from git history**

`git show ee29e39a^:internal/agent/domain/system_assistant_tools.go` — restore verbatim (it contains `ParseOfficialDocsToolArguments`, `ParseDiagnosticToolArguments`, `boundedToolArgumentsJSON`, `toolArgumentsSize`, and `decodeClosed`; the file is self-contained using `pkg/constants` and `pkg/safetext`).

- [ ] **Step 2: Restore its test file from git history**

`git show ee29e39a^:internal/agent/domain/system_assistant_tools_test.go` (adapt to testify if needed).

- [ ] **Step 3: Run the domain tests**

Run: `go test ./internal/agent/domain/... -count=1`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/domain/system_assistant_tools.go internal/agent/domain/system_assistant_tools_test.go
git commit -m "feat(agent): restore in-process system assistant tool parsers"
```

## Task 2: Restore application-side system assistant tool definitions and executors

**Files:**

- Create: `internal/agent/application/system_assistant_tools.go`
- Test: `internal/agent/application/system_assistant_tools_test.go`

- [ ] **Step 1: Restore definitions from git history**

`git show ee29e39a^:internal/agent/application/system_assistant_tools.go` — restore. Verify every referenced constant/type still exists: `constants.SystemAssistantQueryMaxRunes`, `constants.SystemAssistantAreasMaxCount`, `constants.SystemAssistantToolMaxJSONBytes`, `domain.ResourceKind`, `domain.ProposalOperation`, `domain.DiagnosticArea`, `domain.ResourceChangeProposalArtifact`. The file also contains `guardInternalAssistantEvidence` and `safeAssistantToolError` helpers used by react.go.

- [ ] **Step 2: Restore/adapt the test file**

`git show ee29e39a^:internal/agent/application/system_assistant_tools_test.go` — adapt to current `domain` package layout.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/agent/application/... -run 'SystemAssistant|Proposal|OfficialDocs' -count=1`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/application/system_assistant_tools.go internal/agent/application/system_assistant_tools_test.go
git commit -m "feat(agent): restore in-process system assistant tool definitions"
```

## Task 3: Restore execution fields/options and react dispatch branches

**Files:**

- Modify: `internal/agent/application/agent.go`
- Modify: `internal/agent/application/graph/react.go`
- Test: `internal/agent/application/graph/react_test.go`, `internal/agent/application/graph/platform_mcp_artifact_test.go`

- [ ] **Step 1: Add ExecutionConfig fields + options in `agent.go`**

Add to `ExecutionConfig`:

```go
OfficialDocsSearchFn     func(context.Context, string) ([]domain.Citation, error)
DiagnosticFn             func(context.Context, []domain.DiagnosticArea) (domain.DiagnosticEvidence, error)
ProposalCreateFn         func(context.Context, map[string]any) (domain.ResourceChangeProposalArtifact, error)
InternalToolResultGuardFn func(any) (port.GuardedToolResult, error)
```

Add options (from `ee29e39a^:internal/agent/application/agent.go:949-960`): `WithOfficialDocsSearchFn`, `WithDiagnosticFn`, `withProposalCreateFn`, `withInternalToolResultGuard`.

- [ ] **Step 2: Add dispatch branches in `react.go`**

In `dispatchToolCall`, before the default `execMCPTool`, add cases for `stratum_search_official_docs`, `stratum_diagnose_tenant`, `stratum_propose_resource_change` calling new exec functions (`execOfficialDocsSearchTool`, `execDiagnoseTenantTool`, `execProposeResourceChangeTool`) modeled on `ee29e39a^:internal/agent/application/graph/react.go:429-500` (parse args → timeout context with `constants.SystemAssistantToolTimeout` → call fn → `domain.BoundCitations`/`domain.BoundDiagnosticEvidence` → `guardInternalAssistantEvidence(s.InternalToolResultGuardFn, ...)` → append `SystemAssistantToolArtifact` → return `toolExecResult`).

- [ ] **Step 3: De-couple react.go from `pkg/platformmcp`**

Replace `platformmcp.ToolSearchOfficialDocs` / `ToolDiagnoseTenant` / `ToolProposeResourceChange` / `Phase1ToolNames` usages in `platformMCPArtifact`, `isPlatformMCPTool`, `recordToolErrorArtifact`, and the `classifyToolProvider` switch with local string constants defined in the agent domain (`domain.SystemAssistantToolSearchOfficialDocs` etc.) or an application-level const block in `system_assistant_tools.go`. Remove the `pkg/platformmcp` import from react.go.

- [ ] **Step 4: Restore/adapt tests**

`platform_mcp_artifact_test.go` → rename references to local tool-name constants; add react dispatch tests for the three in-process tools (from `ee29e39a^` react tests if present).

- [ ] **Step 5: Run tests**

Run: `go test ./internal/agent/application/graph/... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/application/agent.go internal/agent/application/graph/react.go internal/agent/application/graph/react_test.go internal/agent/application/graph/platform_mcp_artifact_test.go
git commit -m "feat(agent): execute system assistant platform tools in-process"
```

## Task 4: Rewire assembleOptions to inject in-process tools

**Files:**

- Modify: `internal/agent/application/agent_service.go`
- Modify: `internal/agent/application/system_assistant_profile.go`
- Modify: `internal/agent/domain/port/repository.go`, `internal/agent/infrastructure/persistence/agent_repo.go`, `internal/agent/application/registry.go`
- Modify: `api/wiring/agent.go`
- Test: `internal/agent/application/system_assistant_mcp_test.go` (rewrite), `agent_service_test.go`, `agent_repo_test.go`, `system_assistant_profile_test.go`

- [ ] **Step 1: Add deps + in-process options in `assembleOptions`**

In `AgentServiceDeps` add:

```go
OfficialDocsSearchFn func(context.Context, string) ([]domain.Citation, error)
ProposalCreateFn     func(context.Context, map[string]any) (domain.ResourceChangeProposalArtifact, error)
```

In `assembleOptions`, replace the system assistant MCP branch:

- for system assistant: `extraTools = SystemAssistantToolDefinitionsForRole(roleClass)` (computed after the existing `Authorize` call) instead of `buildExtraToolsChecked` MCP resolution;
- append `WithOfficialDocsSearchFn(s.deps.OfficialDocsSearchFn)`, `WithDiagnosticFn(func(ctx, areas) { return s.deps.DiagnosticProvider.Collect(...) })`, `withProposalCreateFn(s.deps.ProposalCreateFn)`, `withInternalToolResultGuard(...)` (restore a bounded JSON guard like `guardInternalAssistantEvidence`).
- Keep ordinary agents on `buildExtraToolsChecked` with `mcpToolIDs = a.GetConfig().MCPToolIDs` (remove `withoutPlatformMCPTools` call).

- [ ] **Step 2: Remove `withoutPlatformMCPTools` / `containsPlatformMCPToolID`**

Delete the `platformMCPToolIDPrefix` const, `withoutPlatformMCPTools`, `containsPlatformMCPToolID`, `ErrPlatformMCPBindingForbidden` guard in `Create`/`Update`; update tests (`agent_service_test.go`, `agent_service_extra_test.go`).

- [ ] **Step 3: Simplify system assistant bindings**

`UpdateSystemAssistantBindings` loses the `mcpToolIDs` parameter (port, repo SQL `replaceMCPTools` call removed for the system assistant, registry, service `updateSystemAssistant`); keep skills/knowledge bindings. Update handler test doubles.

- [ ] **Step 4: Wire callbacks in `api/wiring/agent.go`**

```go
deps.OfficialDocsSearchFn = deps.OfficialDocsSearch
deps.ProposalCreateFn = func(ctx context.Context, payload map[string]any) (domain.ResourceChangeProposalArtifact, error) {
    return a.ProposalService.Create(ctx, payload)  // verify exact service signature
}
```

Remove `deps.OfficialDocsSearch` if no longer referenced elsewhere (or keep and alias).

- [ ] **Step 5: Update profile wiring**

`system_assistant_profile.go` + `api/wiring/agent.go` system assistant seed: stop persisting/expecting MCPToolIDs for the system assistant.

- [ ] **Step 6: Run tests**

Run: `go test ./internal/agent/... ./api/wiring/... -count=1`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/agent api/wiring/agent.go
git commit -m "refactor(agent): route system assistant platform tools in-process"
```

## Task 5: Delete IAM platform-MCP token infrastructure

**Files:**

- Delete: `internal/iam/application/mcp_token_exchange.go`, `internal/iam/infrastructure/token/delegation.go`, `internal/iam/infrastructure/persistence/mcp_token_replay_repo*.go`, `internal/iam/domain/port/mcp_exchange.go`, `internal/iam/domain/port/delegation_token.go` (+ all their tests)
- Modify: `api/wiring/iam.go` or wherever `NewMCPTokenExchange`/delegation are wired (remove)

- [ ] **Step 1: Verify zero remaining references after deletion**

Run: `rg -n "MCPTokenExchange|APIDelegation|InvocationClaims|InvocationReplay|DelegationToken|PlatformMCPBinding" --glob '*.go' -g '!vendor'`
Expected: no production references (only this task's leftovers to clean).

- [ ] **Step 2: Remove wiring + run tests**

Run: `go test ./internal/iam/... ./api/... -count=1`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add -A internal/iam api/wiring api/http api/middleware
git commit -m "refactor(iam): remove platform MCP token exchange and delegation"
```

## Task 6: Delete the internal HTTP control plane

**Files:**

- Delete: `cmd/server/internal_server.go` (+ test), `api/http/internal_router.go`, `api/http/handler/mcp_forward_handler.go`, `api/http/handler/platform_assistant_capability_handler.go`, `api/http/handler/observed_mcp_token_exchange_handler.go`, `api/middleware/mtls_identity.go`, `api/middleware/delegation_jwt.go` (+ tests)
- Modify: `cmd/server/runtime.go` (remove `registerInternalHTTPServer` call), `config/config.go` + `config/config_test.go` (remove `InternalAPIConfig`, `InternalAPI` field, `PlatformMCPDialAddress` validation), `.env.example` if present
- Modify: `internal/mcp/infrastructure/forward.go` → delete file; `client_manager.go` → remove `fwdTransport` field/`SetForwardHTTPTransport`/`GetOwnerNode`/`ForwardToolCall` references; check `mcpnode` package consumers

- [ ] **Step 1: Delete internal server/router files and update runtime/config**

- [ ] **Step 2: Verify no consumers of removed endpoints**

Run: `rg -n "internal/livez|internal/mcp/tools/call|internal/platform-mcp|internal/platform-assistant|InternalAPI|InternalAPIConfig|RequirePlatformMCPIdentity|RequireDelegatedScope" --glob '*.go' -g '!vendor'`
Expected: no production references.

- [ ] **Step 3: Update tests and run**

Run: `go test ./cmd/server/... ./api/... ./config/... ./internal/mcp/... -count=1`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add -A cmd/server api/http api/middleware config internal/mcp
git commit -m "refactor(server): remove platform MCP internal control plane"
```

## Task 7: Remove MCP managed-transport plumbing and wiring

**Files:**

- Modify: `internal/mcp/infrastructure/client.go` (remove `SetManagedHTTPTransportProvider`, `SystemKey`/`ManagementPlatform` special paths, invocation credential provider), `client_manager.go` (remove managed transport plumbing), `api/wiring/mcp.go` (remove `SetManagedHTTPTransportProvider` call)
- Delete: `api/wiring/platform_mcp.go`, `api/wiring/platform_mcp_transport.go`, `api/wiring/platform_mcp_test.go`
- Test: `internal/mcp/infrastructure/client_invocation_test.go`, `client_manager_managed_test.go` (rewrite/delete platform cases)

- [ ] **Step 1: Remove plumbing and verify no references**

Run: `rg -n "ManagedHTTPTransport|InvocationCredential|SystemServerKey|ManagementPlatform|platformMCPTransport|verifyPlatformMCP" --glob '*.go' -g '!vendor'`
Expected: no production references.

- [ ] **Step 2: Run MCP tests**

Run: `go test ./internal/mcp/... ./api/wiring/... -count=1`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add -A internal/mcp api/wiring
git commit -m "refactor(mcp): remove platform MCP managed transport plumbing"
```

## Task 8: Remove platform MCP metrics

**Files:**

- Modify: `pkg/observability/prometheus.go` (remove `platformMCP` field, `InitPlatformMCPMetrics`, `platform_mcp_*` recorders), `pkg/observability/observability.go` (provider interface if it exposes platform MCP methods)
- Test: `pkg/observability/prometheus_test.go`, `observability_test.go`, `pkg/platformmcp` contract tests

- [ ] **Step 1: Remove metric family and verify**

Run: `rg -n "PlatformMCP|platform_mcp" pkg/observability --glob '*.go'`
Expected: no references.

- [ ] **Step 2: Run observability tests**

Run: `go test ./pkg/observability/... -count=1`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add -A pkg/observability
git commit -m "refactor(observability): remove platform MCP metrics"
```

## Task 9: Update tenant schema with idempotent cleanup

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql`
- Test: `pkg/storage/postgres/tenant_schema_test.go`, `tenant_schema_integration_test.go`

- [ ] **Step 1: Replace platform-mcp seed blocks with cleanup**

Replace the `mcp_configs` seed DO block and identity-conflict guard with:

```sql
DELETE FROM agent_mcp_tool_links WHERE server_id = 'stratum-platform-mcp';
DELETE FROM mcp_configs WHERE id = 'stratum-platform-mcp';
```

Replace the `agent_mcp_tool_links` seed + guard block with the DELETE above (keep table DDL). Replace `mcp_invocation_jtis` CREATE TABLE + index with:

```sql
DROP TABLE IF EXISTS mcp_invocation_jtis;
```

- [ ] **Step 2: Update schema tests**

`tenant_schema_test.go` asserts the absence of `stratum-platform-mcp` rows and presence of cleanup; `tenant_schema_integration_test.go` verifies existing tenants end with no platform-mcp rows and no `mcp_invocation_jtis` table.

- [ ] **Step 3: Run tests**

Run: `go test ./pkg/storage/postgres/... -count=1`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add pkg/storage/postgres/tenant_schema.sql pkg/storage/postgres/tenant_schema_test.go pkg/storage/postgres/tenant_schema_integration_test.go
git commit -m "chore(schema): remove platform MCP seeds and invocation replay table"
```

## Task 10: Remove Helm deployment, values, and monitoring rules

**Files:**

- Delete: `helm/templates/platform-mcp-deployment.yaml`, `platform-mcp-service.yaml`, `platform-mcp-servicemonitor.yaml`, `platform-mcp-prometheusrule.yaml`, `platform-mcp-networkpolicy.yaml`, `platform-mcp-serviceaccount.yaml`, `internal-certificates.yaml`
- Modify: `helm/values.yaml`, `helm/values-prod.yaml` (remove `platformMCP:` section + image entry), `helm/Chart.yaml` (if image list), any secret/configmap templates referencing platform MCP TLS

- [ ] **Step 1: Verify Helm renders without platform MCP**

Run: `helm template ./helm >/tmp/stratum-helm-render.yaml` (from worktree) — inspect output has no `stratum-platform-mcp` resources.
Also run the repo's rendering check if present: `bash scripts/quality/check-platform-mcp-rendering-test.sh` (update/delete as needed).

- [ ] **Step 2: Update monitoring docs**

`docs/operations/alerts/business.md` and any runbook referencing `StratumPlatformMCP*` — remove/annotate deprecated.

- [ ] **Step 3: Commit**

```bash
git add -A helm docs/operations
git commit -m "chore(helm): remove platform MCP deployment and alert rules"
```

## Task 11: Update e2e scripts and remove stubs

**Files:**

- Modify: `scripts/e2e/system-stateful.sh`, `system-stateful-test.sh`, `system-stateful-behavior-test.sh` (remove platform-mcp startup, TLS cert generation, health poll, port entries)
- Delete: `test/e2e/cmd/platform-assistant-stubs/`
- Check: `.test/verification.yaml`, `test/e2e/stateful/*` for platform-mcp capability names; historical attestations are kept as-is

- [ ] **Step 1: Remove platform-mcp process/cert plumbing from e2e scripts**

Verify with `rg -n -i "platform.mcp" scripts/e2e test/e2e --glob '*.sh' --glob '*.go'` → no references.

- [ ] **Step 2: Commit**

```bash
git add -A scripts/e2e test/e2e
git commit -m "test(e2e): drop platform MCP stubs and startup"
```

## Task 12: Update docs and superpowers plans/specs

**Files:**

- Modify: `docs/agent/architecture.md`, `docs/agent/deployment-architecture.md`, `docs/agent/backend-go.md` (if referenced), `docs/evidence/platform-mcp-protocol-2026-07-29.md`, `docs/superpowers/specs/2026-07-29-platform-mcp-control-plane-design.md`, `docs/superpowers/plans/2026-07-29-platform-mcp-phase-1.md`, `docs/superpowers/plans/2026-07-30-parallel-stateful-e2e-phase-one.md`, `docs/superpowers/specs/2026-07-30-parallel-stateful-e2e-phase-one-design.md`

- [ ] **Step 1: Annotate deprecated**

Add a clear "DEPRECATED / 已废弃 (2026-08-04): platform-mcp 整体移除，系统助手改为进程内直调" banner to each; remove platform-mcp from architecture/deployment docs.

- [ ] **Step 2: Commit**

```bash
git add -A docs
git commit -m "docs(platform): mark platform MCP deprecated and removed"
```

## Task 13: Full verification and PR

- [ ] **Step 1: Static + short tests**

Run: `go vet ./...` then `go test -short ./... -count=1` (from worktree; use `STRATUM_WORKTREE_ROOT` symlink setup if frontend tests need node_modules).

- [ ] **Step 2: Quality gates**

Run: `make code-quality` and `make risk-guardrails` (from worktree).

- [ ] **Step 3: System verification**

Run: `make test-verify-before-pr` (per stratum-e2e-development skill; platform-MCP removal is an MCP/IAM capability change → R3 soaks apply). Fix any failures before proceeding.

- [ ] **Step 4: Update the reaper alert note**

The backend `StratumReaperDown` startup-window noise remains after this change; it is intentionally out of scope (rule guard is a separate small PR).

- [ ] **Step 5: Push and create PR**

```bash
git push -u origin feat/deprecate-platform-mcp
gh pr create --base main --title "refactor(platform): deprecate platform MCP and use in-process assistant tools" --body "..."
```

PR description includes What / Why / HowToTest per AGENTS.md.

- [ ] **Step 6: Check base freshness before relying on CI**

`git fetch origin main` and compare base commit; if behind, merge latest main, re-verify, push.

## Out of scope (tracked separately)

- Backend `StratumReaperDown` post-deploy false positive (gauge=0 until first cycle) — fix via alert rule `> 0` guard in a separate change.
- Unrelated startup `seed.builtin_docs.ingest_failed` UUID errors.
