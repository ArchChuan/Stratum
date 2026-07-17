# History Dynamic Long-Term Memory Implementation Plan

> **For agentic workers:** Implement inline in this isolated worktree. Do not commit; preserve the existing Phase 0 Facts and Phase 1 User snapshot changes.

**Goal:** Add scoped, tiered, idempotent History aggregation, lifecycle management, safe fact archival, and relevant budgeted injection by evolving `memory_summaries`.

**Architecture:** Extend the tenant-local summary table with backward-compatible segment metadata. A focused repository owns tenant-isolated claims/upserts, promotion/compression queries, relevance reads, and protected fact archival; a per-tenant worker owns scheduling, LLM calls outside transactions, retries, and degradation. The existing injector renders snapshot, Facts, then relevant History under its one shared character budget.

**Tech Stack:** Go, pgx/PostgreSQL tenant schemas, existing memory workers and tenant LLM resolver, testify/pgxmock-style project tests.

---

## Task 1: Domain and centralized policy

**Files:**

- Create: `internal/memory/domain/history.go`
- Create: `internal/memory/domain/history_test.go`
- Modify: `pkg/constants/memory.go`

- [ ] Write failing tests for tier/status validation, deterministic aggregation keys, source bounds, and promotion selection.
- [ ] Run the focused domain tests and confirm RED because History types and policy do not exist.
- [ ] Add the minimal History segment model and centralized thresholds, ages, capacities, Top-N, budgets, timeouts, and protection thresholds.
- [ ] Run the focused tests and confirm GREEN; refactor without changing behavior.

## Task 2: Tenant schema and marker migration

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql`
- Modify: `pkg/storage/postgres/tenant_schema_test.go`
- Create: `pkg/migration/sql/022_memory_history_tiers.up.sql`
- Create: `pkg/migration/sql/022_memory_history_tiers.down.sql`

- [ ] Write failing schema tests for all History columns, safe defaults/backfills, constraints, scoped indexes, and the unique idempotency key.
- [ ] Run the schema and migration tests and confirm RED.
- [ ] Add idempotent tenant DDL only to `tenant_schema.sql`; add paired comment-only marker migrations.
- [ ] Run schema/migration tests and migration guardrails and confirm GREEN.

## Task 3: History persistence

**Files:**

- Create: `internal/memory/domain/port/history_repo.go`
- Create: `internal/memory/infrastructure/persistence/history_repo.go`
- Create: `internal/memory/infrastructure/persistence/history_repo_test.go`

- [ ] Write failing tests for threshold selection, scoped deterministic upsert, duplicate idempotency, tier promotion, bounded compression replacement, protection-aware archival, relevance ordering/limit, and tenant `search_path` isolation.
- [ ] Run focused persistence tests and confirm RED.
- [ ] Implement repository operations through the existing tenant transaction wrapper, with parameterized queries and no raw message payload copied into History source metadata.
- [ ] Run focused persistence tests and confirm GREEN; refactor shared scans/query fragments only when they remove real duplication.

## Task 4: Background aggregation and lifecycle worker

**Files:**

- Create: `internal/memory/infrastructure/workers/history_worker.go`
- Create: `internal/memory/infrastructure/workers/history_worker_test.go`
- Modify: `api/wiring/memory.go`

- [ ] Write failing tests proving below-threshold batches do nothing, duplicate runs remain idempotent, aggregation/compression LLM failures leave source rows active for retry, successful runs promote/compress, archival failures degrade independently, and cancellation/Stop work.
- [ ] Run focused worker tests and confirm RED.
- [ ] Implement a per-tenant scheduled worker using the repository and existing tenant LLM resolver, with bounded calls/timeouts, panic recovery, logging, and retry-on-next-pass semantics.
- [ ] Wire it into the existing `TenantWatcher` worker set; run focused tests and confirm GREEN.

## Task 5: Shared-budget relevant injection

**Files:**

- Modify: `internal/memory/infrastructure/pipeline/injector.go`
- Modify: `internal/memory/infrastructure/pipeline/injector_test.go`

- [ ] Write failing tests for user/agent scope, relevance Top-N ordering, History read timeout/failure degradation, History placement after snapshot/Facts, and strict adherence to the existing total budget.
- [ ] Run focused injector tests and confirm RED.
- [ ] Add a bounded History query/read and render History only from remaining shared budget.
- [ ] Run injector tests and confirm GREEN.

## Task 6: Documentation and full verification

**Files:**

- Modify: `docs/agent/tiered-memory.md`

- [ ] Document actual tiers, aggregation key/window, lifecycle and compression rules, fact protections, injection order/budget, failure behavior, tenant DDL/marker, rollback, and deferred risks.
- [ ] Run `gofmt` on changed Go files.
- [ ] Run `make migration-guardrails`.
- [ ] Run `go test ./internal/memory/... -count=1 -timeout=10m`.
- [ ] Run `go test ./internal/agent/... -count=1 -timeout=10m`.
- [ ] Run `go build ./...` and `go vet ./...`.
- [ ] Run `git diff --check`, inspect `git status --short` and `git diff --stat`, and report exact results without committing.
