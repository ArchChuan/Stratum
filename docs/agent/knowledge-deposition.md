# Knowledge deposition policy

This document is the canonical owner of Stratum's knowledge-deposition categories, report fields, and write boundaries.
Generated root instructions contain only a gate and a link to this policy.

## Task-end gate

Before the final response for every substantive implementation, architecture, incident, debugging, or review task:

1. Create an authoritative JSON report and rendered Markdown pair at
   `tmp/knowledge-deposition/YYYY-MM-DD/<client>-<session-id>-<task-id>.{json,md}`, with the current-task marker at
   `tmp/knowledge-deposition/current/<client>-<session-id>.json`.
2. Evaluate each candidate against the destination boundaries and evidence requirements below.
3. Record every retained candidate, or explicitly record `none` when no candidate qualifies.
4. Summarize the same report in the final response; do not introduce candidates that are absent from the report.

A substantive task changes or evaluates project behavior, architecture, operations, security, data governance, or reusable
engineering practice. Routine questions and mechanical status checks do not require a report.

## Destinations

Each candidate has exactly one target. Inclusion requires all conditions in that row to be confirmed.

| Target | Confirmed inclusion boundary |
| --- | --- |
| `skill` | A reusable, multi-step procedure that improves agent execution across repeated tasks and has stable activation conditions, inputs, checks, and failure handling. |
| `hook` | A deterministic, automatically checkable invariant that should block or warn at a defined lifecycle event and can run without semantic judgment or hidden context. |
| `global_md` | A short, durable instruction that applies across repositories and agent sessions, is important on every relevant turn, and cannot be enforced more reliably by a hook or skill. |
| `obsidian` | Durable cross-project knowledge such as a verified principle, case, counterexample, or correction that benefits from links, provenance, and later synthesis; project-only implementation facts do not qualify. |
| `project_git` | A Stratum-specific fact, contract, architectural decision, operating procedure, or regression lesson that must evolve and be reviewed with this repository's code, tests, docs, or ADRs. |
| `none` | No candidate is sufficiently novel, reusable, evidenced, or in scope for the destinations above; routine edits, transient state, and duplicates resolve here. |

## Evidence and duplicate checks

Every retained candidate must record:

- claim;
- evidence paths;
- scope;
- exclusions or counterexamples;
- duplicate result;
- confidence;
- target.

Before selecting a target, search that target's existing canonical sources and record whether the candidate is new,
updates an existing item, conflicts with one, or is already covered. A search snippet is not evidence. Claims must remain
within the demonstrated version and scope, and conflicting evidence must be preserved rather than silently reconciled.

Candidates targeting `obsidian` must additionally record:

- knowledge type;
- Vault queries;
- related notes;
- verification status;
- governance action.

## Write boundary

The task-end gate produces a report and recommendation only. It must not automatically write to Skills, hooks, global
instructions, project documentation or ADRs, or Obsidian. Any deposition is a separate, explicitly authorized and reviewed
task under the destination's own governance.

Raw logs, conversations, prompts, secrets, credentials, uncontextualized snippets, and unsupported guesses are never
knowledge-deposition candidates. Reference only the minimal evidence paths needed to verify a claim; do not copy sensitive
or irrelevant source material into a report.
