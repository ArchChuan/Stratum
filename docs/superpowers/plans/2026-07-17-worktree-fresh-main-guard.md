# Fresh Main Worktree Guard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Force agent-created worktrees and branches to start from the freshly fetched `origin/main` commit.

**Architecture:** A repository script is the sole supported creation entry point and performs fetch plus worktree creation.
Claude Code and Codex retain separate PreToolUse scripts but enforce the same deny/allow contract, with tests in each
native hook suite and an integration test against a temporary bare remote.

**Tech Stack:** Bash, Git worktrees, jq-based Claude/Codex hooks

---

## Task 1: Define the dual-hook policy with failing tests

**Files:**

- Modify: `.claude/hooks/run-tests.sh`
- Modify: `.codex/hooks/run-tests.sh`
- Create: `.claude/hooks/worktree-guard.sh`
- Create: `.codex/hooks/worktree-guard.sh`

- [ ] Add test cases that deny `git worktree add -b`, `git checkout -b`, `git switch -c`, and `git branch feat/x`, allow
  `bash scripts/new-worktree.sh ../stratum-x feat/x`, and allow `git status`.
- [ ] Run `bash .claude/hooks/run-tests.sh` and `bash .codex/hooks/run-tests.sh`; verify failure because
  `worktree-guard.sh` does not exist.
- [ ] Implement protocol-specific guards that parse Bash commands, recognize the approved script entry point, and deny
  raw branch creation with a message naming `scripts/new-worktree.sh`.
- [ ] Register each guard as a `PreToolUse:Bash` command in `.claude/settings.local.json` and `.codex/hooks.json`.
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

- Modify: `AGENTS.md`
- Modify: `.claude/hooks/README.md`
- Modify: `.codex/hooks/README.md`

- [ ] Replace the raw worktree command in `AGENTS.md` with
  `bash scripts/new-worktree.sh ../stratum-<feature> feat/<feature>` and state that it fetches `origin/main` first.
- [ ] Add `worktree-guard.sh` responsibilities and the approved entry point to both hook READMEs.
- [ ] Run `bash .claude/hooks/run-tests.sh`, `bash .codex/hooks/run-tests.sh`, and
  `bash scripts/test-new-worktree.sh`; verify zero failures.
- [ ] Run `git diff --check` and inspect `git diff --stat` to confirm only planned files changed.
- [ ] Commit the implementation as `chore(git): enforce fresh-main worktree creation`.
