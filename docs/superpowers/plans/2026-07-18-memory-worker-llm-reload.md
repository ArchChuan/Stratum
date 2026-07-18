# Memory Worker LLM Reload Implementation Plan

> **For agentic workers:** Execute inline with strict RED-GREEN-refactor checkpoints. Do not commit, push, or create a PR for this worktree.

**Goal:** Make long-lived memory workers resolve the current tenant LLM capability for every operation while preventing invalidated cache loads from restoring stale gateways.

**Architecture:** The workers package owns a minimal tenant-aware LLM resolver contract and resolving adapters, while `api/wiring` supplies a closure backed by the existing tenant resolver. `TenantGatewayCache` exposes generation snapshot and conditional set operations so settings invalidation wins against concurrent cache fills. Existing fixed-client constructors remain compatible.

**Tech Stack:** Go 1.25, pgx, httptest, GitHub Actions.

---

## Task 1: Dynamic worker adapters

**Files:**

- Modify: `internal/memory/infrastructure/workers/llm_superseder.go`
- Modify: `internal/memory/infrastructure/workers/history_summarizer.go`
- Test: `internal/memory/infrastructure/workers/llm_superseder_test.go`
- Test: `internal/memory/infrastructure/workers/history_summarizer_test.go`

- [ ] Add tests proving one adapter instance resolves A then B/provider B on successive calls.
- [ ] Run focused tests and record the expected missing-constructor RED.
- [ ] Add the minimal resolver/client contract and resolving constructors while retaining fixed constructors.
- [ ] Run focused tests GREEN and refactor shared resolve/error behavior without changing semantics.
- [ ] Cover unavailable resolver, recovery, context cancellation, and summarize/compress re-resolution.

## Task 2: Cache generation CAS

**Files:**

- Modify: `internal/llmgateway/infrastructure/tenant_cache.go`
- Modify: `internal/llmgateway/infrastructure/tenant_cache_test.go`
- Modify: `api/wiring/tenant_resolver.go`

- [ ] Add blocking stale-loader and concurrent clone/race tests for generation snapshot and conditional set.
- [ ] Run focused tests and record the missing-API RED.
- [ ] Add clearly named generation snapshot and conditional set APIs; make `Invalidate` increment generation and delete the entry.
- [ ] Make tenant resolver capture generation before loading and conditionally publish the gateway.
- [ ] Run cache/resolver tests with `-race` GREEN.

## Task 3: Production wiring

**Files:**

- Modify: `api/wiring/memory.go`
- Modify: `api/wiring/memory_test.go`

- [ ] Add a wiring seam test proving tenant worker construction does not eagerly resolve LLM and always contains dynamic supersede/history processors when resolver infrastructure exists.
- [ ] Run the wiring test RED.
- [ ] Replace build-time `ResolveLLM` with a worker resolver closure and resolving constructors.
- [ ] Run wiring and worker tests GREEN.

## Task 4: Vertical PostgreSQL/HTTP integration and CI

**Files:**

- Create: `test/e2e/memory_worker_llm_reload_test.go`
- Modify: `.github/workflows/memory-e2e.yml` only if current invocation does not already include the test.

- [ ] Add an integration test using PostgreSQL tenant settings, the formal service invalidation path, real resolver/cache, and an httptest OpenAI-compatible endpoint with fake credential fingerprints.
- [ ] Cover same-process A-to-B rotation without rebuilding the resolving processor and concurrent stale-load rejection.
- [ ] Require dependencies when `REQUIRE_MEMORY_E2E=1`; allow local skip otherwise; use synchronization channels rather than sleeps.
- [ ] Run the test RED before any integration-only production seam, then GREEN.

## Task 5: Verification and review

**Files:**

- Review all changed files and retain this plan as the implementation record.

- [ ] Run focused tests and affected race tests.
- [ ] Run `go test ./...`, the required broad race command, `go vet ./...`, `go build ./...`, and `git diff --check`.
- [ ] Validate CI YAML/actionlint if workflow YAML changes.
- [ ] Review the diff for credential leakage, scope expansion, and compatibility.
- [ ] Confirm the worktree remains intentionally uncommitted and report residual runtime-stop risk without fixing it.
