# Fresh Main Worktree Guard Design

## Goal

Ensure every agent-created feature branch and worktree starts from the latest remote `main`, while keeping the local
`main` worktree unchanged.

## Design

Add `scripts/new-worktree.sh <path> <branch>` as the only supported creation entry point. The script validates its
arguments, confirms the repository has an `origin` remote, runs `git fetch origin main`, and creates the worktree with
`git worktree add <path> -b <branch> origin/main`. A fetch failure or existing branch/path fails closed before creation.

Claude Code and Codex keep separate PreToolUse hook implementations because their hook protocols differ. Both hooks
apply the same policy:

- allow the approved script entry point;
- deny raw branch-creating forms of `git worktree add`, `git checkout -b/-B`, `git switch -c/-C`, and `git branch`;
- allow read-only Git commands and operations on existing branches.

`AGENTS.md` documents the approved command. Hook rejection messages point agents to the script.

## Verification

Unit-style hook tests cover approved-entry allowance, raw branch-creation denial, and ordinary Git command allowance
for both Claude and Codex protocols. A shell integration test uses a temporary bare remote, advances remote `main`,
runs the creation script from a stale local clone, and asserts the new worktree HEAD equals the refreshed
`refs/remotes/origin/main` commit.

## Non-goals

- Updating or checking out the local `main` branch.
- Managing worktree cleanup.
- Supporting branches based on another feature branch through the automated entry point.
