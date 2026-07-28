# Tiered Stateful Acceptance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make PR acceptance use a source-bound 600-second test soak while retaining an explicit 3600-second release soak contract.

**Architecture:** Add an acceptance profile to safe results and attestations, with duration policy centralized in the Go validator. Pass the profile through the browser runner, CLI, Make, CI, and generated agent instructions without changing packs, capabilities, or evidence reconciliation.

**Tech Stack:** Go 1.25, TypeScript, Playwright, Bash, Make, GitHub Actions

---

## Task 1: Profile Contract And Validator

**Files:**

- Modify: `internal/platform/e2eattestation/attestation.go`
- Modify: `internal/platform/e2eattestation/attestation_test.go`

- [ ] Add table-driven failing tests proving `test` accepts 600 and rejects 599, `release` accepts 3600 and rejects 3599, profile mismatch fails, missing/unknown soak profiles fail, and short mode rejects profile metadata.
- [ ] Run `go test ./internal/platform/e2eattestation -run 'Profile|Duration' -count=1` and confirm the new cases fail.
- [ ] Add `AcceptanceProfile string` to safe results, define `test` and `release` profile constants, centralize their minimum durations in one function, validate the profile during generation, and require `VerifyOptions.RequiredProfile` during verification.
- [ ] Keep all existing source digest, manifest, status, pack, capability, evidence, cleanup, expiry, artifact hash, and credential checks unchanged.
- [ ] Run `go test ./internal/platform/e2eattestation -count=1` and confirm all tests pass.

## Task 2: CLI And Browser Result Propagation

**Files:**

- Modify: `cmd/e2e-attestation/main.go`
- Modify: `cmd/e2e-attestation/main_test.go`
- Modify: `web/e2e/stateful/core/runtime.ts`
- Modify: `web/e2e/stateful/core/runtime.test.ts`
- Modify: `web/e2e/system-stateful.spec.ts`

- [ ] Add failing CLI tests for missing/unknown soak `--required-profile` and runtime tests for `STATEFUL_E2E_PROFILE=test|release` defaults and minimum durations; short mode must reject a profile.
- [ ] Run `go test ./cmd/e2e-attestation -count=1` and `npm --prefix web test -- e2e/stateful/core/runtime.test.ts` to confirm failure.
- [ ] Parse `STATEFUL_E2E_PROFILE`, default soak to `test`, default duration to 600 for test and 3600 for release, reject duration below the selected profile minimum, and include `acceptance_profile` in safe results.
- [ ] Add CLI profile flags to generation and verification, pass the selected profile to the Go options, and reject missing or unknown values fail closed.
- [ ] Run the focused Go and TypeScript tests and confirm they pass.

## Task 3: Runner, Make, CI, And Instruction Wiring

**Files:**

- Modify: `scripts/e2e/system-stateful.sh`
- Modify: `scripts/e2e/system-stateful-test.sh`
- Modify: `Makefile`
- Modify: `.github/workflows/ci.yml`
- Modify: `scripts/quality/e2e-attestation-test.sh`
- Modify: `scripts/quality/system-e2e-instructions-test.sh`
- Modify: `docs/agent/instructions.md`
- Modify: `.agents/skills/stratum-e2e-development/SKILL.md`
- Regenerate: `AGENTS.md`
- Regenerate: `CLAUDE.md`

- [ ] Extend shell contract tests to assert soak defaults to `STATEFUL_E2E_PROFILE=test` and 600 seconds, release uses 3600 seconds, generation receives the profile, and CI verification requires the test profile.
- [ ] Run `bash scripts/e2e/system-stateful-test.sh`, `bash scripts/quality/e2e-attestation-test.sh`, and `bash scripts/quality/system-e2e-instructions-test.sh`; confirm the new assertions fail.
- [ ] Pass the profile through runner generation, add `E2E_REQUIRED_PROFILE ?= test`, make the PR attestation check require it, and add an explicit `e2e-system-release-soak` target.
- [ ] Keep CI browser-free and set the PR job's required profile to `test`; update the source instruction and E2E skill to describe test and release profiles.
- [ ] Regenerate project instruction files with `bash scripts/quality/generate-agent-instructions.sh`.
- [ ] Re-run all shell contract tests and confirm they pass.

## Task 4: Full Verification And Test Attestation

**Files:**

- Generate: `test/e2e/attestations/` source-digest-named JSON through `cmd/e2e-attestation`

- [ ] Run `make fe-lint`, `npm --prefix web run typecheck`, `stratum-verify frontend-test`, `make fe-build`, focused Go tests, `make risk-guardrails`, and `git diff --check`; require zero failures.
- [ ] Freeze source and run `STATEFUL_E2E_PROFILE=test STATEFUL_E2E_DURATION_SEC=600 STATEFUL_E2E_PACKS=all make e2e-system-soak` without concurrent source or PR mutations.
- [ ] Verify the generated attestation locally, then commit it and run `make e2e-attestation-check E2E_REQUIRED_MODE=soak E2E_REQUIRED_PROFILE=test` against committed `HEAD`.
- [ ] Inspect the proof for mode `soak`, profile `test`, duration at least 600, 12 passed packs, 95 passed capabilities, positive UI/HTTP/database/reconciled evidence, successful cleanup, no residual IDs, and no unverified capabilities.

## Task 5: Ship PR 148

**Files:**

- Update PR body through a temporary body file outside tracked source.

- [ ] Commit only task-related files with repository title conventions and push `test/workflow-stateful-soak` using `--force-with-lease` only if required by the completed rebase.
- [ ] Repair PR #148 body with What, Why, and HowToTest using `gh pr edit --body-file`.
- [ ] Wait for every required GitHub check to pass; diagnose and fix any failure without weakening the acceptance contract.
- [ ] Squash-merge PR #148 with remote branch deletion, verify `main` contains the merge, and clean the isolated worktree through the approved repository workflow.
- [ ] Generate the ignored knowledge-deposition report and complete a requirement-by-requirement audit before reporting completion.
