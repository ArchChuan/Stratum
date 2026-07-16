# Memory User Interaction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the personal-memory clear action accessible, permission-consistent, responsive, and explicit about success and failure.

**Architecture:** Retain `UserMenu` as the only user-facing memory surface because the backend has no list contract. Keep HTTP ownership in `memoryUserApi`; let the confirmation return its mutation promise so Ant Design owns the pending state.

**Tech Stack:** React 18, TypeScript, Ant Design, Vitest, Testing Library.

---

### Task 1: Freeze User Interaction Behavior

**Files:**
- Create: `web/src/app/layout/__tests__/UserMenu.test.tsx`
- Modify: `web/src/app/layout/UserMenu.tsx`

- [ ] Write component tests that render member and admin auth states and locate the user-menu trigger and clear action by accessible role/name.
- [ ] Add a test that confirms the dialog, verifies `clearMyMemories` is called once, and observes the success message.
- [ ] Add a test that rejects with a backend error and verifies the detailed persistent failure message.
- [ ] Run `npm test -- src/app/layout/__tests__/UserMenu.test.tsx` from `web/` and verify the new assertions fail for the missing accessible/loading/error behavior.
- [ ] Add an accessible button trigger, return the clear promise from `onOk`, remove duplicated loading state, and format failures with `extractErrorMessage`.
- [ ] Re-run the focused test and verify it passes.

### Task 2: Verify API and Frontend Gates

**Files:**
- Test: `web/src/modules/memory/api/memory-user.api.test.ts`
- Test: `web/src/app/layout/__tests__/UserMenu.test.tsx`

- [ ] Run `npm test -- src/modules/memory/api/memory-user.api.test.ts src/app/layout/__tests__/UserMenu.test.tsx` from `web/` and verify both contract layers pass.
- [ ] Run `npm run lint`, `npm run typecheck`, `npm test`, and `npm run build` from `web/`.
- [ ] Review `git diff origin/main...HEAD` for scope and commit the design, tests, and implementation as `feat(memory): harden personal memory interaction (st-9qe.5)`.
