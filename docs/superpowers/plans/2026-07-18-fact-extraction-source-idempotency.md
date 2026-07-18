# Fact Extraction Source-Identity Idempotency Implementation Plan

> **For agentic workers:** Execute task-by-task with strict RED/GREEN TDD. Do not commit in this worktree.

**Goal:** Make extraction retries reuse one canonical fact and one entity mutation while retaining retryable vector writes.

**Architecture:** Application code assigns pre-filter ordinals and builds versioned canonical payload hashes. A narrow persistence port atomically inserts or resolves a fact and mutates entities in one tenant PostgreSQL transaction; vectors remain outside PostgreSQL.

**Tech Stack:** Go 1.25, pgx v5, PostgreSQL tenant schemas, Milvus port, testify/pgx integration tests.

---

## Task 1: Canonical Payload And Validation

- [x] Add tests for entity order/dedup, versioned SHA-256 hash, scope ownership, source validation, and pre-filter ordinal stability.
- [x] Run the focused tests and record expected RED failures.
- [x] Add the minimal domain/application source identity and canonicalization implementation.
- [x] Run focused tests GREEN.

## Task 2: Atomic PostgreSQL Persistence

- [x] Add real PostgreSQL integration tests for sequential replay, payload conflict, concurrent replay, scope isolation, and legacy facts.
- [x] Run integration tests and record expected RED failures or dependency skips.
- [x] Add nullable schema columns, ownership partial unique indexes, typed conflict mapping, and the atomic writer.
- [x] Run persistence tests GREEN.

## Task 3: Extraction And Worker Retry Flow

- [x] Add application/worker tests for MessageID propagation, stable vector ID after vector failure, no vector mutation on conflict, and MarkCompleted retry behavior.
- [x] Run focused tests RED.
- [x] Wire source provenance through the worker and extraction service, retaining legacy behavior without provenance.
- [x] Run application/worker tests GREEN.

## Task 4: Migration And Guardrails

- [x] Add migration/schema tests RED for nullable columns and both partial unique indexes.
- [x] Add the forward-compatible tenant schema and migration marker.
- [x] Run migration guardrails GREEN.

## Task 5: Verification

- [x] Run gofmt and focused memory tests.
- [x] Run PostgreSQL integration/E2E tests when dependencies are available.
- [x] Run `go test -race ./internal/memory/...`.
- [x] Run `go test ./...`, `go vet ./...`, and `go build ./...`.
- [x] Run `git diff --check` and inspect the final diff for scope.
