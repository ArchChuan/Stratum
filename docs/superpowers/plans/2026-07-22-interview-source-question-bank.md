# Stratum Source-Level Interview Question Bank Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expand the existing interview report with 80-120 evidence-backed, source-level questions covering Go HTTP fundamentals and Stratum's Agent platform.

**Architecture:** Keep the existing report as the single reader-facing artifact. Build a repository evidence inventory first, then append question batches organized by interview depth; distinguish upstream framework behavior, Stratum behavior, and proposed improvements in every detailed answer.

**Tech Stack:** Markdown, Go 1.25, Gin 1.9, go-playground/validator, PostgreSQL/pgx, Redis, NATS JetStream, Milvus, React/TypeScript, repository tests and official upstream documentation.

---

## Task 1: Establish the evidence inventory

**Files:**

- Read: `tmp/agent-engineer-interview-report.md`
- Read: `api/http/dto/*.go`
- Read: `api/http/handler/*.go`
- Read: `api/middleware/*.go`
- Read: `internal/*/{application,domain,infrastructure}/**/*.go`
- Read: `docs/adr/*.md`
- Read: `docs/agent/*.md`
- Modify: `tmp/agent-engineer-interview-report.md`

- [ ] **Step 1: Read the repository evidence protocol from the configured Obsidian MCP**

Read vault-relative resource `99-系统/知识输入与证据检索协议.md`; record its source-quality and conflict-handling requirements in working notes.

- [ ] **Step 2: Search verified Obsidian material by topic**

Search for Agent loop, context engineering, MCP security, long-term memory, RAG consistency, workflow recovery, idempotency, and evaluation. Treat provisional notes as leads only.

- [ ] **Step 3: Inventory source symbols and tests**

Run:

```bash
rg -n 'ShouldBindJSON|binding:"|context.WithTimeout|execTenant|Idempotency|fencing|Retry|Circuit|Approval' api internal pkg --glob '*.go'
rg -n '^func Test' api internal pkg --glob '*_test.go'
```

Expected: source and test paths for all five question-bank layers.

- [ ] **Step 4: Add the question-bank section skeleton**

Append headings for Go/HTTP, architecture, Agent core, resilience/security, scenario questions, high-frequency review, and source index. Do not duplicate the existing architecture overview.

## Task 2: Write the JSON and HTTP validation exemplar chain

**Files:**

- Read: `api/http/dto/agent.go`
- Read: `api/http/dto/evaluation.go`
- Read: `api/http/handler/agent_crud_handler.go`
- Read: `api/http/handler/chat_handler.go`
- Read: `api/middleware/error_handler.go`
- Read: `api/middleware/body_limit.go`
- Read: `api/http/contract_test.go`
- Modify: `tmp/agent-engineer-interview-report.md`

- [ ] **Step 1: Verify binding behavior in source and focused tests**

Trace `ShouldBindJSON` into Gin binding and go-playground validator behavior. Verify missing, `null`, empty string, numeric zero, nested values, unknown fields, malformed JSON, and body-size behavior against upstream source or official docs.

- [ ] **Step 2: Write the exemplar answer**

Use this answer contract:

```text
30 秒回答 -> Stratum 调用链 -> 底层机制 -> 零值陷阱 -> 当前错误映射 -> 测试证据
```

Explicitly state that value fields cannot always distinguish absence from an explicit zero value and that PATCH-style presence semantics require pointers or an equivalent nullable/presence type.

- [ ] **Step 3: Add continuous follow-ups**

Add questions covering `required`, `omitempty`, `dive`, cross-field validation, unknown JSON fields, duplicate JSON keys, custom validators, DTO versus domain validation, and stable API error contracts.

- [ ] **Step 4: Validate all cited paths and symbols**

Run:

```bash
rg -n 'ShouldBindJSON|binding:"required|BodyLimit|ValidationErrors' api --glob '*.go'
```

Expected: every Stratum-specific assertion in the exemplar resolves to a current source location or is explicitly labeled as an upstream behavior.

## Task 3: Add Go and engineering foundation questions

**Files:**

- Read: `pkg/tenantdb/*.go`
- Read: `pkg/httpclient/*.go`
- Read: `pkg/observability/*.go`
- Read: `api/wiring/*.go`
- Read: representative application services and infrastructure repositories
- Modify: `tmp/agent-engineer-interview-report.md`

- [ ] **Step 1: Add Go runtime and concurrency questions**

Cover context cancellation, timeout placement in loops, goroutine lifecycle, channels, race avoidance, interfaces, error wrapping, defer, slices/maps, JSON and memory allocation. Tie each detailed question to an actual Stratum use or failure boundary.

- [ ] **Step 2: Add HTTP and architecture questions**

Cover middleware order, error translation, dependency direction, DTO boundaries, ports/adapters, composition root, transaction boundaries, and handler responsibilities.

- [ ] **Step 3: Add storage and distributed-systems questions**

Cover tenant `search_path`, `execTenant`, transaction rollback, JSONB encoding, cache invalidation, JetStream delivery, idempotency, PostgreSQL/Milvus partial failure, and schema evolution.

- [ ] **Step 4: Check question count and evidence density**

Run:

```bash
rg -n '^#### Q[0-9]+' tmp/agent-engineer-interview-report.md
```

Expected: at least 35 source-level questions after Tasks 2 and 3, with source paths on all high-frequency items.

## Task 4: Add Agent-platform core questions

**Files:**

- Read: `internal/agent/application/graph/*.go`
- Read: `internal/agent/application/context_budget.go`
- Read: `internal/agent/application/tool_approval_service.go`
- Read: `internal/memory/application/*.go`
- Read: `internal/knowledge/application/*.go`
- Read: `internal/workflow/application/*.go`
- Read: `internal/evaluation/application/*.go`
- Modify: `tmp/agent-engineer-interview-report.md`

- [ ] **Step 1: Add ReAct and context-engineering chains**

Cover state transitions, termination, last-step tool removal, stuck detection, lazy planning, message grouping, token budgets, compaction, tool-result pairing, and production-wiring boundaries.

- [ ] **Step 2: Add Skill and MCP chains**

Cover instruction versus execution capability, revision pinning, tool identity, allowlist intersection, discovery trust, risk classification, approval persistence, and remote exactly-once limits.

- [ ] **Step 3: Add Memory and RAG chains**

Cover extraction identity, confidence semantics, scope isolation, supersession, vector/text retrieval, RRF, degradation, dual-store consistency, chunking, embedding changes, and recovery.

- [ ] **Step 4: Add Workflow and Evaluation chains**

Cover DAG versus Agent loop, leases, generation/fencing, effect classes, unknown outcomes, retry persistence, assertions, trace evidence, judge bias, and regression gates.

- [ ] **Step 5: Verify current implementation boundaries**

For each claim, search its symbol and a relevant test. Move unsupported claims into “改进项” or remove them.

## Task 5: Add resilience, security, and scenario questions

**Files:**

- Read: `docs/agent/architecture.md`
- Read: `docs/agent/observability.md`
- Read: security-, IAM-, tenant-, MCP-, and workflow-related tests
- Modify: `tmp/agent-engineer-interview-report.md`

- [ ] **Step 1: Add dependency-resilience questions**

Cover timeout budgets, bounded retries, error classification, backoff, circuit breakers, bulkheads, backpressure, readiness, deterministic shutdown, and late resource cleanup.

- [ ] **Step 2: Add security and tenancy questions**

Cover fail-closed authorization, tenant propagation, credential transport, encryption, log redaction, prompt injection boundaries, approval consumption, and audit retention.

- [ ] **Step 3: Add source-change scenario questions**

Each scenario must ask the candidate to identify files, state invariants, propose the minimal change, name failure tests, and explain compatibility. Include JSON validation, new tenant repository, retrying MCP calls, changing embedding models, adding a workflow effect, and diagnosing a wrong Agent answer.

- [ ] **Step 4: Reach the target count**

Run:

```bash
rg -c '^#### Q[0-9]+' tmp/agent-engineer-interview-report.md
```

Expected: a count between 80 and 120.

## Task 6: Validate and publish the report update

**Files:**

- Modify: `tmp/agent-engineer-interview-report.md`

- [ ] **Step 1: Check Markdown and placeholders**

Run:

```bash
rg -n 'TBD|TODO|待补|待确认|稍后补充' tmp/agent-engineer-interview-report.md
```

Expected: no unresolved placeholders.

- [ ] **Step 2: Validate cited repository paths**

Extract backticked repository paths and confirm they exist. Manually inspect ambiguous symbol-only references.

- [ ] **Step 3: Check internal consistency**

Confirm each detailed answer separates upstream behavior, current Stratum behavior, and improvement advice. Confirm no production metrics or capabilities are invented.

- [ ] **Step 4: Run documentation checks**

Run:

```bash
git diff --check
make risk-guardrails
```

Expected: no whitespace errors and all applicable guardrails pass; disclose that the target `tmp` report is untracked if Git-based checks cannot include it.

- [ ] **Step 5: Summarize the result**

Report the final question count, major topic counts, evidence sources, verification performed, and the exact updated report path.
