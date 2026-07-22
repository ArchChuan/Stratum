# Worktree Guard Precision Design

## Goal

Reduce false-positive mutation blocks in the Stratum primary checkout while preserving its read-only role. Agents may write only when the target is an explicitly identified linked worktree belonging to the same repository.

## Scope

- Refine the shared worktree policy used by Codex and Claude Code.
- Correct and extend the policy test suite.
- Track the existing nested `AGENTS.md` and `CLAUDE.md` governance files.
- Do not weaken branch-change, Git mutation, destructive command, or mixed-root protections for the primary checkout.

## Target Resolution

The policy resolves the effective target in this order:

1. tool-level `workdir` or `cwd`;
2. an explicit absolute `cd <path> && ...` prefix;
3. consistent `git -C <path>` targets across every command segment;
4. a validated absolute `STRATUM_WORKTREE_ROOT` declaration;
5. the session cwd as fallback.

A resolved target is writable only when Git confirms that it is a linked worktree with the same common Git directory as the Stratum primary checkout and its top-level path differs from the primary root. Mixed roots, ambiguous shell syntax, missing paths, and invalid declarations fail closed.

For file editing tools, an absolute target outside the primary checkout remains allowed. A path inside an explicitly identified Stratum linked worktree is therefore writable even when the session cwd is the primary checkout.

## Command Classification

The primary checkout remains read-only. The policy blocks commands with concrete mutation signals, including:

- filesystem writers such as `rm`, `mkdir`, `cp`, `mv`, `install`, `tee`, formatters, and file redirections;
- Git state changes such as add, commit, merge, rebase, restore, stash, and branch changes;
- dependency installation or update commands;
- interpreters or shells that contain inline code, script files, or other content whose effects cannot be established safely.

The policy no longer treats every interpreter query as a mutation. Known read-only syntax and version checks are allowed through explicit, test-backed classification. Unknown scripts and build-tool invocations remain denied because their effects cannot be determined in a pre-tool hook; in particular, `make --dry-run` may still execute parse-time shell expressions or `+` recipes.

Existing narrow exceptions remain: the required worktree creation helper, risk guard explanation, fast-forward-only main update, bounded temporary cleanup, read-only curl, and constrained SSH diagnostics.

## Governance Files

The following existing nested governance files will be added to version control from the feature worktree:

- `pkg/constants/{AGENTS.md,CLAUDE.md}`
- `pkg/migration/{AGENTS.md,CLAUDE.md}`
- `pkg/storage/{AGENTS.md,CLAUDE.md}`
- `web/{AGENTS.md,CLAUDE.md}`

Their contents are preserved as-is. Root `AGENTS.md` and `CLAUDE.md` are already tracked and remain protected in the primary checkout.

## Tests

The shell test suite will verify:

- tracked root governance files are denied in the primary checkout;
- the eight nested governance files are tracked by the feature branch;
- explicit linked-worktree `workdir`, absolute file paths, `git -C`, and validated declarations are allowed;
- mixed or false linked-worktree declarations are denied;
- representative read-only commands do not trigger mutation blocks;
- unknown scripts and commands with concrete write signals remain denied;
- both Codex and Claude adapters return their platform-specific allow/deny envelopes.

Tests are written or corrected before policy implementation changes. Each new behavior must first fail for the expected reason, then pass after the smallest policy change.

## Rollout And Recovery

The repository contains tests and documentation, while the active shared policy and adapters live under the user's local configuration. After repository review, the validated policy is installed to the shared location and both adapters are tested against it. The previous shared files are retained through the existing version-controlled feature change or a timestamped local backup before replacement.

Any target-resolution ambiguity remains a denial with the effective cwd included in the reason so agents can correct their tool invocation.
