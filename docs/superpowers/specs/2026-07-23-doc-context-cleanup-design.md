# Documentation Context Cleanup Design

**Date:** 2026-07-23

**Scope:** Current documentation under `docs/`

## Goal

Reduce duplicated and unreachable current documentation while preserving useful project context and all historical evidence. Make it clear which files enter an agent context automatically, which files are loaded through explicit references, and which files are reader-facing guides only.

## Context Model

- Root and nested `AGENTS.md` files are Codex instruction entry points. The repository root file is generated from `docs/agent/instructions.md` and `docs/agent/templates/agents-prefix.md`.
- Root and nested `CLAUDE.md` files may import selected `docs/agent/*.md` files with `@...` references.
- A Markdown file elsewhere under `docs/` does not enter an agent context merely because it exists. It must be referenced by an applicable instruction file or opened for the task.
- `docs/documentation-map.md` is the reader-facing navigation entry. It does not make every linked file persistent agent context.

## Cleanup Boundary

Historical evidence remains unchanged:

- `docs/superpowers/specs/`
- `docs/superpowers/plans/`
- `docs/audits/`

The cleanup applies only to current guides, indexes, generated package documentation, and module context documents.

## Decision Rules

A current document may be deleted only when all of these conditions hold:

1. It has no effective reference from `README.md`, `docs/documentation-map.md`, instruction files, scripts, tests, CI, source comments, or another retained current document.
2. Its useful content is already covered by a retained document that is at least as current and authoritative.
3. It is not an independent runbook, architecture decision, operational baseline, or generated artifact required by repository tooling.
4. Removing it leaves no broken links or lost task-specific instructions.

An unreferenced document with unique, valid content is not deleted automatically. It is either added to the navigation map, linked from the appropriate context document, or retained with a clearer purpose.

## Consolidation Strategy

Use conservative cleanup:

- keep `docs/agent/instructions.md`, its templates, and module documents referenced by root or nested agent instruction files;
- retain one authoritative explanation for each current workflow;
- replace duplicated operational or conceptual prose with a short link to the authoritative document;
- remove stale navigation entries and obsolete documents only after the decision rules are satisfied;
- update `docs/documentation-map.md` to distinguish automatic context, on-demand context, current reader guides, generated references, and historical records.

No application code, runtime configuration, historical conclusion, or product behavior changes are in scope.

## Verification

The implementation must provide:

- a repository-wide inbound-reference inventory for every removed document;
- a review of replacement coverage for every deleted or shortened section;
- no broken relative Markdown links in retained current documentation;
- current generated `AGENTS.md` and `CLAUDE.md` entries;
- passing agent-instruction checks and repository risk guardrails relevant to documentation changes;
- a final diff confirming that historical directories were not modified.

## Success Criteria

- Context entry points and on-demand references are explicit in `docs/documentation-map.md`.
- Current documentation contains no confirmed duplicate file whose useful content is fully covered elsewhere.
- No uniquely useful current documentation is removed solely because it lacks inbound links.
- Historical plans, specifications, and audits remain intact.
