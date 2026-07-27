# System Stateful and Soak Browser E2E Design

## Purpose

Add a development-completion acceptance workflow that exercises all Stratum product domains through real headless Chromium sessions. The workflow performs repeated, stateful user operations, reconciles browser, HTTP, and database evidence, and emits a machine-readable attestation tied to the tested source tree. Pull-request CI does not run the browser suite; it verifies that a valid attestation covers the current source and required product capabilities.

## Product Boundary

The acceptance workflow covers every user-visible product domain:

- IAM;
- Dashboard;
- Agent;
- Skill;
- MCP;
- Knowledge;
- Memory;
- Evaluation;
- Workflow;
- cross-domain journeys between those domains.

Coverage is defined by a versioned manifest rather than an informal test-file list. Every managed frontend route, user-visible write operation, role gate, and critical business-state transition maps to a browser action and its evidence requirements, or to an explicitly justified lower test layer. A manifest check fails when a managed route or write API has no mapping.

This suite does not claim exhaustive input-combination coverage. Boundary combinations remain in unit and service tests, while every core user goal and critical authorization or publication gate has at least one real browser closure.

## Execution Model

The suite runs after feature implementation and before code is submitted for review:

```text
implementation complete
-> start local dependencies and backend
-> run system stateful acceptance in headless Chromium
-> run risk-required or release soak acceptance
-> reconcile UI, HTTP, and database evidence
-> generate source-bound attestation
-> commit code and attestation
-> CI validates the attestation without running browsers
```

The repository exposes one command for short acceptance and one for soak acceptance. The commands start or validate required services, run the browser packs, write safe artifacts, stop processes they started, and return nonzero on any failed, skipped, timed-out, or unreconciled required capability.

## Browser-Only User Actions

All stateful and soak packs use Playwright with real headless Chromium. Each product action begins from a user-visible page and operates menus, drawers, forms, selectors, tables, dialogs, and command buttons.

API calls are allowed only for bounded environment setup, capturing HTTP evidence, and directly proving a denied backend operation. Database access is allowed only for test identity discovery, exact test-role setup when no product bootstrap exists, persistence reconciliation, and exact cleanup. Neither API nor SQL may replace the primary UI action being accepted.

Desktop actions enter through the real application navigation. Mobile actions enter through the real navigation drawer. Direct page navigation cannot substitute for navigation coverage. Tests use role and label locators, web-first assertions, response or event waits, and database polling; fixed sleeps are prohibited except inside bounded infrastructure-health checks.

## Domain Packs

Each domain pack owns a state model and repeated CRUD or operational journeys:

| Pack | Required browser behavior |
|---|---|
| Dashboard | tenant summary, recent executions, resource counts, navigation entry and refresh read-back |
| IAM | login, refresh, tenant selection, membership CRUD, role changes, denied administration |
| Agent | Agent CRUD, resource binding, conversation, execution, execution-history read-back |
| Skill | draft creation, repeated editing, publication, revision selection, standalone test execution |
| MCP | server CRUD, connection lifecycle, tool discovery, policy changes, denied management |
| Knowledge | workspace CRUD, document lifecycle, processing terminal state, retrieval, tenant isolation |
| Memory | user read view, administrator filtering, scope handling, exact clearing, persistence read-back |
| Evaluation | dataset and suite lifecycle, runs, candidates, experiments, promotion gates and outcomes |
| Workflow | draft revisions, validation, publication, version read-back, runs, approvals, cancellation, intervention, stream recovery |

Cross-domain packs cover contracts that a domain pack cannot prove alone:

- Skill activation through Agent and MCP tool execution;
- Knowledge and Memory contribution to Agent context and execution records;
- Agent-backed Workflow execution and administrator control;
- Evaluation of Skill or Agent candidates and promotion outcome propagation.

Every pack runs at least two mutation/read-back cycles in short mode. Soak mode continues seeded valid action selection until its duration or cycle limit is reached.

## Stateful Core

The common stateful core provides:

- a credential-free expected-state model;
- action preconditions;
- deterministic seeded action selection;
- duration and cycle budgets;
- UI, HTTP, and database evidence collection;
- source-safe diagnostic serialization;
- exact entity tracking and cleanup;
- replay from seed and completed action prefix.

Each action implements the following conceptual contract:

```text
name
isEnabled(model)
execute(browserActors, model, evidence)
reconcile(database, model)
```

An action updates the expected model only after browser output, HTTP response, and persisted state agree. Unexpected transient errors fail the action; the framework does not retry product assertions. A failed soak pack stops before selecting another action and performs final reconciliation against the last known valid model.

Action logs contain only sequence number, seed, actor label, action name, entity IDs, expected state, observed status, and sanitized route templates. They never contain authorization headers, tokens, cookies, passwords, private keys, API keys, raw upstream responses, or sensitive request bodies.

Unit tests for the stateful core prove deterministic replay, action-precondition enforcement, budget termination, invalid-transition rejection, stale-revision rejection, ownership invariants, and credential-free diagnostics.

## Actors, Credentials, and Isolation

Administrator, tenant administrator, member A, and member B each own a separate Playwright browser context. Contexts do not share cookies, storage, request clients, or access tokens.

The harness may read dedicated test identities and credentials from a local or ephemeral E2E database. Such values remain in process memory and are never printed, written to artifacts, or embedded in Playwright traces. The harness cannot reverse stored password hashes. When no usable administrator login exists, it may create a temporary guest, promote only that exact membership in the test database, and obtain administrator claims through the real refresh or login flow.

All identity SQL validates UUIDs and uses parameterized queries. It is scoped to the selected test tenant and user. Tests never connect to production databases. CI and local environment checks must reject production-like database hosts or configurations before credential lookup or mutation.

Bearer credentials never enter URLs, Web Storage, generic logs, assertions, or failure messages. Trace and action-artifact writers redact cookies and authorization headers. Temporary role changes are reverted by exact identity or left for the existing guest reaper with an explicit residual-data record if product-safe cleanup is unavailable.

## Short Acceptance

Short acceptance is the mandatory development-completion gate. It runs all domain and cross-domain packs in parallel against isolated tenants. Each pack targets six to eight minutes; the aggregate wall-clock target is ten minutes on the supported development machine.

Short mode uses a repository-defined fixed seed and explicit minimum action sequence so coverage is stable and reviewable. Random selection may add enabled actions within the remaining budget but cannot replace required actions.

The command succeeds only when:

- every managed domain and cross-domain pack runs;
- every required browser journey completes;
- no required test is skipped;
- all state-changing actions have UI, HTTP, and database evidence;
- all role and tenant boundaries hold;
- final reconciliation reports zero unexplained differences;
- process and temporary-file cleanup succeeds;
- retained prefixed database rows, if any, are explicitly attested.

## Soak Acceptance

Soak acceptance reuses the same actors, action library, evidence collectors, and models. It defaults to 60 minutes and accepts bounded configuration:

- `STATEFUL_E2E_SEED`: unsigned integer replay seed;
- `STATEFUL_E2E_DURATION_SEC`: duration from 600 to 14400 seconds;
- `STATEFUL_E2E_MAX_CYCLES`: positive hard cycle limit;
- `STATEFUL_E2E_PACKS`: validated domain-pack subset, with `all` as the release default.

During soak execution, actors repeatedly create, update, query, publish, execute, cancel, approve, clear, and delete or deactivate resources as their current model permits. The harness periodically refreshes pages, performs real authentication refresh, closes and recreates pages, rebuilds browser contexts, switches tenants where the product allows it, and reconnects streams.

The soak loop finishes the current atomic action when time expires, performs final reconciliation, writes its attestation, and exits. Release acceptance requires all packs. A feature-specific development run may select impacted packs only when the risk classifier does not require cross-domain or full-system soak; the attestation records this narrower scope and cannot satisfy a full-release requirement.

## Coverage Manifest

The manifest is the auditable definition of system coverage. Each entry includes:

```text
capability ID
domain
user goal
route entry
allowed and denied roles
write API or state transition
browser action ID
HTTP evidence rule
database evidence rule
short or soak requirement
lower-layer justification when browser coverage is intentionally excluded
```

A repository checker compares the manifest with registered frontend routes, API path registries, and browser action IDs. It rejects duplicate capability IDs, unknown actions, missing evidence rules, unowned managed routes, and write operations without a browser or justified lower-layer mapping.

The attestation records the manifest digest and per-capability result. A report cannot claim full-system acceptance when any required capability is absent, skipped, or failed.

## Attestation

The acceptance runner writes a canonical JSON attestation under:

```text
test/e2e/attestations/<source-digest>.json
```

The source digest covers the content and repository-relative path of every tracked file plus every non-ignored untracked source file present in the implementation worktree, except the attestation output directory and other explicitly generated acceptance artifacts. Test framework code, coverage manifests, CI validators, dependency locks, application code, configuration, migrations, and deployment inputs remain inside the digest. The runner snapshots this file set before starting and fails if a covered file changes during acceptance. After commit, CI computes the same digest from committed files.

The attestation includes:

- schema version;
- source digest and tested Git parent;
- manifest digest;
- headless browser name and version;
- execution mode, seed, start time, duration, and host class;
- packs and capabilities executed;
- action counts and sanitized sequence digest;
- UI, HTTP, and database evidence counts;
- Playwright trace and safe-log hashes;
- cleanup result and exact residual entity IDs;
- explicit unverified capabilities and risk classification;
- final pass or fail status.

Canonical serialization makes the attestation deterministic apart from declared runtime fields. The generator writes no secrets. A secret-pattern scan runs before the file becomes eligible for commit.

## CI Attestation Validation

Pull-request CI does not start the browser suite or backend dependencies. It performs a fast `System E2E Attestation` validation:

- parse the canonical JSON schema;
- recompute and compare the source digest;
- recompute and compare the manifest digest;
- require the short-mode full-system scope for normal feature submissions;
- enforce all required packs and capabilities passed with no unexplained skips;
- enforce minimum action, role, evidence, refresh, and reconciliation counts;
- require soak evidence when repository risk rules classify the change as soak-required;
- verify artifact hashes and absence of credential patterns;
- reject attestations outside the configured development-window policy;
- reject reports whose cleanup or residual-data declaration is inconsistent.

The CI job name is stable so branch protection can require it. The final build job depends on the validator, but no CI browser execution is introduced.

CI validation proves that a structurally valid report and evidence hashes correspond to the current source tree. It cannot independently prove that a developer did not fabricate the report because it does not repeat execution. The design therefore supports an attestation-signature field and public-key verification. Initial enforcement uses source and artifact digests plus signed Git commits when available; organization-managed acceptance signing can become mandatory without changing the report schema.

## Developer Workflow Integration

The acceptance commands integrate with repository development closeout:

1. risk rules determine whether short acceptance alone or short plus soak is required;
2. the developer or coding agent runs the required command after implementation and ordinary tests pass;
3. the command snapshots the completed implementation, permits its expected pre-commit dirty state, refuses covered source changes during execution, and invalidates an older report when source changes;
4. the generated attestation is reviewed and committed with the implementation;
5. pre-push or PR tooling checks for a matching passing attestation;
6. CI independently validates the committed report against the submitted source.

Documentation states that any source change after acceptance, including test changes, invalidates the digest and requires a new browser run. Formatting only the generated attestation does not change the source digest, but manual attestation edits break canonical or artifact-hash validation.

## Skill Integration

The project-local `stratum-e2e-development` skill is the mandatory orchestration entry for feature development, bug fixes, and release acceptance. Its installed copy currently lives under the ignored `.agents/` directory, so the implementation updates that local skill and places its enforceable command contract in tracked repository scripts, generated agent instructions, and tests. The skill no longer treats a manually selected browser scenario as sufficient system acceptance.

The skill must:

- inspect the current diff and coverage manifest after ordinary implementation tests pass;
- classify whether short full-system acceptance, impacted-pack soak, or full-system release soak is required;
- invoke the repository acceptance command rather than inventing ad hoc browser scripts;
- require headless Chromium user actions for all selected capabilities;
- permit database credential discovery only within the defined test-only secret boundary;
- monitor the runner through completion and continue root-cause analysis on failure;
- refuse a completion claim when required packs are skipped, evidence does not reconcile, cleanup fails, or the attestation does not match current source;
- summarize the attestation, residual data, unverified capabilities, seed, and safe evidence paths in the final report;
- stop services it started and avoid printing credentials or raw sensitive artifacts.

The skill remains local policy and orchestration. Deterministic action selection, browser execution, manifest validation, digest calculation, attestation generation, secret scanning, and CI validation live in versioned repository code with automated tests. The tracked agent instructions name the canonical commands and completion requirements, while the ignored installed skill supplies the detailed operating procedure. This separation prevents prompt wording from becoming the only enforcement mechanism and gives local developers, coding agents, hooks, and CI one shared implementation.

The skill documents the canonical commands and completion states. Temporary one-off Playwright specifications remain allowed only for diagnosis; they cannot satisfy the required stateful acceptance or produce a valid full-system attestation.

## Environment and Cleanup

The local harness starts or validates PostgreSQL, PgBouncer, Redis, NATS, Milvus, MCP fixtures, backend, and Vite according to selected pack requirements. It waits for bounded health and readiness checks and records which processes it owns.

Processes started by the harness stop in reverse dependency order. Pre-existing services remain running. Temporary files use `tmp-` prefixes and are removed before success. Test data uses `E2E-STATEFUL-<run-id>-` names and exact tracked IDs. Cleanup never truncates tenant data, drops schemas or collections, or uses wildcard deletion.

If product APIs do not support safe cleanup, the suite records exact retained rows for the guest reaper instead of issuing direct destructive SQL. Cleanup failure makes acceptance fail unless the manifest explicitly defines a non-destructive retained-data policy for that capability.

## Failure Evidence

On failure the runner preserves a secret-scanned evidence bundle containing:

- seed and completed action prefix;
- Playwright trace with sensitive headers and cookies removed;
- sanitized action log;
- expected and observed state summaries;
- HTTP method, route template, and status only;
- database invariant names and non-sensitive entity IDs;
- backend error-event names without raw sensitive payloads.

Screenshots remain disabled by default. Web-first DOM assertions, HTTP evidence, database reconciliation, and Playwright traces are the primary evidence.

## Acceptance Criteria

Implementation is complete when:

- every managed product domain has a headless-browser stateful pack;
- required cross-domain journeys execute through headless Chromium;
- all primary CRUD and operational actions are triggered through the UI;
- separate browser contexts prove administrator and member isolation;
- test-only database credential discovery follows the stated secret boundaries;
- every mutation produces matching UI, HTTP, and database evidence;
- stateful-core tests prove deterministic replay and valid action selection;
- short full-system acceptance completes within the supported ten-minute target;
- a configurable 60-minute full-system soak completes and emits a passing attestation;
- coverage-manifest validation detects an intentionally unmapped route or action;
- any tracked source change invalidates the previous attestation;
- CI validates the attestation without starting browser or backend services;
- `stratum-e2e-development` invokes and enforces the same repository acceptance and attestation workflow;
- failure evidence reproduces the seed and contains no credential material;
- all owned processes stop and cleanup is exact and auditable.

## Residual Boundaries

Passing acceptance demonstrates the covered state model and evidence rules, not exhaustive state-space correctness. Chromium is the initial browser engine. External provider reliability, production data scale, multi-region behavior, network fault injection, and performance saturation require specialized validation. CI report validation is an integrity and coverage check, not independent proof of runtime execution until organization-managed signing is enforced.
