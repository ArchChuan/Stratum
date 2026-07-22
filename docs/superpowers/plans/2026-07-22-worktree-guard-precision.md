# Worktree Guard Precision Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce false-positive primary-checkout mutation blocks, reliably allow explicit linked-worktree targets, track nested governance files, and restore a passing guard test suite.

**Architecture:** Keep `/home/yang/.local/lib/stratum-worktree-policy.sh` as the runtime policy source and `/home/yang/.local/bin/test-stratum-worktree-guard` as its executable contract. Target resolution validates linked worktrees by Git common directory; command classification distinguishes concrete mutation signals from exact read-only interpreter queries.

**Tech Stack:** Bash, jq, Git worktrees, shell regression tests

---

## Task 1: Correct Governance Expectations

**Files:**

- Modify: `/home/yang/.local/bin/test-stratum-worktree-guard`
- Test: `/home/yang/.local/bin/test-stratum-worktree-guard`

- [ ] Change the root `AGENTS.md` case from expected `allow` to expected `deny`, because `AGENTS.md` is tracked.
- [ ] Run `/home/yang/.local/bin/test-stratum-worktree-guard` and verify the stale expectation no longer fails while new precision cases are not yet present.

## Task 2: Add Failing Precision Tests

**Files:**

- Modify: `/home/yang/.local/bin/test-stratum-worktree-guard`
- Test: `/home/yang/.local/bin/test-stratum-worktree-guard`

- [ ] Add cases proving that an explicit linked-worktree `workdir`, absolute edit path, single and compound `git -C`, and validated `STRATUM_WORKTREE_ROOT` are allowed from a primary-cwd payload.
- [ ] Add cases proving that mixed Git roots, false declarations, unknown scripts, inline interpreter code, and concrete redirections remain denied.
- [ ] Add representative read-only cases for `bash -n`, `python --version`, and `node --version`, plus a denial case for `make --dry-run` because it may execute parse-time shell expressions.
- [ ] Run the test and verify the read-only cases fail with the primary-checkout mutation reason.

## Task 3: Refine Command Classification

**Files:**

- Modify: `/home/yang/.local/lib/stratum-worktree-policy.sh`
- Test: `/home/yang/.local/bin/test-stratum-worktree-guard`

- [ ] Add a validator that permits only exact read-only interpreter/build-tool forms covered by tests.
- [ ] Remove bare `bash`, `sh`, `python`, `python3`, `node`, and `make` from the generic mutation expression while adding explicit denial for script execution and inline code forms.
- [ ] Keep concrete filesystem, Git, dependency, redirection, mixed-root, and branch-change mutation patterns unchanged.
- [ ] Run `bash -n` and the full shell test suite; expect all cases to pass.

## Task 4: Track Nested Governance Files

**Files:**

- Create: `pkg/constants/AGENTS.md`
- Create: `pkg/constants/CLAUDE.md`
- Create: `pkg/migration/AGENTS.md`
- Create: `pkg/migration/CLAUDE.md`
- Create: `pkg/storage/AGENTS.md`
- Create: `pkg/storage/CLAUDE.md`
- Create: `web/AGENTS.md`
- Create: `web/CLAUDE.md`

- [ ] Copy the eight existing untracked files from the primary checkout without changing their contents.
- [ ] Verify each source and destination with `cmp`.
- [ ] Confirm all eight paths appear in `git status --short` and add them to the feature branch.

## Task 5: Verify Adapters And Repository Guards

**Files:**

- Verify: `/home/yang/.codex/hooks/main-branch-guard.sh`
- Verify: `/home/yang/.claude/hooks/stratum-worktree-guard.sh`
- Verify: `/home/yang/.local/lib/stratum-worktree-policy.sh`
- Verify: `/home/yang/.local/bin/test-stratum-worktree-guard`

- [ ] Run `bash -n` on the policy, test, and both adapters.
- [ ] Run the complete worktree guard test suite and require zero failures.
- [ ] Send representative allow and deny payloads through both adapters and validate their protocol envelopes with jq.
- [ ] Run `make risk-guardrails` in the feature worktree and expose any unrelated baseline failure.
- [ ] Commit the plan, governance files, and repository documentation using the required title format.
