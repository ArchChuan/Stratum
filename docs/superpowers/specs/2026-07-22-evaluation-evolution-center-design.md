# Evaluation and Evolution Center Design

**Date:** 2026-07-22
**Status:** Approved design
**Scope:** Tenant-level evaluation and human-gated evolution for Skill, Agent, MCP, and Knowledge resources

## 1. Goal

Provide tenant administrators with one operational center to evaluate four resource types, generate bounded candidate revisions, compare candidates with stable revisions, run canary experiments, and explicitly promote or roll back based on durable evidence.

The first release covers all four resource types:

- Skill: instruction quality, tool use, output assertions, and bounded instruction/runtime-parameter candidates.
- Agent: end-to-end task quality, cost, latency, tool-chain behavior, and bounded prompt/runtime/binding candidates.
- MCP: tool availability, contract compliance, success rate, latency, and bounded timeout/retry/tool-enable candidates.
- Knowledge: retrieval relevance, citation correctness, no-answer behavior, and bounded retrieval/reranking/query-rewrite candidates.

The product is human-gated. The system may generate candidates, calculate recommendations, and automatically stop unsafe canary traffic, but it must not automatically promote a candidate to stable.

## 2. Current State and Constraints

The repository currently has a working Skill evaluation path:

- `internal/evaluation` owns suites, runs, jobs, candidates, experiments, deployments, feedback, and stage decisions.
- `web/src/modules/evaluation` exposes a Skill-only API model and a local `SkillEvaluationPanel` workflow.
- Evaluation routes support creation and lookup by ID, but do not expose the paginated list, overview, or timeline queries required by a tenant-level center.
- `ResourceKind` and the frontend resource schema currently accept only `skill`.
- Skill already has immutable revisions and candidate creation.
- Agent, MCP, and Knowledge currently expose mutable configuration models and do not have an equivalent immutable revision/deployment contract.
- Opik/OTel traces are the runtime evidence source; encrypted large payloads are stored through MinIO rather than tenant observation tables.

The design must preserve the repository's dependency direction, tenant-schema isolation, API error contract, encrypted-payload policy, and explicit administrative authorization.

## 3. Non-Goals

The first release does not:

- modify MCP tool definitions or input schemas automatically;
- generate, rewrite, or delete Knowledge documents;
- add new external tools, data sources, Skills, MCP servers, or Knowledge workspaces to an Agent candidate;
- expand an Agent candidate's permissions or data-access boundary;
- automatically promote a candidate to stable;
- replace Opik with a second trace store;
- expose raw prompts, tool arguments, retrieved content, credentials, or encrypted payloads in list APIs or logs;
- provide a general-purpose experiment builder outside the four supported resource adapters.

## 4. Architecture

`evaluation` remains the consumer-side orchestration context. It defines the contracts needed to snapshot, execute, generate a bounded candidate, and resolve a deployed revision. It does not import sibling contexts' application or infrastructure packages.

Provider behavior is connected through thin adapters in `api/wiring`:

```text
Evaluation and Evolution Center
  -> evaluation HTTP/API
      -> evaluation application services
          -> shared suites, runs, candidates, experiments, deployments
          -> EvaluableResourceAdapter
              -> Skill wiring adapter
              -> Agent wiring adapter
              -> MCP wiring adapter
              -> Knowledge wiring adapter
          -> Opik trace evidence reader
          -> encrypted MinIO payload store
```

The consumer-side adapter contract has four responsibilities:

1. Resolve and validate an immutable resource revision.
2. Execute an evaluation case against that exact revision.
3. Create a bounded candidate revision from an approved search space or rewrite request.
4. Return a safe summary/diff for review without exposing sensitive payload content.

Deployment resolution is shared. Runtime callers resolve a stable or canary revision from `evaluation_deployments`, then pass that immutable revision to the provider. Providers must not infer the current mutable configuration after assignment.

## 5. Shared State Model

Every resource follows one state path:

```text
draft
  -> published baseline
  -> baseline evaluated
  -> candidate generated
  -> candidate evaluated
  -> administrator starts canary
  -> online evidence accumulates
  -> administrator promotes or rolls back
```

Illegal transitions fail explicitly. In particular:

- an unpublished revision cannot be evaluated as a baseline;
- a candidate cannot enter canary before its required offline suite passes;
- promotion is disabled until sample and observation-time gates pass;
- stale decisions cannot overwrite a newer experiment state;
- only one active deployment exists per tenant, resource kind, and resource ID;
- a hard safety violation may automatically set canary traffic to zero, but does not silently mark the candidate promoted or erase the decision history.

All write commands accept an idempotency key. State transitions use a database transaction and row-level locking. A repeated command returns the existing transition result rather than creating a second revision, experiment, or decision.

## 6. Resource Revision Boundaries

### 6.1 Skill

Skill keeps its existing immutable revision model. Candidate generation may change instructions and already-supported safe runtime parameters. Existing Skill workspace actions become a resource-scoped entry into the shared center rather than maintaining a separate in-memory workflow.

### 6.2 Agent

An Agent revision snapshots:

- system prompt/instructions;
- selected model and safe model parameters;
- maximum iteration count;
- existing Skill, MCP tool, and Knowledge bindings plus per-binding enabled state;
- other execution settings needed to reproduce a run.

Candidate generation may change the prompt, safe model parameters, maximum iterations, and enable/disable state of bindings already authorized on the baseline. It cannot add a binding, widen permissions, or introduce a new external dependency.

### 6.3 MCP

An MCP revision snapshots non-secret server identity plus encrypted runtime configuration references, tool enablement, timeout, and bounded retry settings. Tool definitions and input schemas remain provider-discovered contracts and are hashed for drift detection.

Candidate generation may change timeout, finite retry policy, and enablement of tools that already exist on the baseline. It cannot modify a tool schema, credentials, transport destination, or risk classification.

### 6.4 Knowledge

A Knowledge revision snapshots the workspace identity, immutable document-set/content hash, embedding/reranking identity, and retrieval configuration.

Candidate generation may change top-K, score threshold, reranking configuration, and query-rewrite configuration. It cannot add, rewrite, or delete documents, change tenant ownership, or silently re-embed content with another model.

## 7. Persistence and Migration

Add a tenant-scoped `resource_revisions` table containing:

- revision ID, resource kind, and resource ID;
- parent revision ID and source (`manual`, `optimization`, or `rollback`);
- immutable content hash and safe summary;
- encrypted payload object reference and payload hash;
- creator and timestamps;
- lifecycle status.

Extend the existing tenant evaluation tables to accept all four resource kinds. Do not create one table set per resource type. Add `experiment_decisions` to retain:

- experiment ID and prior/new state;
- actor and action;
- recommendation and metric snapshot;
- administrator reason;
- idempotency key and timestamp.

Large or sensitive snapshots remain encrypted in MinIO. PostgreSQL stores references, hashes, safe summaries, and queryable metrics only.

Migration order must support historical tenants:

1. Create new tables with `IF NOT EXISTS`.
2. Add nullable/defaulted columns with `ADD COLUMN IF NOT EXISTS`.
3. Backfill existing Skill rows and deployments.
4. Add indexes and constraints that depend on backfilled columns.
5. Update both canonical tenant provisioning and the migration tenant baseline where required.
6. Prove old-schema-to-new-schema ordering in an integration test before deployment.

No existing evaluation table is dropped as part of this feature.

## 8. HTTP API

Keep the existing create/publish/run/optimize/experiment/feedback endpoints compatible. Add paginated tenant queries:

```text
GET /evaluations/overview
GET /evaluations/resources
GET /evaluations/suites
GET /evaluations/runs
GET /evaluations/candidates
GET /evaluations/experiments
GET /evaluations/resources/:kind/:id/timeline
```

Add explicit administrative commands:

```text
POST /evaluations/candidates/:id/reject
POST /evaluations/experiments/:id/pause
POST /evaluations/experiments/:id/promote
POST /evaluations/experiments/:id/rollback
```

List endpoints support cursor or bounded page pagination, resource-kind filtering, resource ID filtering, status filtering, and descending time order. Responses expose safe summaries and aggregate metrics, never raw payloads.

Tenant members may read the center. Active tenant administrators may create suites, run evaluations, generate/reject candidates, and change experiment state. A resource ID from another tenant is returned as not found, even when it exists.

## 9. Frontend Information Architecture

Add the administrator navigation entry `评测与进化`. The first viewport contains only resource type, status filter, and the primary `新建评测` command.

### 9.1 Overview

Show pending gates, running evaluations, active canaries, and seven-day rollback count. A unified resource view uses at most five columns:

1. resource name;
2. resource type;
3. stable revision;
4. current loop stage;
5. primary quality indicator.

Selecting a resource opens its detail timeline.

### 9.2 Evaluation Runs

Show baseline/candidate comparison, pass rate, quality score, cost, P95 latency, and error rate. Failed runs expand to cases and execution steps, with a safe Opik trace link where available. Raw payloads are not rendered.

### 9.3 Candidates

Show a safe change summary and parameter diff:

- Skill/Agent: prompt summary and safe runtime parameters;
- MCP: timeout, retry, and tool enablement;
- Knowledge: top-K, threshold, reranking, and query rewrite.

Administrators may reject, evaluate, or send an eligible candidate to canary. Rejection and canary creation require a confirmation when consequences are material.

### 9.4 Experiments

Compare stable and canary quality, sample count, cost, latency, errors, and security events. Promotion, pause, and rollback use explicit confirmation. A disabled promotion action states the missing sample, duration, or guardrail requirement.

### 9.5 Resource Timeline

The detail drawer presents one durable audit chain:

```text
suite created -> baseline run -> candidate generated -> candidate run
-> canary started -> feedback attributed -> promoted/paused/rolled back
```

The existing Skill evaluation tab becomes a compact resource-scoped view backed by the same APIs, with a link to the full center.

Desktop uses dense tables and drawers. Mobile uses stable compact items and a full-screen detail drawer; diffs and experiment metrics stack vertically. Fixed action areas must not overlap content.

## 10. Recommendation and Human Gates

The system computes a recommendation from offline results and attributed online evidence. The UI clearly separates:

- observed facts (sample count and measured metrics);
- configured guardrails;
- system recommendation;
- administrator decision and reason.

Recommendations never invoke a promotion command. Promotion requires an explicit administrator action against the current experiment version. Safety-stop logic may set canary percentage to zero when a hard violation occurs; it records a system decision and leaves the candidate and evidence available for review.

## 11. Failure Behavior

- **Opik unavailable:** new promotion evidence cannot be established. The UI reports the evidence dependency outage and disables promotion. Stable serving continues.
- **MinIO unavailable:** snapshot reads/writes fail before a revision transition commits. No partial revision or candidate is reported as successful.
- **LLM provider unavailable:** candidate generation or model-based evaluation fails visibly and remains retryable. It is not converted into a low score or successful empty candidate.
- **MCP/Knowledge dependency unavailable:** the evaluation records a failed sample. During canary, hard dependency or safety guardrails may stop canary traffic.
- **Database transition failure:** the transaction rolls back and the API returns the existing frozen error envelope.
- **Cross-tenant identifier:** return not found without confirming existence.
- **Malformed or sensitive upstream response:** return a safe error category and correlation identifier; do not log or render raw bodies.

## 12. Delivery Work Packages

The first release contains six sequential, independently testable packages:

1. Shared resource kinds, revisions, queries, timeline, human-gated state machine, and tenant isolation.
2. Skill migration from local panel state to persistent shared APIs.
3. Agent revision, execution adapter, and bounded candidate generator.
4. MCP revision, contract evaluator, and bounded runtime candidate generator.
5. Knowledge revision, retrieval evaluator, and bounded runtime candidate generator.
6. Tenant center UI, resource-scoped Skill entry, and full browser E2E.

Each provider adapter must pass the shared adapter contract suite before its UI capability is enabled.

## 13. Verification

Use the repository `stratum-e2e-development` workflow with isolated Opik, Collector, MinIO, PostgreSQL, and test LLM configuration. Never print credentials or raw secret settings.

Required E2E evidence includes:

- each resource completes baseline evaluation, candidate generation, candidate evaluation, canary, and manual promotion;
- failing candidates, security events, and dependency failures cannot be promoted and protect stable traffic;
- feedback is attributed only when Opik revision, experiment, variant, and trace evidence match;
- refresh preserves state and repeated commands are idempotent;
- members are read-only, admins can act, and cross-tenant resources are not observable;
- encrypted payloads are not present in PostgreSQL or logs;
- Opik, MinIO, LLM, MCP, Knowledge, and database failures do not produce successful half-state;
- desktop and mobile browser flows exercise filters, details, diffs, confirmation, promotion, and rollback, with HTTP and database assertions.

Verification layers:

- table-driven domain state-machine tests;
- shared adapter contract tests for all four providers;
- repository and migration-order integration tests;
- handler and frozen API contract tests;
- frontend model, hook, component, responsive, and permission tests;
- real API/browser/database E2E;
- `go vet`, short tests, race tests, frontend lint/build/test, `make risk-guardrails`, architecture guards, and secret scan.

## 14. Evidence and Claim Matrix

| Claim | Repository evidence | Obsidian input | External evidence | Boundary / conclusion |
|---|---|---|---|---|
| Skill already has a partial loop | Evaluation domain/services/routes and `SkillEvaluationPanel` | Search returned stale AI-generated clipping paths | Not required for local fact | Verified repository fact |
| Center needs list/timeline APIs | Routes expose creates and ID lookups only | No verified note found | Generic UI guidance is not sufficient | Verified implementation gap |
| Four resources need immutable revisions for auditable rollback | Only Skill currently has revision services; Agent/MCP/Knowledge mutate configuration | Search hits were unverified leads and were not used | Official dependency semantics must be checked during planning | Architectural conclusion from project requirements |
| Opik is the runtime evidence source | Existing Opik adapter, OTel metadata, tests, and merged migration | No verified note required | Current Opik API/docs must be checked before query implementation | Verified local choice; external API details pending version check |
| MCP schema must not be auto-mutated | MCP tools are provider-discovered contracts; user selected safe runtime-only candidates | No verified note found | Current MCP Tools specification must be checked during planning | Approved product boundary |
| Promotion remains human-gated | User-approved requirement and existing experiment state machine | AI clippings are not evidence | No external claim required | Approved product behavior |

The Obsidian search results referenced two AI/source clippings whose indexed paths no longer resolve. They are treated as unverified leads and do not support any design claim. Official Opik, OpenTelemetry, and MCP version details must be captured in the implementation plan before code relies on them.

## 15. Success Criteria

The feature is complete only when a tenant administrator can use the new center to run and inspect the full human-gated loop for Skill, Agent, MCP, and Knowledge in an isolated real environment; the evidence survives refresh; unsafe or insufficiently evidenced candidates cannot be promoted; promotion and rollback affect only the correct tenant and exact revision; and all required quality guards pass.
