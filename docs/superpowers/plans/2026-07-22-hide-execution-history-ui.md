# Hide Execution History UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Hide both frontend execution-history entry points while preserving the API, hooks, and reusable components for later restoration.

**Architecture:** Remove the `/history` menu and route registration. Remove execution-history state, API loading, statistic, and table rendering from Dashboard so the unavailable endpoint is never requested there. Keep standalone execution-history modules untouched except for tests that verify the public entry points are gone.

**Tech Stack:** React 18, TypeScript, React Router 6, Ant Design, Vitest.

---

## Files

- Modify `web/src/app/layout/menu.config.tsx`: remove the history menu item and `/history` open-key mapping.
- Modify `web/src/modules/agent/routes.tsx`: remove the `/history` route and unused page import.
- Modify `web/src/modules/dashboard/hooks/useDashboardPage.ts`: remove execution-history API call, state, response type, limit constant, and execution count population.
- Modify `web/src/modules/dashboard/pages/DashboardPage.tsx`: remove execution-history imports, card, section, and unused hook return value.
- Modify `web/src/app/layout/__tests__/menu.config.test.tsx`: assert the menu and open keys no longer expose history.
- Create or update `web/src/modules/dashboard/pages/__tests__/DashboardPage.test.tsx`: assert the Dashboard does not render execution-history UI and the hook does not call the execution-history API.
- Preserve `web/src/modules/agent/pages/ExecutionHistoryPage.tsx`, `web/src/modules/agent/components/ExecutionHistoryTable.tsx`, `web/src/modules/agent/hooks/useExecutionHistory.ts`, `web/src/modules/agent/api/agent.api.ts`, and their existing tests.

### Task 1: Add failing UI contract tests

**Files:**

- Modify: `web/src/app/layout/__tests__/menu.config.test.tsx`
- Create: `web/src/modules/dashboard/pages/__tests__/DashboardPage.test.tsx`

- [ ] **Step 1: Extend menu tests**

Use the existing test fixture/user factory in `menu.config.test.tsx`. Add assertions that flattened menu item keys do not include `/history`, no menu label contains `执行历史`, and `resolveOpenKeys('/history')` returns `[]` while `resolveOpenKeys('/agents')` still returns `['agent-group']`.

- [ ] **Step 2: Add Dashboard rendering/API contract tests**

Mock `useDashboardPage` or the API modules following existing Dashboard test conventions. The test must render `DashboardPage` with normal non-execution counts and assert:

```tsx
expect(screen.queryByText('近期执行')).not.toBeInTheDocument();
expect(screen.queryByText('最近执行记录')).not.toBeInTheDocument();
expect(screen.queryByText(/执行历史/)).not.toBeInTheDocument();
```

For the hook/API contract, mock `agentApi.executions` as a rejecting spy and assert rendering the Dashboard does not call it; existing skill, agent, MCP, and knowledge calls should remain represented by the fixture.

- [ ] **Step 3: Run focused tests and confirm RED**

Run:

```bash
npm --prefix web test -- \
  src/app/layout/__tests__/menu.config.test.tsx \
  src/modules/dashboard/pages/__tests__/DashboardPage.test.tsx --run
```

Expected: the new assertions fail because the menu, route-related open key, Dashboard statistic, and recent execution section still exist.

- [ ] **Step 4: Commit the failing tests**

```bash
git add web/src/app/layout/__tests__/menu.config.test.tsx \
  web/src/modules/dashboard/pages/__tests__/DashboardPage.test.tsx
git commit -m "test(frontend): define hidden execution history contracts"
```

### Task 2: Remove the two frontend entry points

**Files:**

- Modify: `web/src/app/layout/menu.config.tsx`
- Modify: `web/src/modules/agent/routes.tsx`
- Modify: `web/src/modules/dashboard/hooks/useDashboardPage.ts`
- Modify: `web/src/modules/dashboard/pages/DashboardPage.tsx`

- [ ] **Step 1: Remove menu and route exposure**

Delete the `HistoryOutlined` import and `/history` child item from `menu.config.tsx`. Remove `/history` from `resolveOpenKeys`'s agent path list. Delete the `ExecutionHistoryPage` import and `agents-history` `<Route>` from `agent/routes.tsx`.

- [ ] **Step 2: Stop Dashboard execution-history requests**

In `useDashboardPage.ts`, remove `DashboardExecution`, `RECENT_EXEC_LIMIT`, `ExecutionsResponse`, `recentExecs` state, `agentApi.executions(1, RECENT_EXEC_LIMIT)` from `Promise.allSettled`, execution response parsing, `executions` count assignment, and `setRecentExecs`. Return only `{ counts, loading }`. Preserve cancellation, loading, and the four remaining API calls.

- [ ] **Step 3: Remove Dashboard execution-history rendering**

In `DashboardPage.tsx`, remove `ThunderboltOutlined`, `RecentExecutionsTable`, `recentExecs` destructuring, the “近期执行” stat-card entry, and the “最近执行记录” section. Preserve the remaining four cards and layout.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run:

```bash
npm --prefix web test -- \
  src/app/layout/__tests__/menu.config.test.tsx \
  src/modules/dashboard/pages/__tests__/DashboardPage.test.tsx --run
npm --prefix web run typecheck
npm --prefix web run lint -- --quiet
```

Expected: all focused tests pass, TypeScript has no errors, and lint passes.

- [ ] **Step 5: Commit the implementation**

```bash
git add web/src/app/layout/menu.config.tsx web/src/modules/agent/routes.tsx \
  web/src/modules/dashboard/hooks/useDashboardPage.ts \
  web/src/modules/dashboard/pages/DashboardPage.tsx
git commit -m "fix(frontend): hide unavailable execution history"
```

### Task 3: End-to-end and regression verification

**Files:**

- Verify changed files and preserved execution-history modules; do not delete preserved components/API files.

- [ ] **Step 1: Run frontend regression suite and build**

```bash
npm --prefix web test -- --run
npm --prefix web run lint -- --quiet
npm --prefix web run build
```

- [ ] **Step 2: Start the frontend and verify user-visible navigation**

Run `npm --prefix web run dev -- --host 127.0.0.1`, capture the printed URL, and use Playwright/DOM assertions to verify:

- Dashboard contains no visible `近期执行`, `最近执行记录`, or `执行历史` text.
- Sidebar contains no link with `href="/history"` or label `执行历史`.
- Sidebar still contains `Agent 列表` and Dashboard still contains `概览`.
- Navigating to `/history` does not render `ExecutionHistoryPage` content.

Do not print credentials or API responses. Stop the dev server after verification.

- [ ] **Step 3: Verify no accidental API usage**

Search the changed Dashboard hook/page for `agentApi.executions`, `recentExecs`, `近期执行`, and `最近执行记录`; expect no matches. Confirm standalone execution-history files and `agentApi.executions` remain present.

- [ ] **Step 4: Review and commit any focused correction**

Run `git diff --check` and `git status --short`. If a real defect is found, commit only its focused correction with a `fix(frontend): ...` message; otherwise create no empty commit.
