# Local E2E and CI Verification Design

## Status

Approved design for implementation planning.

## Context

Stratum has a mature stateful browser runner with isolated run scopes, dynamic ports, per-run PostgreSQL databases,
capability evidence, cleanup leases, and schema-v2 attestations. The repository also has a parallel GitHub Actions CI
pipeline for static checks, unit tests, integration tests, contract tests, security checks, and builds.

The verification model became internally inconsistent after the browser and release verification workflows were
removed from GitHub Actions:

- `.agents/skills/stratum-e2e-development/SKILL.md` still says CI is the browser E2E acceptance authority.
- `.test/verification.yaml` still assigns browser short, soak, reviews, and release soak to one shared check set.
- GitHub Actions no longer runs headless browser short or soak verification.
- Local browser evidence cannot be treated as GitHub OIDC/Sigstore authority.
- Deployment currently consumes ordinary CI success rather than a browser acceptance report.

This design resolves the conflict by assigning separate authorities to local browser verification, PR merge checks,
and deployment verification.

## Goals

1. Keep `stratum-e2e-development` as the only Test Skill used by Claude Code, Codex, and human developers.
2. Run real headless-browser E2E before creating a PR, not in PR CI, merge queues, or deployment workflows.
3. Preserve real UI, HTTP, database, trace, capability reconciliation, run isolation, and cleanup evidence.
4. Keep PR CI deterministic, parallel, and reasonably fast.
5. Make stale local E2E evidence explicit when source changes.
6. Bind deployment to the exact CI-tested commit and immutable application image digests.
7. Remove unused trust machinery rather than leaving unreachable or misleading acceptance states.

## Non-Goals

- GitHub does not cryptographically prove that a developer ran local browser E2E.
- Browser E2E is not restored to GitHub Actions.
- A local report is not a merge approval, release approval, or Sigstore identity.
- This work does not create an external E2E orchestration platform.
- This work does not redesign product capabilities or browser pack contents unless a contract defect is found.

## Trust Boundaries

Verification has three independent authorities:

```yaml
policy:
  browser_e2e_authority: local
  merge_authority: ci
  deployment_authority: release_pipeline
```

### Local browser authority

The repository runner proves that the checked-out source completed the selected browser journey and cleanup contract.
The report is a developer assertion backed by reproducible local evidence. It is useful for engineering discipline and
audit history, but GitHub does not treat it as a trusted status check.

### CI merge authority

GitHub Actions decides whether a commit may merge based on deterministic L0-L2 checks. CI does not consume, sign, or
validate local browser reports.

### Deployment authority

The release pipeline decides whether a CI-tested commit and its immutable image digests may deploy. It verifies build,
migration, deployment, readiness, health, and rollback contracts without running a browser.

## Verification Layers

| Layer | Location | Trigger | Coverage | Blocks |
|---|---|---|---|---|
| L0 | Local and CI | Every change | Format, static analysis, generated files, code quality | Commit and PR |
| L1 | Local and CI | Every change | Unit and domain behavior | PR |
| L2 | Local and CI | Matching paths | PostgreSQL, HTTP, contracts, failure paths, security | PR |
| L3 | Local | Before PR for R2+ | Headless browser short, UI to HTTP to DB | Developer handoff |
| L4 | Local | Before PR for R3+ | 600-second all-pack soak and cleanup reconciliation | Developer handoff |
| L5 | Release pipeline | Deployment | Digests, migration, rollout, health, rollback | Deployment |

The release profile remains available as an explicit local command for planned releases, but it is not a GitHub
browser gate and must not be described as one.

## Risk Model

The planner remains deterministic and may only increase risk.

| Risk | Local checks | CI checks |
|---|---|---|
| R0 | Documentation consistency | Documentation and generated-file checks |
| R1 | Focused unit, static, build | Static, unit, build, code quality |
| R2 | R1 plus integration, contract, browser short | Static, unit, integration, contract, build |
| R3 | R2 plus failure paths and 600-second browser soak | R2 CI plus security and risk guardrails |
| R4 | R3 plus explicit local release soak when requested | R3 CI; release pipeline adds L5 checks |

`.test/verification.yaml` must represent local and CI checks separately. A check identifier must have one execution
owner. The manifest must not claim that CI runs browser checks.

## State Model

The overloaded `accepted` status is replaced by three independent records.

### Local verification

```yaml
version: 1
status: passed | failed | not_run | stale | infra_failed
mode: none | short | soak | release-soak
tested_commit: <git-sha>
manifest_digest: sha256:<digest>
capabilities:
  passed: 0
  failed: 0
  blocked: 0
  skipped: 0
  unreconciled: 0
cleanup:
  complete: false
  residual_entities: 0
attestation_path: <repository-relative-path-or-empty>
```

`passed` requires a clean source snapshot and the selected capability set to have zero failed, skipped, blocked, and
unreconciled results. Cleanup must be complete and residual entities must be declared. `infra_failed` is distinct from
a product failure.

### CI verification

```yaml
version: 1
status: passed | failed | pending
commit: <git-sha>
checks:
  static: passed
  unit: passed
  integration: passed
  contract: passed
  build: passed
  security: passed
```

This record is represented by GitHub status checks. The repository does not need to persist a second synthetic receipt.

### Release verification

```yaml
version: 1
status: deployable | blocked | deployed
commit: <git-sha>
artifact_digests: [sha256:<digest>]
migration_check: passed
health_check: passed
rollback_check: passed
```

The deployment receipt records digests observed from the deployed workloads, not manually entered values.

## Local Before-PR Workflow

The canonical command is:

```bash
make test-verify-before-pr
```

It performs these deterministic stages:

```text
plan risk
  -> run focused local checks
  -> start isolated E2E infrastructure
  -> run browser short for R2+
  -> run 600-second soak for R3+
  -> verify attestation and cleanup
  -> write local verification report atomically
  -> verify report freshness against HEAD and manifest digest
  -> stop owned infrastructure
```

Development commands remain available for iteration:

```bash
make test-verify-fast
make e2e-system-short
make e2e-system-soak
make e2e-system-release-soak
```

Only `test-verify-before-pr` writes the final local report. It may record a failed terminal outcome from a dirty source
snapshot, but it may produce `passed` only when tracked files and relevant untracked source files are clean. A new
commit, subsequent tracked change, relevant untracked source file, or manifest change makes a passed report `stale`.
Runtime evidence under the versioned attestation output directory is excluded from source-change classification.

The command must preserve the first failure in run history. A diagnostic rerun cannot overwrite failure evidence.

## E2E Runner Contract

The current E2E infrastructure remains the versioned verification kernel:

- secure run-scope and lease registry;
- dynamic loopback ports;
- per-run PostgreSQL database identity;
- shared infrastructure reference lifecycle;
- fixture URL propagation;
- schema-v2 topology and cleanup attestation;
- headless Chromium operations;
- UI, HTTP, database, trace, and cleanup evidence reconciliation;
- reverse-order shutdown and residual reporting.

The runner must never depend on a developer's unrelated PostgreSQL container or terminate processes it did not start.
All credentials remain process-local and must not appear in reports or logs.

## PR Collaboration

The PR template includes a developer-provided summary:

```text
Local E2E: passed | not_run
Mode: short | soak | release-soak
Tested commit: <sha>
Capabilities: <passed>/<selected>
Cleanup: complete | incomplete
```

This is an audit hint, not a required GitHub status or cryptographic proof. CI does not download local artifacts. Reviewers
may ask the author to rerun local E2E when the summary is missing, stale, or inconsistent with the change risk.

## CI Design

The parallel structure introduced by commit `ce6c3068` is retained:

- Static Checks;
- Code Quality;
- Frontend Lint and Build;
- Safety Guards;
- Test;
- Integration and domain-specific E2E tests that do not use a browser;
- Contract Goldens;
- Security Scan;
- Build.

The `Migration Guardrails` compatibility check may aggregate parallel jobs, but it must fail closed. It passes only when
every required `needs.<job>.result` is `success`; `failure`, `cancelled`, and unexpected `skipped` results block it.

A CI contract test maps every manifest CI check identifier to an actual workflow job. It also rejects browser check IDs
owned by CI and rejects Test Skill text claiming that CI runs browser E2E.

## Release Pipeline

The release pipeline consumes the exact successful CI commit. For `workflow_run`, candidate identity comes from
`github.event.workflow_run.head_sha`, not the workflow file checkout's default `github.sha` assumption. Automatic
deployment is allowed only when that SHA is still the current `main` tip; an older successful run is recorded as
superseded and must not deploy after `main` advances.

Release verification includes:

1. Confirm the triggering CI workflow succeeded and its head SHA equals the current `main` tip.
2. Checkout and build that exact commit.
3. Resolve immutable backend, frontend, Platform MCP, and adapter image digests.
4. Validate Helm rendering, tenant migration compatibility, and deployment safety.
5. Deploy pinned digests.
6. Verify rollout, readiness, health, and critical dependency connectivity.
7. Prove rollback behavior or preserve the prior digest set needed for rollback.
8. Record actual cluster image digests and terminal deployment status.

Browser tests are not part of this pipeline.

## Failure Semantics

- Product assertion failures produce `failed`.
- Infrastructure startup, dependency availability, or runner ownership failures produce `infra_failed`.
- Source or manifest changes after a pass produce `stale`.
- A soak failure requires a complete soak rerun because duration is part of the contract.
- Failed capability, skipped capability, unreconciled evidence, cleanup failure, and undeclared residual entities are
  blocking local outcomes.
- CI retries operate per job. Retrying a failed CI job does not alter local browser history.
- Deployment failures remain visible and must not be converted to success by diagnostic steps.

## Repository Changes

### Keep

- `.agents/skills/stratum-e2e-development/`
- `.test/verification.yaml`
- `cmd/verification-plan`
- `cmd/e2e-run-scope`
- `cmd/e2e-attestation`
- `internal/platform/e2erunscope`
- `internal/platform/e2eattestation`
- `scripts/e2e/system-stateful.sh`
- short, soak, release-soak, attestation, and cleanup make targets

### Add or replace

- `.test/schemas/local-verification.schema.json`
- `.test/schemas/ci-verification.schema.json`
- `.test/schemas/release-verification.schema.json`
- `scripts/quality/write-local-verification-report.sh`
- `scripts/quality/check-local-verification-report.sh`
- focused behavior tests for report writing and freshness
- a manifest-to-CI workflow contract test
- PR template local E2E summary fields

### Remove after references reach zero

- commit-bound fake review receipt generation;
- planned-check receipt generation that has no CI consumer;
- Sigstore signature receipt code for browser E2E;
- completion report fields that mix local, CI, and release authority;
- references to deleted browser and release workflows;
- documentation that claims CI runs headless browser E2E.

Removal must be reference-driven and covered by contract tests. The stateful runner and schema-v2 evidence are not
removed.

## Delivery Phases

### Phase 1: Reconcile the source of truth

- Separate local and CI checks in the manifest.
- Update the Test Skill, agent instructions, and verification ADR.
- Define the three schemas and reject the old mixed authority model.
- Add consistency tests before deleting obsolete code.

### Phase 2: Build the local before-PR entrypoint

- Add `test-verify-fast` and `test-verify-before-pr` orchestration.
- Generate the local report atomically.
- Detect stale source and manifest state.
- Preserve run history, cleanup proof, and explicit infrastructure failures.

### Phase 3: Align CI

- Retain parallel CI jobs.
- Add manifest-to-job mapping validation.
- Make the required aggregate job fail closed.
- Remove browser, Sigstore, and fake review receipt assumptions from CI-facing contracts.

### Phase 4: Align deployment

- Bind workflow-run deployment to the triggering CI head SHA.
- Validate immutable digests, migration, health, and rollback evidence.
- Produce a release verification record without browser fields.

### Phase 5: Acceptance and cleanup

- Exercise one real R2 change through browser short and PR CI.
- Exercise one real R3 change through short, 600-second soak, and PR CI.
- Prove reports become stale after source and manifest changes.
- Prove CI never starts Chromium.
- Prove deployment rejects a stale or mismatched commit.
- Remove obsolete schemas, scripts, tests, and documentation only after all references reach zero.

## Acceptance Criteria

1. `make test-verify-before-pr` selects short for R2 and soak for R3 without manual flags.
2. All browser execution is headless and local to the developer workflow.
3. A passed local report is bound to current HEAD and manifest digest.
4. Source or manifest changes make the report stale.
5. Failed, skipped, blocked, or unreconciled capabilities cannot produce a passed local report.
6. Cleanup failure or residual entities cannot produce a passed local report.
7. PR CI contains no Playwright browser installation or browser E2E job.
8. Every manifest CI check maps to a real fail-closed CI job.
9. The required aggregate check rejects failed, cancelled, or unexpected skipped dependencies.
10. Deployment checks out the successful CI workflow's exact head SHA.
11. Deployment records immutable digests observed from the cluster.
12. The repository contains one Test Skill and no contradictory CI-browser authority claims.
13. Automatic deployment rejects a successful CI run whose head SHA is no longer the current `main` tip.

## Evidence and External Constraints

Repository code, tests, Git history, workflow runs, branch protection, and environment configuration are the primary
facts for this design. GitHub's merge checks, reusable workflow, environment, and artifact attestation mechanisms are
external control-plane capabilities; this design intentionally does not use artifact attestations to establish trust in
local browser evidence.

The required Obsidian MCP was not available in the current tool environment. No Obsidian claim was used as evidence,
and this limitation must remain visible during implementation planning.
