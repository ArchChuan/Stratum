# Unified Agent Verification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Evolve the existing `stratum-e2e-development` skill into the single CI-authoritative verification workflow for Claude Code, Codex, and future coding agents.

**Architecture:** Keep runner lifecycle, leases, fixtures, concurrency, cleanup, and attestation implementation in the Task 1-10 verification kernel. Add one repository manifest and make the existing skill orchestrate classification, planning, reviews, runner execution, CI observation, attestation validation, and the final completion report. Agent-specific files remain thin entry points.

**Tech Stack:** Bash, Go, YAML, JSON Schema, Playwright, GitHub Actions, existing `cmd/e2e-attestation`, existing `scripts/e2e/system-stateful.sh`, Claude Code hooks, Codex `AGENTS.md`/hooks/`codex exec`.

---

## Dependency Boundary

The concurrent runner work in the other session owns Tasks 1-10 and must land its
stable interfaces before Tasks 4-6 below modify the skill contract. Do not edit
its in-progress files from this plan. The integration boundary is the runner
invocation contract, attestation v2 schema, cleanup result, and run identity.

## File Map

- Create: `.test/verification.yaml` - repository verification policy and risk rules.
- Create: `.test/schemas/verification-plan.schema.json` - plan output contract.
- Create: `.test/schemas/completion-report.schema.json` - terminal report contract.
- Modify: `.agents/skills/stratum-e2e-development/SKILL.md` - single workflow entry point and state machine.
- Create: `.agents/skills/stratum-e2e-development/references/verification-manifest.md` - manifest semantics.
- Create: `.agents/skills/stratum-e2e-development/references/review-contract.md` - independent review rules.
- Create: `.agents/skills/stratum-e2e-development/references/failure-taxonomy.md` - product/fixture/environment/assertion/flaky classification.
- Create: `.agents/skills/stratum-e2e-development/references/agent-adapters.md` - Claude Code and Codex entry-point rules.
- Create: `scripts/quality/verification-manifest-test.sh` - deterministic manifest guard.
- Create: `scripts/quality/verification-schemas-test.sh` - schema and fixture guard.
- Modify: `scripts/quality/system-e2e-instructions-test.sh` - assert the unified skill and canonical commands.
- Modify: `AGENTS.md` generated source under `docs/agent/` only through the project generation workflow; do not hand-edit generated `AGENTS.md`.
- Modify: `.github/workflows/ci.yml` - invoke CI-authoritative verification and attestation checks after existing required jobs.
- Modify: `.github/workflows/stateful-e2e.yml` - consume runner contract and expose attestation artifact metadata.
- Modify: `Makefile` - add canonical `test-verify-*` targets as thin wrappers.
- Test: `test/e2e/attestations/` fixtures and existing `cmd/e2e-attestation` tests; only add fixtures after the Task 1-10 schema is final.

### Task 1: Freeze the runner integration contract

**Files:**

- Read only: the Task 1-10 branch files for run-scope, leases, lifecycle, fixture addresses, and attestation v2.
- Test: the other session's runner contract and concurrency tests.

- [ ] **Step 1: Record the interfaces from the completed Task 2-10 work**

Capture the exact command, environment variables, JSON fields, exit codes, and
cleanup semantics in a short review note. Do not invent compatibility aliases.

- [ ] **Step 2: Verify the atomic lease publication contract**

Run the other session's focused tests, race repetition, `go vet`, and
`make risk-guardrails`. Expected: no partially published lease is observable and
the runner fails closed when registry publication is unavailable.

- [ ] **Step 3: Freeze the interface in a review commit**

The review must list the runner identity, lease ownership, cleanup result, and
attestation v2 fields that the skill will consume. This is a prerequisite for
all following tasks.

### Task 2: Add the repository verification manifest

**Files:**

- Create: `.test/verification.yaml`
- Create: `scripts/quality/verification-manifest-test.sh`
- Modify: `Makefile`

- [ ] **Step 1: Write failing manifest guard cases**

The shell test must reject missing `version`, `policy.authority`,
`policy.fail_closed`, R0-R4 levels, silent capability skip policy, and a risk
rule without an explicit level. It must accept the checked-in manifest.

- [ ] **Step 2: Run the guard and verify it fails before the manifest exists**

Run:

```bash
bash scripts/quality/verification-manifest-test.sh
```

Expected: failure identifying the missing `.test/verification.yaml`.

- [ ] **Step 3: Add the minimal manifest**

Define the R0-R4 matrix, Stratum rules for tenant/auth/migration/Agent/MCP/
Memory/external dependency/deployment changes, capability IDs, required
evidence, review requirements, and attestation schema `2`.

- [ ] **Step 4: Make the guard pass**

Run the same command. Expected: `verification manifest tests passed`.

- [ ] **Step 5: Add thin Make targets**

Add `test-verify-plan`, `test-verify-local`, `test-verify-ci`,
`test-verify-attestation`, and `test-verify-report` targets. Each target must
delegate to an existing script or the Task 1-10 runner; no target may contain
business classification logic.

- [ ] **Step 6: Commit the manifest contract**

```bash
git add .test/verification.yaml scripts/quality/verification-manifest-test.sh Makefile
git commit -m "feat(testing): add verification manifest contract"
```

### Task 3: Add structured plan and completion schemas

**Files:**

- Create: `.test/schemas/verification-plan.schema.json`
- Create: `.test/schemas/completion-report.schema.json`
- Create: `scripts/quality/verification-schemas-test.sh`

- [ ] **Step 1: Write failing schema fixtures**

Cover a valid R3 plan, a report with `accepted`, a report with `blocked`, and
invalid reports containing skipped capabilities, missing commit, missing
manifest digest, incomplete cleanup, or an unverified attestation.

- [ ] **Step 2: Run the schema guard before schemas exist**

Run:

```bash
bash scripts/quality/verification-schemas-test.sh
```

Expected: failure for missing schema files.

- [ ] **Step 3: Define the schemas**

Require commit, manifest digest, risk level, mode, review results, capability
counts, attestation status, cleanup status, and immutable artifact digest for an
accepted report. Enumerate terminal statuses as `accepted`, `failed`, `blocked`,
and `incomplete`.

- [ ] **Step 4: Run the schema guard and commit**

```bash
bash scripts/quality/verification-schemas-test.sh
git add .test/schemas scripts/quality/verification-schemas-test.sh
git commit -m "test(testing): define verification evidence schemas"
```

### Task 4: Evolve `stratum-e2e-development` in place

**Files:**

- Modify: `.agents/skills/stratum-e2e-development/SKILL.md`
- Create: `.agents/skills/stratum-e2e-development/references/verification-manifest.md`
- Create: `.agents/skills/stratum-e2e-development/references/review-contract.md`
- Create: `.agents/skills/stratum-e2e-development/references/failure-taxonomy.md`
- Create: `.agents/skills/stratum-e2e-development/references/agent-adapters.md`
- Modify: `scripts/quality/system-e2e-instructions-test.sh`

- [ ] **Step 1: Add failing skill-contract assertions**

Extend the quality test to require the existing skill name, the manifest path,
R0-R4 risk levels, `received` through `accepted` state transitions, CI-only
authority, retry-for-diagnostics semantics, review requirements, and the
Task 1-10 runner boundary.

- [ ] **Step 2: Run the contract test and verify missing sections fail**

```bash
bash scripts/quality/system-e2e-instructions-test.sh
```

Expected: failure naming the missing unified workflow clauses.

- [ ] **Step 3: Update the existing skill without creating a second skill**

Keep current browser, API, database, cleanup, short/soak/release-soak, and
attestation guidance. Add the manifest/state machine/review/failure sections
and state explicitly that the skill orchestrates but CI attestation decides.

- [ ] **Step 4: Add the four focused references**

Keep each reference executable: include the exact fields, commands, terminal
statuses, and failure examples that an Agent must use. Do not duplicate the
whole skill or runner implementation.

- [ ] **Step 5: Make the contract test pass and commit**

```bash
bash scripts/quality/system-e2e-instructions-test.sh
git add .agents/skills/stratum-e2e-development scripts/quality/system-e2e-instructions-test.sh
git commit -m "feat(testing): evolve e2e skill into unified acceptance workflow"
```

### Task 5: Add Claude Code and Codex thin adapters

**Files:**

- Modify: `AGENTS.md` only through its generated source workflow.
- Modify: `docs/agent/instructions.md` and the relevant Claude project instruction source.
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/stateful-e2e.yml`

- [ ] **Step 1: Write adapter contract tests**

Assert both instruction surfaces name only `stratum-e2e-development`, point to
the same manifest and canonical commands, and forbid local completion claims
without CI attestation. Assert CI jobs fail when the required verification job
or attestation is absent.

- [ ] **Step 2: Add the shared entry-point instructions**

Keep Claude and Codex wording tool-specific only where necessary. The risk
matrix and evidence semantics must remain in the skill and manifest.

- [ ] **Step 3: Add CI invocation and artifact wiring**

Make CI invoke the canonical plan/verification/attestation checks, upload raw
logs as short-lived artifacts, and expose only the structured report and signed
attestation as durable evidence.

- [ ] **Step 4: Run adapter and workflow guards**

```bash
bash scripts/quality/system-e2e-instructions-test.sh
bash scripts/quality/e2e-attestation-test.sh
bash scripts/quality/risk-regression-guard.sh --all
```

- [ ] **Step 5: Commit adapter integration**

```bash
git add docs/agent .github/workflows scripts/quality
git commit -m "ci(testing): route agent verification through unified skill"
```

### Task 6: Validate concurrent acceptance and release promotion

**Files:**

- Read only: Task 1-10 runner and attestation implementation.
- Modify only the final acceptance fixtures or workflow files required by the
  frozen runner contract.

- [ ] **Step 1: Run focused single-runner verification**

Run the Task 1-10 focused tests, then the platform assistant browser and
stateful short journeys. Expected: UI, HTTP, database, trace, and cleanup
evidence all reconcile.

- [ ] **Step 2: Run the dual-runner contract**

Start two isolated runs concurrently with different run identities and dynamic
ports. Expected: neither run can reclaim or observe the other's lease, fixture,
URL, database, or cleanup record.

- [ ] **Step 3: Run the failure matrix**

Terminate a runner, force a timeout, cause a port collision, and make a
dependency fail readiness. Expected: explicit `failed` or `blocked`, no false
success, safe scoped cleanup, and a complete diagnostic record.

- [ ] **Step 4: Run the required soak profiles**

```bash
STATEFUL_E2E_PROFILE=test STATEFUL_E2E_DURATION_SEC=600 \
  STATEFUL_E2E_PACKS=all make e2e-system-soak
make e2e-system-release-soak
```

- [ ] **Step 5: Verify attestation and same-digest promotion**

Run `make e2e-attestation-check`, then verify the build, test, staging, and
production records reference the same immutable artifact digest.

- [ ] **Step 6: Produce the final report and commit acceptance fixtures**

The report must include mode, seed, pack/capability counts, attestation path,
cleanup result, residual entities, and unverified risks. Do not print tokens,
cookies, keys, passwords, or raw sensitive responses.

## Verification Commands

Before any completion claim, run the proportionate checks:

```bash
bash scripts/quality/risk-regression-guard.sh --explain
make risk-guardrails
go vet ./...
go test -short ./...
bash scripts/quality/system-e2e-instructions-test.sh
bash scripts/quality/e2e-attestation-test.sh
```

For R2/R3/R4 changes also run the required short/soak profile. For R4, run the
release soak and deployment verification. If a command is skipped, report the
exact reason and do not mark the task `accepted`.

## Plan Self-review

- Spec sections map to Tasks 1-6: runner boundary, risk matrix, manifest,
  state machine, reviews, adapters, release chain, and phased delivery.
- No second skill or central service is introduced.
- Task 2 atomic publication is an explicit prerequisite.
- All implementation steps contain concrete paths, commands, and expected outcomes.
- The plan does not modify the other session's in-progress runner files.
