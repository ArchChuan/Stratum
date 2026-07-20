# Architecture Guard Report Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Resolve the still-reproducible dependency-direction and tenant-isolation defects reported in `tmp/arch-guard/reports/20260715-160907.md` and `20260715-162125.md`.

**Architecture:** Tenant-scoped PostgreSQL repositories will execute through `pkg/storage/postgres.Pool.ExecTenant`, using unqualified table names inside one validated transaction. Cross-context calls will depend on consumer-owned domain ports, with type conversion confined to `api/wiring`; architecture lint permissions will then be narrowed to enforce the resulting direction.

**Tech Stack:** Go 1.22+, pgx v5, pgxmock, go-arch-lint, project quality scripts.

---

## Task 1: Reject missing and invalid tenant identities

**Files:**

- Modify: `internal/memory/infrastructure/persistence/memory_repo.go`
- Modify: `internal/memory/infrastructure/persistence/fact_repo.go`
- Test: `internal/memory/infrastructure/persistence/memory_repo_test.go`
- Test: `internal/memory/infrastructure/persistence/fact_repo_internal_test.go`

- [ ] Add failing tests asserting empty tenant IDs return errors and do not begin transactions.
- [ ] Run `go test ./internal/memory/infrastructure/persistence -run 'EmptyTenant|NilPool'` and confirm failure from the current silent-success behavior.
- [ ] Replace silent returns with explicit configuration/tenant errors; route valid calls through `postgres.Wrap(pool).ExecTenant`.
- [ ] Re-run the focused tests and confirm PASS.

## Task 2: Move Knowledge repositories onto the validated tenant executor

**Files:**

- Modify: `internal/knowledge/infrastructure/persistence/workspace_repo.go`
- Modify: `internal/knowledge/infrastructure/persistence/doc_repo.go`
- Modify: `internal/knowledge/infrastructure/persistence/chunk_repo.go`
- Test: `internal/knowledge/infrastructure/persistence/tenant_isolation_test.go`

- [ ] Add pgxmock tests proving an invalid tenant is rejected before SQL and a valid tenant starts a transaction, sets `search_path`, and uses unqualified tenant tables.
- [ ] Run the focused test and confirm it fails while repositories call the pool directly and interpolate schemas.
- [ ] Wrap the raw pool with `postgres.Wrap`, execute every operation through `ExecTenant`, and remove `schemaFor` plus schema-formatted SQL.
- [ ] Re-run Knowledge persistence tests and confirm PASS.

## Task 3: Remove hand-written tenant transactions from Memory persistence

**Files:**

- Modify: `internal/memory/infrastructure/persistence/entity_repo.go`
- Modify: `internal/memory/infrastructure/persistence/extraction_queue.go`
- Test: `internal/memory/infrastructure/persistence/tenant_validation_test.go`

- [ ] Add failing tests for empty/unsafe tenant IDs on entity and queue operations.
- [ ] Run the focused tests and confirm the local `SET LOCAL` helpers accept unsafe identifiers.
- [ ] Replace local transaction helpers with `postgres.Wrap(pool).ExecTenant` and preserve error wrapping.
- [ ] Re-run persistence tests and confirm PASS.

## Task 4: Introduce Knowledge consumer-owned ports

**Files:**

- Create: `internal/knowledge/domain/port/vector.go`
- Create: `internal/knowledge/domain/port/text.go`
- Modify: `internal/knowledge/application/ingest_service.go`
- Modify: `internal/knowledge/application/rag_service.go`
- Modify: `internal/knowledge/application/mocks.go`
- Create: `internal/knowledge/infrastructure/vector/adapter.go`
- Modify: `api/wiring/knowledge.go`
- Test: `internal/knowledge/application/ingest_service_test.go`
- Test: `internal/knowledge/application/rag_service_test.go`

- [ ] Add compile-time tests/mocks using only Knowledge port DTOs and explicit `tenantID` arguments.
- [ ] Confirm tests fail because application constructors require `pkg/vector`/`pkg/textchunk` concrete types and RAG reads PostgreSQL tenant context.
- [ ] Define minimal consumer ports and domain search/chunk DTOs; adapt existing implementations in infrastructure/wiring.
- [ ] Remove `pkg/vector`, `pkg/textchunk`, and `pkg/storage/postgres` imports from Knowledge application.
- [ ] Run Knowledge tests and confirm PASS.

## Task 5: Move cross-context LLM/MCP conversion into wiring

**Files:**

- Modify: `internal/agent/domain/port/llm.go`
- Modify: `internal/memory/domain/port/llm.go`
- Modify: `internal/skill/domain/port/llm.go`
- Modify: `internal/agent/infrastructure/capability/llm_adapter.go`
- Modify: `internal/memory/infrastructure/pipeline/*.go`
- Modify: `internal/memory/infrastructure/workers/*.go`
- Modify: `internal/skill/infrastructure/executors/llm.go`
- Move: `internal/mcp/infrastructure/port_adapters.go` to `api/wiring/mcp_agent_adapter.go`
- Modify: `api/wiring/container.go`
- Test: affected package tests under `internal/{agent,memory,skill,mcp}` and `api/wiring`

- [ ] Add/adjust tests so consumer packages compile against their own ports without importing `internal/llmgateway/domain` or sibling contexts.
- [ ] Implement thin wiring ACL adapters that translate local DTOs to LLMGateway/MCP DTOs.
- [ ] Remove provider-domain and consumer-domain imports from sibling infrastructure packages.
- [ ] Run affected package tests and confirm PASS.

## Task 6: Remove infrastructure-to-application contracts and tighten lint

**Files:**

- Create: `internal/memory/domain/port/extraction.go`
- Modify: `internal/memory/infrastructure/workers/extraction_worker.go`
- Modify: `api/wiring/memory.go`
- Modify: `.go-arch-lint.yml`
- Test: `internal/memory/infrastructure/workers/extraction_worker_test.go`

- [ ] Move worker request/service contracts into Memory consumer-owned ports and adapt the application service in wiring.
- [ ] Remove `infra-* -> app-*`, sibling domain, and sibling infrastructure permissions once imports are gone.
- [ ] Run `/usr/bin/bash scripts/quality/arch-guard.sh` and confirm PASS.

## Task 7: Full verification

**Files:**

- Modify only files required by failures discovered during verification.

- [ ] Run `gofmt` on changed Go files.
- [ ] Run focused package tests for every changed context.
- [ ] Run `stratum-verify go-test`.
- [ ] Run `stratum-verify go-full`.
- [ ] Run `/usr/bin/bash scripts/quality/arch-guard.sh` and inspect `git diff --check`.
