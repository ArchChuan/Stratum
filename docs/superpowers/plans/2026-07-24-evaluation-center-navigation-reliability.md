# Evaluation Center Navigation Reliability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make evaluation deep links deterministic, remove the authentication loading warning, and stop E2E before infrastructure startup when frontend dependencies are incomplete.

**Architecture:** Keep URL parsing and synchronization in `EvaluationCenterPage`, using React Router as the navigation source of truth and the existing hook as the API boundary. Keep the loading-state fix local to `PrivateRoute`, and add a read-only dependency preflight to the existing E2E shell harness.

**Tech Stack:** React 18, React Router 6, Ant Design 5, Vitest, Testing Library, Bash, Playwright

---

## Task 1: Evaluation Center URL State

**Files:**

- Modify: `web/src/modules/evaluation/pages/EvaluationCenterPage.tsx`
- Modify: `web/src/modules/evaluation/pages/EvaluationCenterPage.test.tsx`

- [x] Add tests that render the page at `?kind=skill&resource_id=skill-1`, assert the center hook receives both filters, reject an unknown kind, and assert selector changes update the URL.
- [x] Run `npm --prefix web test -- src/modules/evaluation/pages/EvaluationCenterPage.test.tsx` and confirm the new assertions fail because the page does not read search parameters.
- [x] Use `useSearchParams`, validate `kind` with the existing `resourceKindSchema`, and pass `resource_id` to `useEvaluationCenter`.
- [x] Update `kind` through `setSearchParams` while preserving other parameters, then rerun the focused test.

## Task 2: Warning-Free Private Loading State

**Files:**

- Modify: `web/src/modules/iam/components/PrivateRoute.tsx`
- Create: `web/src/modules/iam/components/PrivateRoute.test.tsx`

- [x] Add a test that renders the loading state and asserts no Ant Design Spin warning reaches `console.error`.
- [x] Run the focused test and confirm it fails with the current standalone `Spin tip` warning.
- [x] Remove the unsupported `tip` prop while preserving the centered loading state and accessible loading text.
- [x] Rerun the focused test and existing IAM route tests.

## Task 3: E2E Dependency Preflight

**Files:**

- Modify: `scripts/e2e/evaluation-evolution.sh`
- Create: `scripts/e2e/evaluation-evolution-test.sh`

- [x] Add a shell test that runs the preflight with a fake npm executable returning a missing-package result and asserts a non-zero exit before Docker is invoked.
- [x] Run `bash scripts/e2e/evaluation-evolution-test.sh` and confirm it fails because no dependency preflight exists.
- [x] Add the minimal `npm ls --prefix "$repo_dir/web" --depth=0 @xyflow/react` preflight and a safe `npm ci` remediation message.
- [x] Rerun the shell test, frontend tests, lint, build, `make risk-guardrails`, and the isolated evaluation E2E.
