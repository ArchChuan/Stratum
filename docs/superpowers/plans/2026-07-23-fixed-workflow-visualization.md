# Fixed Workflow Visualization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver the fixed-workflow product as three bounded, independently verifiable increments.

**Architecture:** Keep the existing deterministic Workflow engine as the execution source of truth. First add tenant-safe product queries, immutable input contracts, run ownership, and object authorization; then build the admin authoring surface; finally build member execution and resumable monitoring on those stable contracts.

**Tech Stack:** Go 1.25, Gin, pgx/PostgreSQL tenant schemas, React 18, TypeScript, Ant Design, Zod, `@xyflow/react@12.11.2`, Vitest, Playwright

---

## Delivery Order

1. [Backend Product Contracts](2026-07-23-workflow-backend-product-contracts.md)
2. [Admin Workflow Designer](2026-07-23-workflow-admin-designer.md)
3. [Member Execution And Monitoring](2026-07-23-workflow-execution-monitoring.md)

Do not start plans 2 and 3 against temporary response shapes. Plan 1 freezes the HTTP and domain contracts consumed by both frontends. Plans 2 and 3 may run in parallel only after plan 1 has merged and its contract tests are green.

## Release Gates

- [ ] Plan 1 is merged and tenant schema history tests pass.
- [ ] Plan 2 is merged and an admin can publish a five-node-type workflow without editing JSON.
- [ ] Plan 3 is merged and a member can start, reconnect to, inspect, and cancel only their own run.
- [ ] Run `make risk-guardrails` from the feature worktree and expect `risk regression guard: passed`.
- [ ] Run `go test -v -race -timeout 30s ./...` and expect all Go packages to pass.
- [ ] Run `make fe-lint && make fe-build` and expect both commands to pass.
- [ ] Run the Stratum E2E journey covering admin publish, member start, admin approval, completion, reconnect, failure, and cross-user denial.

## Explicitly Deferred

- Dynamic DAG mutation
- File inputs
- Auto-save and collaborative editing
- Archive, delete, copy, and rollback
- Schedule, webhook, and event triggers
- Arbitrary failed-node retry
- Mobile graph editing
