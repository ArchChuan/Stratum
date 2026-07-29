# Code Quality Ratchet Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Add an incremental Go quality ratchet and reduce a bounded set of Workflow validation complexity.

**Architecture:** A Go AST analyzer calculates deterministic per-function metrics and compares changed functions with a checked-in baseline. A Git-aware shell wrapper is shared by pre-commit, risk guard, Make, and CI. The Workflow refactor is isolated from guard implementation files.

**Tech Stack:** Go AST/token packages, Bash, Git, golangci-lint v2, pre-commit, GitHub Actions.

---

## Task 1: Metric engine and mutation fixtures

**Files:**

- Create: scripts/quality/code-quality-ratchet.go
- Create: scripts/quality/code-quality-ratchet-test.sh
- Create: scripts/quality/testdata/code-quality fixtures

- [ ] Write failing fixture cases for clean code and cyclomatic, cognitive, length, nesting, parse, and deterministic-output failures.
- [ ] Run bash scripts/quality/code-quality-ratchet-test.sh and verify RED because the analyzer is absent.
- [ ] Implement FunctionMetrics with ID, file, name, cyclomatic, cognitive, lines, max nesting, params, and body hash.
- [ ] Walk control-flow AST nodes deterministically and sort output by ID.
- [ ] Run the fixture suite and verify GREEN.
- [ ] Commit as [feat](quality): add code metric analyzer.

## Task 2: Baseline and ratchet semantics

**Files:**

- Modify: scripts/quality/code-quality-ratchet.go
- Modify: scripts/quality/code-quality-ratchet-test.sh
- Create: scripts/quality/code-quality-baseline.json

- [ ] Add failing cases for new violations, unchanged debt, improved debt, worsened debt, renamed debt, deletion, malformed baseline, and refresh.
- [ ] Verify RED.
- [ ] Implement targets 10/15/120/4 and warnings 6/800.
- [ ] Implement explicit refresh-baseline mode and refuse implicit writes.
- [ ] Generate the current repository baseline from Git-tracked production Go files.
- [ ] Refresh to a temporary file and compare byte-for-byte.
- [ ] Commit as [feat](quality): enforce incremental metric baseline.

## Task 3: Git wrapper and warning metrics

**Files:**

- Create: scripts/quality/code-quality-ratchet.sh
- Modify: scripts/quality/code-quality-ratchet-test.sh

- [ ] Add failing cases for explicit paths, staged paths, rename/delete, no Go changes, untracked worktrees, missing optional tools, and warning-only duplicate output.
- [ ] Verify RED.
- [ ] Implement tracked changed-file resolution and invoke the analyzer.
- [ ] Run changed-file dupl as warning-only when golangci-lint is available.
- [ ] Print LOC ratio and TODO/FIXME trend without changing status.
- [ ] Verify GREEN and commit as [feat](quality): add git-aware ratchet wrapper.

## Task 4: Hook and CI integration

**Files:**

- Modify: .pre-commit-config.yaml
- Modify: scripts/quality/risk-regression-guard.sh
- Modify: scripts/quality/risk-regression-guard-test.sh
- Modify: scripts/quality/testdata/fake-risk-guard-executor.sh
- Modify: Makefile
- Modify: .github/workflows/ci.yml
- Modify: docs/agent/instructions.md
- Regenerate: AGENTS.md and CLAUDE.md

- [ ] Add failing assertions for one pre-commit hook, the code-quality risk label, one Make target, one CI invocation, and generated instructions.
- [ ] Run risk guard and instruction tests and verify RED.
- [ ] Wire all entry points to the same wrapper; pass changed filenames from pre-commit.
- [ ] Document baseline refresh review rules in canonical instructions and regenerate.
- [ ] Verify GREEN and commit as [ci](quality): wire incremental code quality guard.

## Task 5: Workflow validation refactor

**Files:**

- Modify: internal/workflow/domain/workflow.go
- Modify: internal/workflow/domain/workflow_test.go

- [ ] Add table-driven characterization tests for every node type and input type.
- [ ] Run domain tests and preserve behavioral GREEN.
- [ ] Run focused cyclop/gocyclo and capture structural RED for validateNode and validInputValue.
- [ ] Extract pure node-type and input-type validators without changing exported behavior.
- [ ] Run tests and focused complexity check; both target functions must be at or below 10.
- [ ] Commit as [refactor](workflow): simplify validation decisions.

## Task 6: Integrated verification

- [ ] Run the ratchet fixture suite.
- [ ] Run risk guard and instruction generator tests.
- [ ] Run Workflow domain tests.
- [ ] Run make lint and make risk-guardrails.
- [ ] Run stratum-verify go-test.
- [ ] Run make e2e-system-short.
- [ ] Run git diff --check, baseline determinism comparison, and inspect git status.
