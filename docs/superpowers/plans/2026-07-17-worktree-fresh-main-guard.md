# Fresh Main Worktree Guard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Force agent-created worktrees and branches to start from the freshly fetched `origin/main` commit.

**Architecture:** A tracked repository script is the sole supported creation entry point and performs fetch plus
worktree creation. Because this repository ignores local agent configuration, Claude Code uses a user-level hook that
calls a primary-workspace script, while Codex extends its existing trusted user-level main-branch guard. Each enforces
the same deny/allow contract and has local tests. A tracked integration test verifies the repository script against a
temporary bare remote.

**Tech Stack:** Bash, Git worktrees, jq-based Claude/Codex hooks

---

## Task 1: Define the dual-hook policy with failing tests

**Files:**

- Modify locally: `/home/yang/go-projects/stratum/.claude/hooks/run-tests.sh`
- Modify locally: `/home/yang/go-projects/stratum/.codex/hooks/run-tests.sh`
- Create locally: `/home/yang/go-projects/stratum/.claude/hooks/worktree-guard.sh`
- Modify locally: `/home/yang/.codex/hooks/main-branch-guard.sh`

- [ ] Add test cases that deny `git worktree add -b`, `git checkout -b`, `git switch -c`, and `git branch feat/x`, allow
  `bash scripts/new-worktree.sh ../stratum-x feat/x`, and allow `git status`.
- [ ] Run `bash .claude/hooks/run-tests.sh` and `bash .codex/hooks/run-tests.sh`; verify failure because
  `worktree-guard.sh` does not exist.
- [ ] Implement protocol-specific guards that parse Bash commands, recognize the approved script entry point, and deny
  raw branch creation with a message naming `scripts/new-worktree.sh`.
- [ ] Register the Claude guard in `/home/yang/.claude/settings.json`; keep the existing Codex Hook definition unchanged
  so its persisted trust remains valid.
- [ ] Re-run both hook suites and verify all cases pass.

## Task 2: Implement fresh-main worktree creation

**Files:**

- Create: `scripts/new-worktree.sh`
- Create: `scripts/test-new-worktree.sh`

- [ ] Write an integration test that creates a temporary bare `origin`, clones it twice, advances `main` in the second
  clone, runs `scripts/new-worktree.sh` from the stale first clone, and asserts the new worktree HEAD equals remote main.
- [ ] Run `bash scripts/test-new-worktree.sh`; verify failure because the creation script does not exist.
- [ ] Implement strict two-argument validation, repository/origin checks, `git fetch origin main`, and
  `git worktree add "$path" -b "$branch" origin/main`.
- [ ] Re-run `bash scripts/test-new-worktree.sh`; verify it passes and temporary files are removed by a trap.

## Task 3: Document and verify the workflow

**Files:**

- Modify locally: `/home/yang/go-projects/stratum/AGENTS.md`
- Modify locally: `/home/yang/go-projects/stratum/.claude/hooks/README.md`
- Modify locally: `/home/yang/go-projects/stratum/.codex/hooks/README.md`

- [ ] Replace the raw worktree command in `AGENTS.md` with
  `bash scripts/new-worktree.sh ../stratum-<feature> feat/<feature>` and state that it fetches `origin/main` first.
- [ ] Add `worktree-guard.sh` responsibilities and the approved entry point to both hook READMEs.
- [ ] Run `bash .claude/hooks/run-tests.sh`, `bash .codex/hooks/run-tests.sh`, and
  `bash scripts/test-new-worktree.sh`; verify zero failures.
- [ ] Run `git diff --check` and inspect `git diff --stat` to confirm only planned tracked files changed; separately
  validate the local ignored JSON and Hook suites.
- [ ] Commit the implementation as `chore(git): enforce fresh-main worktree creation`.
