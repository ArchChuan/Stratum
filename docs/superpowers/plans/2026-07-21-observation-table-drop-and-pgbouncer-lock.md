# Observation Table Drop And PgBouncer Lock Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Permanently remove three obsolete tenant observation tables and make schema provisioning locks safe under PgBouncer transaction pooling.

**Architecture:** Tenant-only deletion stays in canonical tenant DDL and is applied by existing tenant provisioning. Schema bootstrap serialization uses a PostgreSQL transaction-level advisory lock held by an explicit transaction, so PgBouncer pins the lock to one backend and PostgreSQL releases it automatically.

**Tech Stack:** Go 1.25, pgx v5, PostgreSQL, PgBouncer, embedded SQL.

---

## Task 1: Drop Historical Observation Tables

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql`
- Modify: `pkg/storage/postgres/tenant_schema_test.go`
- Modify: `pkg/storage/postgres/tenant_schema_integration_test.go`
- Modify: `docs/agent/observability.md`
- Modify: `docs/superpowers/plans/2026-07-20-opik-trace-evidence-migration.md`

- [x] Replace the retention unit test with assertions requiring three `DROP TABLE IF EXISTS` statements and forbidding recreation.
- [x] Run the focused unit test and confirm it fails because the canonical DDL still retains the tables.
- [x] Update the integration test to seed historical rows and require zero matching tables after provisioning.
- [x] Add dependency-ordered drops and remove corresponding create/alter/index DDL.
- [x] Run schema unit and integration tests twice to prove deletion and idempotency.
- [x] Update observability and migration status documentation.
- [x] Commit only Task 1 files with `fix(evaluation): drop obsolete observation tables`.

## Task 2: Replace Session Advisory Lock

**Files:**

- Modify: `pkg/storage/postgres/tenant.go`
- Modify: `pkg/storage/postgres/tenant_lock_test.go`
- Modify: `pkg/storage/postgres/tenant_lock_integration_test.go`
- Modify: `docs/superpowers/specs/2026-07-16-pipeline-concurrency-safety-design.md`

- [ ] Write unit tests requiring begin, `pg_advisory_xact_lock`, commit on success, and rollback on every failure path.
- [ ] Run focused lock tests and confirm failure against the session-lock implementation.
- [ ] Replace explicit session unlock with an explicit transaction and transaction-level lock.
- [ ] Run focused unit and real PostgreSQL serialization tests.
- [ ] Verify PostgreSQL has no retained session advisory lock after the callback completes.
- [ ] Run architecture, migration, risk, and repository Go verification.
- [ ] Commit only Task 2 files with `fix(storage): make schema lock safe for pgbouncer`.
