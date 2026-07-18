# Scope-Safe Milvus Fact Search Implementation Plan

> **For agentic workers:** Execute inline with strict RED, GREEN, and refactor checkpoints. Do not commit, push, create a PR, or deploy.

**Goal:** Enable scope-safe fact search through the existing Milvus vector store.

**Architecture:** A typed memory-domain filter owns validation, the persistence adapter owns Milvus expression generation and result mapping, and the Milvus package owns typed availability classification. The application only degrades on the typed availability contract.

**Tech Stack:** Go 1.25, Milvus SDK Go v2.4.2, Milvus 2.4.15, testify, GitHub Actions

---

## Task 1: Type-Safe Port and Adapter

**Files:**

- Modify: `internal/memory/domain/port/vector_store.go`
- Modify: `internal/memory/infrastructure/persistence/milvus_adapter.go`
- Test: `internal/memory/infrastructure/persistence/milvus_adapter_test.go`
- Modify: memory vector-store mocks under `internal/memory` and `test/e2e`

- [ ] Add failing tests for valid expressions, escaping, invalid filters, mapping, and errors.
- [ ] Run the focused tests and retain the expected compile/assertion failures.
- [ ] Add `VectorSearchFilter`, recognizable validation errors, `Distance`, and adapter delegation.
- [ ] Update all memory port implementations and run focused tests green.

## Task 2: Availability and Recall Fallback

**Files:**

- Modify: `pkg/storage/milvus/client.go`
- Test: `pkg/storage/milvus/vector_store_test.go`
- Modify: `internal/memory/application/retrieval.go`
- Test: `internal/memory/application/retrieval_test.go`

- [ ] Add failing classification, fallback, cross-agent filter, and input-validation tests.
- [ ] Run the focused tests and retain the expected failures.
- [ ] Add typed availability wrapping at connection, load, and search boundaries.
- [ ] Validate recall before dependencies and degrade only on the typed error.
- [ ] Run focused tests green and refactor without behavior changes.

## Task 3: Real Milvus Gate

**Files:**

- Create: `test/integration/milvus_fact_search_test.go`
- Create: `docker-compose.milvus-test.yml`
- Modify: `.github/workflows/memory-e2e.yml`

- [ ] Add an integration-tagged test that provisions four ownership fixtures.
- [ ] Confirm RED against the unimplemented adapter or unavailable required service.
- [ ] Add repeatable Milvus 2.4.15 startup and CI health checks.
- [ ] Verify allowed results, ordering, missing collection, and fail-closed dependency handling.

## Task 4: Full Verification

- [ ] Run gofmt and markdownlint-compatible heading checks.
- [ ] Run all requested unit, race, vet, repository test, and build commands.
- [ ] Start the real Milvus stack locally and run the required integration test.
- [ ] Run `git diff --check` and report exact status and any unverified risk.
