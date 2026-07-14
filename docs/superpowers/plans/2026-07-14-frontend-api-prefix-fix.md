# Frontend API Prefix Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Correct the deployed clear-memory request and prevent any frontend request from duplicating the Nginx-owned `/api` prefix.

**Architecture:** Keep the existing proxy contract: browser requests gain `/api` through deployment configuration, while frontend modules provide backend-relative paths. Add one behavioral test for the memory API and one repository guard that scans request call sites for literal `/api/` paths.

**Tech Stack:** React, TypeScript, Axios, Vitest, Vite, ESLint

---

## Task 0: Restore the frontend test baseline after RBAC changes

**Files:**

- Modify: `web/src/modules/mcp/pages/__tests__/MCPServersPage.test.tsx`
- Modify: `web/src/shared/ui/__tests__/responsivePages.test.tsx`

- [ ] **Step 1: Isolate the MCP page test from IAM route exports**

Mock `@/modules/iam` so `useTenantRole` returns `{ isAdmin: true }`, keeping the existing action test explicitly in administrator context.

- [ ] **Step 2: Give responsive card fixtures administrator permissions**

Pass `canManage` to `AgentCard` and `SkillCard` so the test's edit/delete button assertions match its intended administrator scenario.

- [ ] **Step 3: Re-run the two previously failing suites**

Run: `npm test -- src/modules/mcp/pages/__tests__/MCPServersPage.test.tsx src/shared/ui/__tests__/responsivePages.test.tsx`

Expected: both files pass without changing production RBAC behavior.

## Task 1: Reproduce the memory API path bug

**Files:**

- Create: `web/src/modules/memory/api/memory-user.api.test.ts`
- Test: `web/src/modules/memory/api/memory-user.api.test.ts`

- [ ] **Step 1: Write the failing behavioral test**

```ts
import { beforeEach, describe, expect, it, vi } from 'vitest';

import api from '@/services/client';
import { memoryUserApi } from './memory-user.api';

vi.mock('@/services/client', () => ({
  default: { delete: vi.fn() },
}));

describe('memoryUserApi', () => {
  beforeEach(() => {
    vi.mocked(api.delete).mockReset();
    vi.mocked(api.delete).mockResolvedValue({} as never);
  });

  it('clears memories through the backend-relative route', async () => {
    await memoryUserApi.clearMyMemories();

    expect(api.delete).toHaveBeenCalledWith('/memory/clear');
  });
});
```

- [ ] **Step 2: Run the focused test and verify RED**

Run: `npm test -- src/modules/memory/api/memory-user.api.test.ts`

Expected: FAIL because the implementation calls `/api/memory/clear`.

## Task 2: Fix the clear-memory request

**Files:**

- Modify: `web/src/modules/memory/api/memory-user.api.ts`
- Test: `web/src/modules/memory/api/memory-user.api.test.ts`

- [ ] **Step 1: Apply the minimal path correction**

```ts
export const memoryUserApi = {
  clearMyMemories: async (): Promise<void> => {
    await api.delete('/memory/clear');
  },
};
```

- [ ] **Step 2: Run the focused test and verify GREEN**

Run: `npm test -- src/modules/memory/api/memory-user.api.test.ts`

Expected: PASS.

## Task 3: Guard all frontend request paths

**Files:**

- Create: `web/src/services/api-paths.test.ts`
- Test: `web/src/services/api-paths.test.ts`

- [ ] **Step 1: Add a repository-level request-path guard**

Create a Vitest test that recursively reads `.ts` and `.tsx` files below `web/src`, excludes test files, identifies literal paths passed to `api.get/post/put/patch/delete` or `fetch`, and reports any literal beginning with `/api/`. The failure output must list the relative file and offending path.

- [ ] **Step 2: Run the guard**

Run: `npm test -- src/services/api-paths.test.ts`

Expected: PASS after Task 2 and confirm no other duplicated literal prefix exists.

## Task 4: Verify the frontend change

**Files:**

- Verify: `web/src/modules/memory/api/memory-user.api.ts`
- Verify: `web/src/modules/memory/api/memory-user.api.test.ts`
- Verify: `web/src/services/api-paths.test.ts`

- [ ] **Step 1: Run all frontend unit tests**

Run: `npm test`

Expected: all tests pass.

- [ ] **Step 2: Run TypeScript checks**

Run: `npm run typecheck`

Expected: exit code 0.

- [ ] **Step 3: Run ESLint**

Run: `npm run lint`

Expected: exit code 0 with zero warnings.

- [ ] **Step 4: Build the production frontend**

Run: `npm run build`

Expected: Vite production build exits with code 0.

- [ ] **Step 5: Review the final diff**

Run: `git diff --check && git status --short && git diff --stat`

Expected: no whitespace errors and only the scoped API fix, tests, design, and plan are changed.
