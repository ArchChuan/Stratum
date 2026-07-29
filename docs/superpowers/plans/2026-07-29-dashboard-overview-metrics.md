# Dashboard Overview Metrics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show eight accurate tenant-scoped counts on the Dashboard, including model providers, members, workflows, and user-to-Agent messages from the rolling previous 168 hours.

**Architecture:** Add a member-readable Platform Dashboard read model backed by one tenant-bound PostgreSQL aggregate query and expose it through `GET /dashboard/overview`. Replace the Dashboard's four full-list requests with one typed API request and render the returned eight metrics responsively.

**Tech Stack:** Go 1.25, Gin, pgx v5/pgxmock, PostgreSQL, React 18, TypeScript, Axios, Zod, Ant Design, Vitest, Testing Library, stateful Playwright E2E.

---

## Task 1: Platform Dashboard Application Contract

**Files:**

- Create: `internal/platform/domain/dashboard.go`
- Create: `internal/platform/domain/port/dashboard.go`
- Create: `internal/platform/application/dashboard_service.go`
- Test: `internal/platform/application/dashboard_service_test.go`

- [ ] **Step 1: Write failing service tests**

Define a fake `DashboardRepository` and assert that `Overview(ctx, "tenant-1")` passes the tenant ID through and returns all eight fields. Add tests asserting an empty tenant ID is rejected before the repository is called and repository errors are wrapped and propagated.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/platform/application -run Dashboard -count=1`

Expected: FAIL because the Dashboard domain value, repository port, and service do not exist.

- [ ] **Step 3: Implement the minimal contract**

Create `domain.DashboardOverview` with fields `Agents`, `Skills`, `KnowledgeWorkspaces`, `MCPServers`, `ModelProviders`, `TenantMembers`, `Workflows`, and `AgentUserMessages7d`. Define:

```go
type DashboardRepository interface {
    Overview(ctx context.Context, tenantID string) (domain.DashboardOverview, error)
}
```

Implement `DashboardService.Overview` to reject `strings.TrimSpace(tenantID) == ""`, call the repository once, and wrap errors as `platform: dashboard overview: %w`.

- [ ] **Step 4: Verify GREEN**

Run: `go test ./internal/platform/application -run Dashboard -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

Commit: `[feat](dashboard): define tenant overview service`

## Task 2: Tenant-Bound PostgreSQL Aggregate

**Files:**

- Create: `internal/platform/infrastructure/persistence/dashboard_repository.go`
- Test: `internal/platform/infrastructure/persistence/dashboard_repository_test.go`

- [ ] **Step 1: Write the failing repository test**

Follow the pgxmock and tenant-context style in existing persistence tests. Expect a transaction, `SET LOCAL search_path`, and one row returning eight counts. Assert the query contains counts for `agents`, `skills`, `rag_workspaces`, `mcp_configs`, `providers`, `public.tenant_members`, `workflow_definitions`, and:

```sql
SELECT COUNT(*) FROM chat_messages
WHERE role = 'user' AND created_at >= NOW() - INTERVAL '168 hours'
```

Assert the member subquery receives the explicit tenant ID. Add rollback/error propagation coverage and a mismatched tenant-context test.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/platform/infrastructure/persistence -run Dashboard -count=1`

Expected: FAIL because `DashboardRepository` does not exist.

- [ ] **Step 3: Implement the minimal repository**

Use `pkg/storage/postgres.ExecTenant` with `postgres.WithTenant` only after validating that any existing tenant context matches the explicit `tenantID`. Execute one `QueryRow` with scalar subqueries for all eight counts. Schema-qualify only `public.tenant_members`; all other tables resolve inside the tenant transaction.

- [ ] **Step 4: Verify GREEN**

Run: `go test ./internal/platform/infrastructure/persistence -run Dashboard -count=1`

Expected: PASS, including rollback and tenant mismatch cases.

- [ ] **Step 5: Commit**

Commit: `[feat](dashboard): aggregate tenant overview counts`

## Task 3: HTTP Handler, Wiring, RBAC, And Contract

**Files:**

- Create: `api/http/dto/dashboard.go`
- Create: `api/http/handler/dashboard_handler.go`
- Test: `api/http/handler/dashboard_handler_test.go`
- Modify: `api/wiring/platform.go`
- Modify: `api/http/router.go`
- Modify: `api/http/contract_test.go`
- Create: `api/http/testdata/contracts/get_dashboard_overview.golden.json`

- [ ] **Step 1: Write failing handler and route tests**

Test that the handler maps all fields to these JSON keys: `agents`, `skills`, `knowledge_workspaces`, `mcp_servers`, `model_providers`, `tenant_members`, `workflows`, and `agent_user_messages_7d`. Assert missing tenant context fails closed and service errors reach common middleware. Add router coverage proving a member token can GET `/dashboard/overview` while missing authentication is rejected.

- [ ] **Step 2: Verify RED**

Run: `go test ./api/http/handler ./api/http -run 'Dashboard|Contracts' -count=1`

Expected: FAIL because the handler, route, wiring field, and golden contract do not exist.

- [ ] **Step 3: Implement handler and wiring**

Add `DashboardService *platformapp.DashboardService` to `wiring.Platform`; initialize it when PostgreSQL is available using the Platform persistence repository. Register the route with `protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))`. Keep the handler limited to tenant extraction, service invocation, DTO mapping, and `c.Error(err)`.

- [ ] **Step 4: Add the frozen contract**

Wire a fake Dashboard repository/service into the contract router, authenticate `/dashboard/` contract cases, and add a golden response containing eight zero values.

- [ ] **Step 5: Verify GREEN**

Run: `go test ./api/http/handler ./api/http ./api/wiring -run 'Dashboard|Contracts|Platform' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

Commit: `[feat](dashboard): expose overview metrics API`

## Task 4: Typed Frontend Dashboard Client And Hook

**Files:**

- Create: `web/src/modules/dashboard/api/dashboard.api.ts`
- Test: `web/src/modules/dashboard/api/dashboard.api.test.ts`
- Modify: `web/src/modules/dashboard/model/dashboard.ts`
- Modify: `web/src/modules/dashboard/index.ts`
- Modify: `web/src/modules/dashboard/hooks/useDashboardPage.ts`
- Modify: `web/src/modules/dashboard/hooks/__tests__/useDashboardPage.test.ts`

- [ ] **Step 1: Write failing API and hook tests**

Assert `dashboardApi.overview()` calls the shared client at `/dashboard/overview` and rejects malformed payloads through a Zod schema. Update the hook test to mock only `dashboardApi.overview`, return all eight nonzero values, and assert one call plus an atomic count update. Add a rejection test asserting the zero defaults remain and `message.error` is called with `{ content: '加载概览数据失败', duration: 0 }`.

- [ ] **Step 2: Verify RED**

Run: `npm --prefix web test -- src/modules/dashboard/api/dashboard.api.test.ts src/modules/dashboard/hooks/__tests__/useDashboardPage.test.ts --run`

Expected: FAIL because the Dashboard API and new count fields do not exist.

- [ ] **Step 3: Implement the typed API and hook**

Define the eight-field Zod schema, export its inferred type, call `api.get('/dashboard/overview')`, and parse `response.data`. Replace the four cross-module list imports and `Promise.allSettled` logic with the single overview request while preserving the cancelled-effect guard and loading cleanup.

- [ ] **Step 4: Verify GREEN**

Run: `npm --prefix web test -- src/modules/dashboard/api/dashboard.api.test.ts src/modules/dashboard/hooks/__tests__/useDashboardPage.test.ts --run`

Expected: PASS with no React warnings.

- [ ] **Step 5: Commit**

Commit: `[feat](dashboard): load overview metrics`

## Task 5: Eight Responsive Statistic Cards

**Files:**

- Modify: `web/src/modules/dashboard/pages/DashboardPage.tsx`
- Modify: `web/src/modules/dashboard/pages/__tests__/DashboardPage.test.tsx`

- [ ] **Step 1: Write the failing rendering test**

Return all eight counts from the mocked hook and assert the four new labels and values render alongside the original labels. Inspect the rendered Ant Design column classes and require `ant-col-xs-24`, `ant-col-sm-12`, and `ant-col-lg-6` for each card.

- [ ] **Step 2: Verify RED**

Run: `npm --prefix web test -- src/modules/dashboard/pages/__tests__/DashboardPage.test.tsx --run`

Expected: FAIL because the new cards and four-column large-screen span are absent.

- [ ] **Step 3: Implement the card list**

Add Ant Design icons for model providers, members, workflows, and recent messages. Add the four card specs in confirmed order and use `xs={24} sm={12} lg={6}` without the current flex override so the grid is stable at one, two, and four columns.

- [ ] **Step 4: Verify GREEN**

Run: `npm --prefix web test -- src/modules/dashboard/pages/__tests__/DashboardPage.test.tsx --run`

Expected: PASS.

- [ ] **Step 5: Commit**

Commit: `[feat](dashboard): display expanded overview cards`

## Task 6: Regression And System Acceptance

**Files:**

- Modify only files required by failures proven in this task.

- [ ] **Step 1: Run focused suites**

Run the new Go package tests, `go test ./api/http/... ./api/wiring/... ./internal/platform/... -count=1`, and all Dashboard Vitest files. Expected: PASS.

- [ ] **Step 2: Run backend and frontend gates**

Run `go vet ./...`, `go test -short ./...`, `make fe-lint`, and `make fe-build`. Expected: all exit 0.

- [ ] **Step 3: Run risk gates**

Run `make risk-guardrails`, then `bash scripts/quality/risk-regression-guard.sh --acceptance <every-changed-file>`. Expected: guardrails pass and selector reports `short`; if it reports `soak`, run the exact soak command it prints.

- [ ] **Step 4: Run system E2E**

Run `STATEFUL_E2E_PACKS=dashboard make e2e-system-short`, monitor to terminal state, then run `make e2e-attestation-check`. Verify the Dashboard pack records browser action, HTTP evidence for `/dashboard/overview`, persistence reconciliation, cleanup success, and no skipped/unverified capability.

- [ ] **Step 5: Final diff review and commit**

Run `git diff --check`, `git status --short`, and inspect `git diff origin/main...HEAD`. Commit only any verification-driven fixes with `[fix](dashboard): close overview acceptance gaps`.
