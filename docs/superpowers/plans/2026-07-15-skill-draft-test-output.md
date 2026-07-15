# Skill Draft Test Output Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Skill workspace draft test show execution status, output, trace, duration, empty-output guidance, and request failures.

**Architecture:** Keep the complete `SkillTestResult` in `SkillWorkspacePage` instead of extracting only `result`. Render a focused result panel in the existing test tab, with a separate page-level error state for failed requests.

**Tech Stack:** React 18, TypeScript, Ant Design, Vitest, Testing Library

---

## Task 1: Cover successful draft execution output

**Files:**

- Modify: `web/src/modules/skill/pages/SkillWorkspacePage.test.tsx`
- Modify: `web/src/modules/skill/pages/SkillWorkspacePage.tsx`

- [ ] **Step 1: Write the failing success-state test**

Mock `skillApi.testDraft` with `{ result: { category: '物流' }, traceID: 'trace-1', durationMs: 42 }`, open the test tab, run the draft, and assert that “执行结果”, the formatted output, trace, and duration are visible.

- [ ] **Step 2: Run the test to verify it fails**

Run: `npm test -- --run src/modules/skill/pages/SkillWorkspacePage.test.tsx`

Expected: FAIL because the current page discards trace and duration and has no result heading.

- [ ] **Step 3: Store and render the complete response**

Change the result state to `SkillTestResult | null`, save the complete API response, and render a success `Alert`, metadata text, and a preformatted output block. Strings render directly; structured values use `JSON.stringify(value, null, 2)`.

- [ ] **Step 4: Run the test to verify it passes**

Run: `npm test -- --run src/modules/skill/pages/SkillWorkspacePage.test.tsx`

Expected: PASS.

## Task 2: Cover empty output and request failure

**Files:**

- Modify: `web/src/modules/skill/pages/SkillWorkspacePage.test.tsx`
- Modify: `web/src/modules/skill/pages/SkillWorkspacePage.tsx`

- [ ] **Step 1: Write failing edge-state tests**

Add one test whose response has an empty `result` and expects “执行成功，但 Skill 未返回内容”; add another whose request rejects and expects a persistent failure alert containing the extracted error message.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npm test -- --run src/modules/skill/pages/SkillWorkspacePage.test.tsx`

Expected: FAIL because neither persistent empty-output guidance nor an inline failure result exists.

- [ ] **Step 3: Implement minimal edge-state rendering**

Add a `testError` state. Clear it before each run, set it from `extractErrorMessage` on failure, and render an error `Alert`. Treat `null`, `undefined`, and blank strings as empty output while preserving `0`, `false`, objects, and arrays as valid output.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npm test -- --run src/modules/skill/pages/SkillWorkspacePage.test.tsx`

Expected: all tests in the file PASS.

## Task 3: Verify the user workflow

**Files:**

- Test: `web/src/modules/skill/pages/SkillWorkspacePage.test.tsx`

- [ ] **Step 1: Run frontend regression checks**

Run: `npm test -- --run src/modules/skill/pages/SkillWorkspacePage.test.tsx && npm run typecheck && npm run lint && npm run build`

Expected: commands exit successfully; the existing chunk-size warning may remain.

- [ ] **Step 2: Run real browser E2E**

Start the required backend/frontend dependencies, open a real Skill workspace test tab, execute a draft Skill, and assert the output heading, returned content, trace, and duration in the DOM. Do not print credentials or raw tokens.

- [ ] **Step 3: Clean up and commit**

Remove temporary E2E files, stop self-started services, confirm `git diff --check`, and commit the page and test changes with `fix(skill): show draft test execution output`.
