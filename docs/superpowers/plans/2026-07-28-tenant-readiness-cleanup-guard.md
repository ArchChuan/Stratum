# Tenant Readiness And Cleanup Guard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fail readiness when the default tenant invariant is broken and make remote guest cleanup incapable of deleting the shared default tenant.

**Architecture:** A read-only PostgreSQL invariant check joins the existing component readiness map. The Playwright support layer owns guest cleanup and limits it to the generated user, with a separately tested fail-closed tenant classification.

**Tech Stack:** Go 1.25, pgx v5, Gin, React/TypeScript, Vitest, Playwright, PostgreSQL.

---

## Task 1: Add The Default Tenant Readiness Invariant

**Files:**

- Create: `pkg/storage/postgres/readiness.go`
- Create: `pkg/storage/postgres/readiness_test.go`

- [ ] **Step 1: Write failing tests**

Test a small query interface with pgxmock for: active default tenant plus `agents` table passes; no row returns `default tenant missing`; false table predicate returns `default tenant schema missing`; query errors remain wrapped.

- [ ] **Step 2: Verify RED**

Run: `go test ./pkg/storage/postgres -run TestCheckDefaultTenantReadiness -count=1`

Expected: FAIL because `CheckDefaultTenantReadiness` does not exist.

- [ ] **Step 3: Implement the minimal read-only query**

Use one `public.tenants` query with an `information_schema.tables` `EXISTS` predicate for `tenant_<id>.agents`. Return explicit errors for no row and false schema evidence.

- [ ] **Step 4: Verify GREEN**

Run: `go test ./pkg/storage/postgres -run TestCheckDefaultTenantReadiness -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/storage/postgres/readiness.go pkg/storage/postgres/readiness_test.go
git commit -m "[fix](storage): check default tenant readiness"
```

## Task 2: Wire The Invariant Into Runtime Readiness

**Files:**

- Modify: `cmd/server/runtime.go`
- Modify: `cmd/server/runtime_test.go`

- [ ] **Step 1: Write failing composition tests**

Replace the ping-only fake with a readiness function and prove both a PostgreSQL failure and a tenant-invariant failure appear under the `postgres` component key while the base component map is preserved.

- [ ] **Step 2: Verify RED**

Run: `go test ./cmd/server -run TestWithPostgresReadiness -count=1`

Expected: FAIL because the runtime wrapper cannot execute the invariant check.

- [ ] **Step 3: Implement minimal wiring**

Make `withPostgresReadiness` accept `func(context.Context) error`. In `Run`, supply a closure that rejects a nil pool, calls `Ping`, then calls `postgres.CheckDefaultTenantReadiness`.

- [ ] **Step 4: Verify GREEN and router regression**

Run: `go test ./cmd/server ./api/http -run 'TestWithPostgresReadiness|TestReadinessHandler' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/server/runtime.go cmd/server/runtime_test.go
git commit -m "[fix](server): fail readiness on missing default tenant"
```

## Task 3: Add Fail-Closed Guest Cleanup

**Files:**

- Modify: `web/e2e/support/real-platform-assistant.ts`
- Modify: `web/e2e/support/real-platform-assistant.test.ts`
- Modify: `web/e2e/system-assistant-real.spec.ts`

- [ ] **Step 1: Write failing helper tests**

Add tests proving `classifyCleanupTenant('t')` accepts exactly one `true` row for guest-user cleanup, rejects `false`, empty, and multi-row output, and `requireDisposableTenant` rejects `true` while accepting exactly one `false` row.

- [ ] **Step 2: Verify RED**

Run: `web/node_modules/.bin/vitest run --config web/vitest.e2e.config.ts web/e2e/support/real-platform-assistant.test.ts`

Expected: FAIL because the cleanup guards do not exist.

- [ ] **Step 3: Implement user-only cleanup**

Add guarded helpers that validate UUIDs, read `public.tenants.is_default`, delete only the exact user, and assert one deleted row. Do not add `DROP SCHEMA` or tenant deletion to guest cleanup.

- [ ] **Step 4: Use cleanup in real specs**

Track admin/member sessions and call `cleanupPlatformAssistantSession` from `finally`, after closing browser contexts.

- [ ] **Step 5: Verify GREEN**

Run: `web/node_modules/.bin/vitest run --config web/vitest.e2e.config.ts web/e2e/support/real-platform-assistant.test.ts`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/e2e/support/real-platform-assistant.ts web/e2e/support/real-platform-assistant.test.ts web/e2e/system-assistant-real.spec.ts
git commit -m "[fix](e2e): guard shared tenant cleanup"
```

## Task 4: Regression And Remote Verification

**Files:**

- Modify only if a verified defect is found during E2E.

- [ ] **Step 1: Run focused and full checks**

Run `go test ./pkg/storage/postgres ./cmd/server ./api/http -count=1`, the E2E helper Vitest, `make fe-lint`, `make fe-build`, and `make risk-guardrails`.

- [ ] **Step 2: Run canonical platform assistant E2E**

Run `bash scripts/test-platform-assistant-e2e.sh` and `bash scripts/test-platform-assistant-browser-e2e.sh` against isolated dependencies.

- [ ] **Step 3: Verify the restored remote invariant**

After deployment, assert the exact image digest, Ready replicas, zero restarts, one active default tenant, the required tenant schema, and `/readyz=200`. Use a temporary guest, prove its user-only cleanup leaves the tenant and schema intact, then prove missing-model member/admin behavior after a provider is reconfigured.

- [ ] **Step 4: Run acceptance selection**

Run `bash scripts/quality/risk-regression-guard.sh --acceptance <changed files>`. If repository system-attestation targets remain absent, report that gap explicitly rather than substituting temporary evidence.

- [ ] **Step 5: Commit verified follow-up changes**

Only if Step 2 or Step 3 identifies a separate reproducible defect, add a failing regression test first and commit that fix independently.
