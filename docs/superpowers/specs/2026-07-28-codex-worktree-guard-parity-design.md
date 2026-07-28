# Codex Worktree Guard Parity Design

## Goal

Make Codex enforce the same Stratum primary-checkout protection decisions as Claude Code without changing Claude Code behavior or weakening the shared worktree policy.

## Current state

Claude Code and Codex already use separate protocol adapters over one shared policy:

- Claude Code adapter: `~/.claude/hooks/stratum-worktree-guard.sh`
- Codex adapter: `~/.codex/hooks/main-branch-guard.sh`
- Shared policy: `~/.local/lib/stratum-worktree-policy.sh`

The adapters must remain separate because Claude Code and Codex require different hook response envelopes. The policy decision itself must remain identical for equivalent inputs.

## Scope

This change will:

1. add parity coverage that sends equivalent operations through both adapters;
2. compare normalized allow/deny decisions and denial reasons;
3. cover primary-checkout writes, protected Git mutations, read-only diagnostics, the approved worktree helper, and linked-worktree writes;
4. change only the Codex adapter or its registration if a failing parity test proves a real divergence.

This change will not alter Claude Code configuration, broaden the writable surface of the primary checkout, duplicate the shared policy, or make Codex consume Claude Code's protocol envelope.

## Design

The existing shared-policy test remains the source of truth for policy semantics. A client-parity layer will invoke the Claude Code and Codex adapters with equivalent payloads, normalize their protocol-specific JSON responses into a common decision model, and assert equality.

The normalized model contains:

- `decision`: `allow` or `deny`;
- `reason`: empty for allowed operations and the user-facing denial reason for rejected operations.

Denial reasons must preserve the effective working directory and the instruction to create a feature worktree with `scripts/new-worktree.sh`. Protocol-only fields are intentionally excluded from equality checks.

## Test cases

The parity suite will verify at least:

- editing a tracked file in the primary checkout is denied by both clients;
- `git add`, branch switching, and native `git worktree add` in the primary checkout are denied;
- `git status` and bounded read-only diagnostics are allowed;
- `bash scripts/new-worktree.sh <path> <branch>` is allowed;
- editing a file in a valid linked Stratum worktree is allowed;
- a command executed with an explicit linked-worktree root is allowed;
- equivalent denials contain the same effective cwd and worktree guidance.

The test must fail before any Codex-side correction is made. If all parity cases already pass, no production hook change is justified; the deliverable is the regression coverage proving the clients are aligned.

## Failure handling and safety

Malformed payloads and ambiguous roots remain fail-closed according to the shared policy. Tests use temporary fixtures or existing validated worktree fixtures and do not mutate the primary checkout. No credentials, environment secrets, or command history contents are printed.

## Acceptance criteria

1. Equivalent Claude Code and Codex inputs produce the same allow/deny decision.
2. Equivalent denials carry the same policy reason after protocol normalization.
3. The primary checkout remains read-only for tracked project changes.
4. The approved worktree creation script and valid linked-worktree operations remain allowed.
5. Existing shared-policy, Claude adapter, Codex adapter, and repository risk-guard tests pass.
