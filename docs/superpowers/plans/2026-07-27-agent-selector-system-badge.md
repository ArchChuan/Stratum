# Agent Selector System Badge Removal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the `系统内置` badge from Agent chat selector options while retaining it in the selected Agent header.

**Architecture:** Simplify `ChatConversationSidebar` option labels to plain Agent names without changing Agent data or selection state. Lock the boundary with a component regression test and desktop/mobile Playwright assertions.

**Tech Stack:** React 18, TypeScript, Ant Design Select, Vitest, Testing Library, Playwright

---

## Task 1: Selector Component Regression

**Files:**

- Modify: `web/src/modules/agent/components/__tests__/PlatformAssistant.test.tsx`
- Modify: `web/src/modules/agent/components/ChatConversationSidebar.tsx`

- [ ] **Step 1: Write the failing selector test**

Change the first platform assistant test so the open selector option contains `平台使用小助手` and does not contain `系统内置`, while the separately rendered `ChatHeader` still contains `系统内置` and the settings button.

```tsx
const option = document.querySelector('.ant-select-item-option-content');
expect(option).toHaveTextContent('平台使用小助手');
expect(option).not.toHaveTextContent('系统内置');
```

- [ ] **Step 2: Verify the test fails against the old component**

Run: `npm --prefix web test -- --run src/modules/agent/components/__tests__/PlatformAssistant.test.tsx`

Expected: FAIL because the selector option still contains `系统内置`.

- [ ] **Step 3: Implement the minimal selector label change**

Remove the selector-only `agentLabel` renderer and unused imports, then supply plain labels:

```tsx
options={agents.map((agent) => ({ value: agent.id, label: agent.name }))}
```

- [ ] **Step 4: Verify the component suite passes**

Run: `npm --prefix web test -- --run src/modules/agent/components/__tests__/PlatformAssistant.test.tsx`

Expected: all tests in the file pass.

## Task 2: Browser Acceptance Boundary

**Files:**

- Modify: `web/e2e/system-assistant.spec.ts`

- [ ] **Step 1: Add selector overlay assertions**

For desktop and mobile projects, open the Agent selector in the visible sidebar or drawer and assert its listbox contains the system Agent name but not the badge:

```ts
await selector.click();
const listbox = page.getByRole('listbox');
await expect(listbox.getByText('平台使用小助手')).toBeVisible();
await expect(listbox.getByText('系统内置')).toHaveCount(0);
await page.keyboard.press('Escape');
```

Keep the existing post-selection header assertion for `系统内置`.

- [ ] **Step 2: Run focused headless browser tests**

Run: `npm --prefix web exec playwright test -- --config=playwright.config.ts e2e/system-assistant.spec.ts --project=desktop-1440 --project=mobile-390`

Expected: both viewport projects pass without opening a headed browser.

## Task 3: Full Frontend and Repository Verification

**Files:**

- Verify only

- [ ] **Step 1: Run frontend lint**

Run: `make fe-lint`

Expected: exit code 0.

- [ ] **Step 2: Run frontend build**

Run: `make fe-build`

Expected: exit code 0.

- [ ] **Step 3: Run risk guardrails**

Run: `make risk-guardrails`

Expected: exit code 0.

- [ ] **Step 4: Review the final diff and commit**

Run: `git diff --check && git status --short`

Expected: only the approved spec, plan, component, component test, and browser test are changed, with no whitespace errors.
