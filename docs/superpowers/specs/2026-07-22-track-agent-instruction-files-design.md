# Track Agent Instruction Files

## Goal

Add the existing module-level `AGENTS.md` and `CLAUDE.md` files to Git so their instructions are versioned with the repository.

## Scope

Track these eight files without changing their contents:

- `web/AGENTS.md`
- `web/CLAUDE.md`
- `pkg/constants/AGENTS.md`
- `pkg/constants/CLAUDE.md`
- `pkg/migration/AGENTS.md`
- `pkg/migration/CLAUDE.md`
- `pkg/storage/AGENTS.md`
- `pkg/storage/CLAUDE.md`

The root `AGENTS.md` and `CLAUDE.md` are already tracked and require no change.

## Non-goals

- Do not change instruction content.
- Do not add CI checks or repository scripts.
- Do not change `.gitignore` or `.git/info/exclude`.
- Do not discover or generate additional instruction files.

## Verification

- Confirm all ten repository instruction files appear in `git ls-files`.
- Confirm the eight module-level files match their source copies byte for byte.
- Confirm `git diff --check` passes.
