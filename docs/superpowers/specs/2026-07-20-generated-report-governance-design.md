# Generated Report Governance Design

## Goal

Consolidate confirmed automated-report risks into one tracked register, remove resolved risk prose from retained local reports, and prevent unattended report generators from repeatedly presenting closed or advisory-only items as current defects.

## Scope

- Include automated `tmp/*/reports/*.md` output that reports repository defects, dependency/security findings, migration failures, architecture violations, or change-summary risks.
- Exclude `agent-interview`, `interview-200`, and other knowledge/interview/advice reports.
- Preserve every local report file and its source metadata. Remove only resolved risk/finding/recommendation prose.
- Keep one tracked report as the current source of truth for open findings, closed finding summaries, verification evidence, source coverage, and governance mappings.

## Source Of Truth

`docs/audits/service-governance-2026-07-20-generated-reports.md` remains the canonical register. It will be reduced from a duplicated long-form finding report to five sections:

1. scope and source inventory;
2. current open risks and evidence gaps;
3. concise AR-001 through AR-024 closure index;
4. durable rule and enforcement mapping;
5. verification and provenance.

The register retains enough evidence to identify the affected chain, repair, and regression protection without copying the original reports' full prose.

## Historical Report Rewrite

For each included local Markdown report:

- retain title, generation time, generator/mode, scan baseline, and non-risk execution results;
- remove resolved finding descriptions, impact text, and recommendations;
- add a short migration notice naming the canonical register and the review date;
- retain genuinely open evidence gaps only when they are also present in the canonical register;
- update generated `latest.md` copies consistently;
- do not modify machine-readable evidence files or excluded knowledge reports.

Because `tmp/` is ignored, this cleanup is a local operational change. The durable behavior belongs in tracked governance rules and generator contracts.

## Agent Instructions

Add the same compact automated-report governance rule to `AGENTS.md` and `CLAUDE.md`:

- generated reports are candidate evidence, not repository facts;
- revalidate each finding against current code, tests, and runtime evidence;
- exclude knowledge/interview/advice output from defect backlogs;
- deduplicate against the canonical register;
- do not reopen closed findings without current reproducible evidence;
- move enforceable lessons into tests, linters, hooks, or CI rather than expanding prompt-only rules.

The entry documents point to the canonical register and do not duplicate all 24 findings.

## Harness Integration

The local unattended generators in `tmp/cron/` will receive a shared output contract:

- read the canonical register before reporting;
- classify output as `new`, `reopened`, `still-open`, or `no-current-finding`;
- require current file/call-path and reproduction evidence for defect classifications;
- suppress closed findings unless the present code demonstrates recurrence;
- keep advisory observations out of the confirmed-risk section;
- emit a canonical finding key to support cross-run deduplication.

A tracked quality script and test will encode this contract. The local cron scripts consume or mirror the tracked contract, while Git hooks and CI validate only tracked files. They must not depend on developer-local `tmp` state.

## Enforcement Placement

| Lesson | Durable location |
|---|---|
| DDD and wiring boundaries | `arch-guard`, depguard, architecture tests |
| Tenant DDL and historical-schema safety | migration boundary scripts and schema-order tests |
| Auth/API compatibility | handler/router contract and integration tests |
| Secret and dependency findings | tracked scanners and blocking CI jobs |
| Real dependency behavior | targeted integration/E2E tests |
| Report deduplication and status | canonical register plus generator contract |
| Human context and rationale | concise `AGENTS.md` and `CLAUDE.md` rule |

No pre-commit hook scans `tmp`: local reports are ignored, can be absent, and must not make commits machine-dependent.

## Failure Handling

- A generator that cannot inspect the canonical register marks its report incomplete instead of claiming no findings.
- A finding without current evidence remains advisory/provisional and cannot be promoted to the canonical open-risk list.
- Conflicting evidence is recorded in the canonical register; generators do not silently choose one conclusion.
- Historical cleanup never removes timestamps, generator identity, baselines, or machine-readable evidence pointers.

## Verification

- A report-governance test proves closed findings are suppressed, reopened findings require current evidence, and excluded report classes are unchanged.
- Static checks confirm `AGENTS.md` and `CLAUDE.md` contain the same governance contract and point to the canonical register.
- A local audit confirms included historical Markdown files retain metadata but no longer duplicate resolved risk prose.
- Existing architecture, migration, deployment, secret-scan, Markdown, and full relevant test suites continue to pass.

## Non-Goals

- Deleting report files or machine-readable scan artifacts.
- Editing knowledge/interview/advice reports.
- Treating AI-generated summaries as authoritative without code review.
- Adding a Git hook whose result depends on ignored local files.
- Rewriting unrelated agent instructions or report scheduling.
