# Remove Knowledge Deposition Mechanism Design

**Date:** 2026-07-28

**Status:** Approved

## Goal

Remove the Stratum task-end knowledge deposition mechanism from Codex and Claude Code without changing unrelated client
hooks or deleting previously generated local reports.

## Scope

The removal covers every active layer of the mechanism:

- user-level Codex registrations in `~/.codex/hooks.json`;
- user-level Claude Code registrations in `~/.claude/settings.json`;
- repository hook adapters, report writer, installer, validators, and their tests under `scripts/knowledge-deposition/`;
- the `knowledge-deposition-test` Make and pre-commit integration;
- generated-instruction inputs, prefixes, generator contracts, `AGENTS.md`, and `CLAUDE.md` task-end requirements;
- canonical policy and workspace documentation that describe installation or enforcement.

Existing ignored artifacts under any `tmp/knowledge-deposition/` directory and existing configuration backups remain
untouched. The general read-only knowledge-input protocol, Obsidian evidence guidance, and knowledge-domain product code
are outside this removal.

## Client Configuration Removal

The uninstall operation reads the two existing JSON files, validates their hook shape, and removes only command hooks
whose command points to a Stratum `scripts/knowledge-deposition/` task-start or stop adapter. It preserves ordering and
content of all unrelated hook entries. Empty managed hook groups are removed; unrelated empty structures are not
normalized as collateral cleanup.

Before replacement, the operation creates private timestamped backups and validates the transformed JSON. Both clients
are staged before either live file is replaced. If the second replacement fails, the first is restored from its backup.
The final verification rejects any remaining knowledge-deposition command while also comparing non-managed hooks against
the originals.

The repository implementation is removed only after the live registrations no longer reference it, preventing broken
commands from remaining active between steps.

## Repository Removal

Delete the runtime and test directory `scripts/knowledge-deposition/` and remove all direct build and pre-commit entries.
Simplify the agent-instruction generator so it no longer reads or requires `docs/agent/knowledge-deposition.md`, injects
prefix text about the mechanism, or asserts the former pre-commit hook. Regenerate `AGENTS.md` and `CLAUDE.md` from the
remaining canonical inputs.

Delete the canonical mechanism policy. Remove only the installation, report, and task-gate sections from broader
knowledge workspace documentation; retain unrelated knowledge product architecture and the repository evidence-input
protocol.

Historical design and implementation plans remain as immutable decision history. They may describe the removed system,
but current-state indexes and instructions must not present it as active.

## Verification

Tests first establish the desired absent state:

- generator tests require no deposition policy input or generated gate;
- pre-commit fixture tests require no `knowledge-deposition-test` hook;
- repository searches reject active references outside historical specs and plans;
- fixture configuration tests prove exact removal for Codex and Claude Code while preserving unrelated hooks;
- live JSON validation proves both client configurations contain no managed command after uninstall;
- `scripts/quality/generate-agent-instructions.sh --check`, `make risk-guardrails`, and relevant shell tests pass.

No new task-end report is required after the mechanism is removed. Reports created before removal remain readable in
their existing ignored locations.

## Failure Boundaries

- Invalid or unsafe client configuration paths stop the uninstall before mutation.
- A transform or validation failure leaves both live configurations unchanged.
- A partial publish attempts rollback and reports the protected backup path if rollback itself fails.
- The uninstall never deletes historical reports, backups, other hooks, client sessions, Skills, or Obsidian content.
