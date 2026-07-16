# Tenant Member Pagination Implementation Plan

> **Historical implementation record (implemented).** Server-side pagination and the frontend pagination controls are present in the current IAM/web code. This document records the original implementation plan.
>
> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make tenant member management load every member through server-side pagination, with 10, 20, and 50 row page-size options and end-to-end regression coverage.

**Architecture:** Keep the existing paginated Go endpoint as the source of truth. Preserve its `members`, `total`, `page`, and `page_size` response in the TypeScript API layer, manage those values in `useTenantMembers`, and pass a controlled Ant Design pagination configuration through the page to the table.

**Tech Stack:** Go, Gin, React, TypeScript, Ant Design, Vitest, Testing Library

---

## Task 1: Lock Down The HTTP Pagination Contract

**Files:**

- Modify: `api/http/handler/tenant_handler_test.go`

- [ ] Add a handler test whose fake repository records `limit` and `offset`, then request `/tenant/members?page=2&page_size=10` and assert `limit=10`, `offset=10`, `total`, `page`, and `page_size`.
- [ ] Run `go test ./api/http/handler -run TestListMembers -count=1` and confirm the new contract test passes against the existing backend implementation.

## Task 2: Add Failing Frontend Pagination Tests

**Files:**

- Create: `web/src/modules/iam/api/tenant.api.test.ts`
- Create: `web/src/modules/iam/hooks/useTenantMembers.test.tsx`
- Modify: `web/src/modules/iam/components/TenantMemberTable.tsx`

- [ ] Test that `tenantApi.members(2, 10)` sends `page=2&page_size=10` and returns all pagination metadata.
- [ ] Test that the hook initially requests page 1 with 20 rows and requests page 2 when `fetchPage(2, 20)` is called.
- [ ] Run the focused Vitest files and confirm they fail because the API and hook do not yet expose server-side pagination.

## Task 3: Implement Controlled Server-Side Pagination

**Files:**

- Modify: `web/src/modules/iam/api/tenant.api.ts`
- Modify: `web/src/modules/iam/hooks/useTenantMembers.ts`
- Modify: `web/src/modules/iam/pages/tenant/MembersPage.tsx`
- Modify: `web/src/modules/iam/components/TenantMemberTable.tsx`

- [ ] Add a validated paginated response type to `tenantApi.members(page, pageSize)` and forward query parameters.
- [ ] Store `{ current, pageSize, total }` in `useTenantMembers`, expose `fetchPage`, and refresh the current valid page after member mutations.
- [ ] Pass pagination state and table changes through `MembersPage`.
- [ ] Configure the table with controlled `current`, `pageSize`, and `total`, enable `10 / 20 / 50`, quick jump, and total display.
- [ ] Run the focused Vitest files and confirm they pass.

## Task 4: Verify The Full Change

**Files:**

- Test: `api/http/handler/tenant_handler_test.go`
- Test: `web/src/modules/iam/api/tenant.api.test.ts`
- Test: `web/src/modules/iam/hooks/useTenantMembers.test.tsx`

- [ ] Run `go test ./api/http/handler ./internal/iam/application -count=1`.
- [ ] Run `npm test -- --run`, `npm run typecheck`, and `npm run build` in `web/`.
- [ ] Review `git diff --check` and the final diff for unrelated changes.
