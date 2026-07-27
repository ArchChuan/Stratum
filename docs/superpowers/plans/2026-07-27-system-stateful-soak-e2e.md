# System Stateful and Soak Browser E2E Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a local, headless-Chromium system acceptance workflow covering every Stratum domain, emitting a source-bound attestation that pull-request CI validates without running browsers.

**Architecture:** A TypeScript stateful core drives domain-specific Playwright packs through isolated browser contexts and reconciles UI, HTTP, and PostgreSQL evidence. A tracked capability manifest defines coverage, a Go validator computes source/manifest digests and validates canonical attestations, shell entrypoints own local services, and the project-local `stratum-e2e-development` skill orchestrates the same commands.

**Tech Stack:** Playwright 1.61, TypeScript 5.6, Node.js 20, React/Vite, Go 1.25, PostgreSQL 16, Docker Compose, Bash, GitHub Actions.

---

## File Structure

- `test/e2e/stateful/manifest.json`: auditable capability-to-action/evidence mapping for every product domain.
- `test/e2e/stateful/attestation.schema.json`: canonical attestation contract shared by local generation and CI validation.
- `test/e2e/attestations/.gitkeep`: tracked output directory; generated JSON reports are committed deliberately.
- `cmd/e2e-attestation/main.go`: `digest`, `generate`, and `verify` commands.
- `internal/platform/e2eattestation/`: manifest parsing, source digest, canonical report validation, and secret scanning.
- `scripts/e2e/system-stateful.sh`: local short/soak environment owner and browser runner.
- `scripts/e2e/system-stateful-test.sh`: hermetic shell contract tests.
- `web/e2e/stateful/core/`: seeded scheduler, model, actors, evidence, database, and attestation client.
- `web/e2e/stateful/packs/`: IAM, Agent, Skill, MCP, Knowledge, Memory, Evaluation, Workflow, and cross-domain packs.
- `web/e2e/system-stateful.spec.ts`: Playwright pack dispatcher.
- `web/playwright.stateful.config.ts`: headless-only configuration and safe artifact policy.
- `.github/workflows/ci.yml`: attestation validation only.
- `docs/agent/instructions.md`: tracked developer/agent completion contract.
- `/home/yang/go-projects/stratum/.agents/skills/stratum-e2e-development/SKILL.md`: ignored installed skill orchestration update.

### Task 1: Define and Validate the Coverage Manifest

**Files:**

- Create: `test/e2e/stateful/manifest.json`
- Create: `internal/platform/e2eattestation/manifest.go`
- Create: `internal/platform/e2eattestation/manifest_test.go`
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/services/api-paths.ts`

- [ ] **Step 1: Write failing manifest contract tests**

Add table tests that reject duplicate capability IDs, unknown domains, empty browser action IDs, missing HTTP/DB evidence, missing required role coverage, and an intentionally unmapped route or mutating API. The expected public API is:

```go
type Manifest struct {
    Version      int          `json:"version"`
    Capabilities []Capability `json:"capabilities"`
}

func LoadManifest(path string) (Manifest, error)
func ValidateManifest(m Manifest, routes, mutations []string, actions map[string]struct{}) error
```

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/platform/e2eattestation -run Manifest -count=1`

Expected: FAIL because `LoadManifest` and `ValidateManifest` do not exist.

- [ ] **Step 3: Implement the parser and complete initial manifest**

Use `encoding/json` with `DisallowUnknownFields`. Export route and mutating API registries from the frontend as static string arrays so the checker does not parse TypeScript source text. Populate capabilities for all routes and writes currently registered under IAM, Agent, Skill, MCP, Knowledge, Memory, Evaluation, and Workflow. Each entry must name one of `short`, `soak`, or `lower_layer` and include a non-empty lower-layer justification only for the last choice.

- [ ] **Step 4: Verify GREEN**

Run: `go test ./internal/platform/e2eattestation -run Manifest -count=1`

Expected: PASS, including the fixture proving an unmapped route fails.

- [ ] **Step 5: Commit**

```bash
git add test/e2e/stateful web/src/app/router.tsx web/src/services/api-paths.ts internal/platform/e2eattestation
git commit -m "[test](e2e): define system capability manifest"
```

### Task 2: Build the Deterministic Stateful Core

**Files:**

- Create: `web/e2e/stateful/core/types.ts`
- Create: `web/e2e/stateful/core/random.ts`
- Create: `web/e2e/stateful/core/scheduler.ts`
- Create: `web/e2e/stateful/core/model.ts`
- Create: `web/e2e/stateful/core/scheduler.test.ts`
- Create: `web/e2e/stateful/core/model.test.ts`

- [ ] **Step 1: Write failing scheduler and model tests**

Define tests proving identical seeds produce identical enabled-action sequences, disabled actions are never selected, duration/cycle exhaustion stops selection, stale revisions are rejected, and one actor cannot acquire another actor's run.

```ts
export interface StatefulAction<M, C> {
  id: string;
  enabled(model: Readonly<M>): boolean;
  run(context: C, model: Readonly<M>): Promise<ActionResult<M>>;
}

export interface ActionResult<M> {
  next: M;
  evidence: EvidenceSummary;
}
```

- [ ] **Step 2: Verify RED**

Run: `npx --prefix web vitest run e2e/stateful/core/scheduler.test.ts e2e/stateful/core/model.test.ts`

Expected: FAIL because the stateful modules do not exist.

- [ ] **Step 3: Implement minimal deterministic core**

Implement a small integer PRNG with explicit uint32 normalization, immutable model transitions, a scheduler that filters enabled actions before selection, and a budget checked only between atomic actions. Do not store browser contexts, tokens, cookies, or credentials in the model.

- [ ] **Step 4: Verify GREEN and refactor**

Run the Step 2 command twice with the same seed and assert identical sequence snapshots.

Expected: PASS with no credential-like data in snapshots.

- [ ] **Step 5: Commit**

```bash
git add web/e2e/stateful/core
git commit -m "[test](e2e): add deterministic stateful core"
```

### Task 3: Add Secure Actors, Database Reconciliation, and Evidence

**Files:**

- Create: `web/e2e/stateful/core/actors.ts`
- Create: `web/e2e/stateful/core/database.ts`
- Create: `web/e2e/stateful/core/evidence.ts`
- Create: `web/e2e/stateful/core/redaction.ts`
- Create: `web/e2e/stateful/core/redaction.test.ts`
- Modify: `web/package.json`
- Modify: `web/package-lock.json`

- [ ] **Step 1: Write failing secret and isolation tests**

Test that serialization removes `authorization`, cookie values, passwords, private keys, and API keys; rejects a production-like database URL; validates UUID parameters; and creates a distinct context ID for `systemAdmin`, `tenantAdmin`, `memberA`, and `memberB`.

- [ ] **Step 2: Verify RED**

Run: `npx --prefix web vitest run e2e/stateful/core/redaction.test.ts`

Expected: FAIL because redaction and database guards are missing.

- [ ] **Step 3: Implement secure infrastructure**

Add `pg` and `@types/pg` as development dependencies. Use parameterized SQL exclusively. Implement `createGuestActor`, exact tenant/global role elevation for generated users, real `/auth/refresh`, and separate Playwright contexts. Provide query helpers that require a validated tenant UUID and execute `SET LOCAL search_path` inside a transaction.

```ts
await client.query('BEGIN');
await client.query(`SELECT set_config('search_path', $1, true)`, [`tenant_${tenantId},public`]);
const result = await client.query(query.text, query.values);
await client.query('ROLLBACK');
```

- [ ] **Step 4: Verify GREEN**

Run: `npx --prefix web vitest run e2e/stateful/core/redaction.test.ts && npm --prefix web run typecheck`

Expected: PASS; no test output contains fixture secrets.

- [ ] **Step 5: Commit**

```bash
git add web/package.json web/package-lock.json web/e2e/stateful/core
git commit -m "[test](e2e): isolate actors and reconcile evidence"
```

### Task 4: Implement Source-Bound Attestations

**Files:**

- Create: `test/e2e/stateful/attestation.schema.json`
- Create: `test/e2e/attestations/.gitkeep`
- Create: `internal/platform/e2eattestation/digest.go`
- Create: `internal/platform/e2eattestation/attestation.go`
- Create: `internal/platform/e2eattestation/digest_test.go`
- Create: `internal/platform/e2eattestation/attestation_test.go`
- Create: `cmd/e2e-attestation/main.go`

- [ ] **Step 1: Write failing digest and validator tests**

Cover tracked and non-ignored untracked files, path ordering, exclusion of `test/e2e/attestations/`, source mutation invalidation, manifest mismatch, missing pack, skipped capability, failed cleanup, artifact hash mismatch, expired report, and credential-pattern rejection.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/platform/e2eattestation ./cmd/e2e-attestation -count=1`

Expected: FAIL because digest and attestation implementations are missing.

- [ ] **Step 3: Implement canonical generation and verification**

Use SHA-256 over sorted `path NUL content NUL` records. Enumerate local source with `git ls-files --cached --others --exclude-standard`; verify committed source with `git ls-tree -r`. Canonicalize report fields before JSON encoding. Keep runtime timestamps declared but outside deterministic artifact digests. Implement CLI commands:

```text
e2e-attestation digest
e2e-attestation generate --input safe-results.json --output-dir test/e2e/attestations
e2e-attestation verify --attestation test/e2e/attestations/<digest>.json
```

- [ ] **Step 4: Verify GREEN**

Run: `go test ./internal/platform/e2eattestation ./cmd/e2e-attestation -count=1`

Expected: PASS, including source-change invalidation and secret rejection.

- [ ] **Step 5: Commit**

```bash
git add test/e2e internal/platform/e2eattestation cmd/e2e-attestation
git commit -m "[test](e2e): generate source-bound attestations"
```

### Task 5: Build the Local Headless Browser Harness

**Files:**

- Create: `web/playwright.stateful.config.ts`
- Create: `web/e2e/system-stateful.spec.ts`
- Create: `web/e2e/stateful/core/runtime.ts`
- Create: `scripts/e2e/system-stateful.sh`
- Create: `scripts/e2e/system-stateful-test.sh`
- Modify: `Makefile`

- [ ] **Step 1: Write failing shell contract tests**

Test unsupported mode, duration outside 600–14400 seconds, unknown pack, production-like database rejection, backend startup failure propagation, child-process cleanup, source mutation during execution, skipped pack rejection, and safe result forwarding to attestation generation.

- [ ] **Step 2: Verify RED**

Run: `bash scripts/e2e/system-stateful-test.sh`

Expected: FAIL because the runner does not exist.

- [ ] **Step 3: Implement the harness**

Provide `make e2e-system-short` and `make e2e-system-soak`. The shell runner owns only processes it starts, traps `EXIT INT TERM`, starts dependencies through existing Make targets, starts the backend with explicit frontend origin, waits on `/health`, and launches Playwright with `headless: true`, `screenshot: 'off'`, trace on failure, one worker per selected pack, and no browser-network mocks.

- [ ] **Step 4: Verify GREEN**

Run: `bash scripts/e2e/system-stateful-test.sh`

Expected: PASS using fake child processes; no real infrastructure is left running.

- [ ] **Step 5: Commit**

```bash
git add web/playwright.stateful.config.ts web/e2e/system-stateful.spec.ts web/e2e/stateful/core/runtime.ts scripts/e2e Makefile
git commit -m "[test](e2e): add local system browser harness"
```

### Task 6: Add IAM and Workflow Packs

**Files:**

- Create: `web/e2e/stateful/packs/iam.ts`
- Create: `web/e2e/stateful/packs/workflow.ts`
- Create: `web/e2e/stateful/packs/iam.test.ts`
- Create: `web/e2e/stateful/packs/workflow.test.ts`
- Refactor: `web/e2e/support/real-workflow.ts`
- Modify: `test/e2e/stateful/manifest.json`

- [ ] **Step 1: Write failing action-registration tests**

Require IAM actions for login, refresh, tenant selection, membership invite/role/remove, settings update, and denied administration. Require Workflow actions for repeated draft saves, invalid publish gate, validation, publish, version refresh, member run, member-B denial, approval/cancel, and stream reconnect.

- [ ] **Step 2: Verify RED**

Run: `npx --prefix web vitest run e2e/stateful/packs/iam.test.ts e2e/stateful/packs/workflow.test.ts`

Expected: FAIL with missing action IDs referenced by the manifest.

- [ ] **Step 3: Implement browser actions and reconciliation**

Move reusable UUID/session helpers from `real-workflow.ts` into the new core. Trigger primary mutations from UI, wrap each expected response with `page.waitForResponse`, refresh and read back visible state, then query exact public or tenant rows. Use four isolated contexts and assert member-B UI denial plus backend denial.

- [ ] **Step 4: Run real packs**

Run: `STATEFUL_E2E_PACKS=iam,workflow make e2e-system-short`

Expected: PASS with two mutation/read-back cycles per pack and a generated partial-scope diagnostic report. No credentials appear in output.

- [ ] **Step 5: Commit**

```bash
git add web/e2e test/e2e/stateful/manifest.json
git commit -m "[test](e2e): cover IAM and workflow statefully"
```

### Task 7: Add Agent, Skill, MCP, and Their Cross-Domain Pack

**Files:**

- Create: `web/e2e/stateful/packs/agent.ts`
- Create: `web/e2e/stateful/packs/skill.ts`
- Create: `web/e2e/stateful/packs/mcp.ts`
- Create: `web/e2e/stateful/packs/agent-skill-mcp.ts`
- Create: `web/e2e/stateful/packs/agent-skill-mcp.test.ts`
- Create: `test/e2e/fixtures/mcp-stateful-server.go`
- Modify: `test/e2e/stateful/manifest.json`

- [ ] **Step 1: Write failing pack and fixture tests**

Require Agent CRUD/conversation/history actions, Skill draft/edit/publish/standalone-run actions, MCP connect/edit/tools/policy/disconnect actions, and one Agent execution that consumes a published Skill and calls the deterministic MCP fixture.

- [ ] **Step 2: Verify RED**

Run: `npx --prefix web vitest run e2e/stateful/packs/agent-skill-mcp.test.ts`

Expected: FAIL because required actions and fixture are absent.

- [ ] **Step 3: Implement deterministic real-service packs**

Start the MCP fixture as a real HTTP process. Configure it through the MCP UI. Use a repository test provider or deterministic platform assistant path for Agent execution; never call a paid external provider in required short acceptance. Reconcile agents, skill revisions, MCP configs/policies, conversations, executions, and tool traces in the tenant schema.

- [ ] **Step 4: Run real packs**

Run: `STATEFUL_E2E_PACKS=agent,skill,mcp,agent-skill-mcp make e2e-system-short`

Expected: PASS with a real tool result visible in the headless browser and matching execution/tool records.

- [ ] **Step 5: Commit**

```bash
git add web/e2e/stateful test/e2e/fixtures test/e2e/stateful/manifest.json
git commit -m "[test](e2e): cover Agent Skill and MCP statefully"
```

### Task 8: Add Knowledge, Memory, Evaluation, and Remaining Cross-Domain Packs

**Files:**

- Create: `web/e2e/stateful/packs/knowledge.ts`
- Create: `web/e2e/stateful/packs/memory.ts`
- Create: `web/e2e/stateful/packs/evaluation.ts`
- Create: `web/e2e/stateful/packs/agent-context.ts`
- Create: `web/e2e/stateful/packs/evaluation-promotion.ts`
- Create: `web/e2e/stateful/packs/context-evaluation.test.ts`
- Modify: `test/e2e/stateful/manifest.json`

- [ ] **Step 1: Write failing coverage tests**

Require Knowledge workspace/document/query actions, Memory user/admin/clear actions, Evaluation suite/run/candidate/experiment/promote actions, Agent context evidence from Knowledge and Memory, and promotion propagation to the selected Skill or Agent revision.

- [ ] **Step 2: Verify RED**

Run: `npx --prefix web vitest run e2e/stateful/packs/context-evaluation.test.ts`

Expected: FAIL with missing manifest actions.

- [ ] **Step 3: Implement browser packs**

Use small deterministic documents, wait for actual processing terminal states, query through the UI, seed only unavailable external evaluation results through documented test fixtures, and operate all product-visible creation/promotion actions through Chromium. Reconcile workspace/document, memory fact, evaluation suite/run/experiment, candidate, and active revision state.

- [ ] **Step 4: Run real packs**

Run: `STATEFUL_E2E_PACKS=knowledge,memory,evaluation,agent-context,evaluation-promotion make e2e-system-short`

Expected: PASS with no fixed sleeps and all asynchronous jobs at terminal states.

- [ ] **Step 5: Commit**

```bash
git add web/e2e/stateful test/e2e/stateful/manifest.json
git commit -m "[test](e2e): cover context and evaluation statefully"
```

### Task 9: Integrate Attestation Validation into CI and Risk Rules

**Files:**

- Create: `scripts/quality/e2e-attestation-test.sh`
- Modify: `.github/workflows/ci.yml`
- Modify: `scripts/quality/risk-regression-guard.sh`
- Modify: `scripts/quality/risk-regression-guard-test.sh`
- Modify: `Makefile`

- [ ] **Step 1: Write failing guard tests**

Add fixtures proving CI validation rejects absent, stale, partial, failed, secret-bearing, and soak-required-without-soak attestations. Assert `.github/workflows/ci.yml` contains a `System E2E Attestation` job that runs only the verifier and does not install Chromium or start backend services.

- [ ] **Step 2: Verify RED**

Run: `bash scripts/quality/e2e-attestation-test.sh && bash scripts/quality/risk-regression-guard-test.sh`

Expected: FAIL because validation is not wired.

- [ ] **Step 3: Implement CI and risk classification**

Add `make e2e-attestation-check`. Make the CI check invoke the Go verifier against the report matching the recomputed digest. Extend risk explanation output with `short` or `soak` acceptance requirements for authentication, tenant, migration, messaging, vector, external dependency, and cross-domain changes. Do not add Playwright execution to CI.

- [ ] **Step 4: Verify GREEN**

Run: `make e2e-attestation-check && bash scripts/quality/e2e-attestation-test.sh && bash scripts/quality/risk-regression-guard-test.sh`

Expected: PASS for a fixture report and fail closed for every negative fixture.

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/ci.yml scripts/quality Makefile
git commit -m "[ci](e2e): require source-bound acceptance proof"
```

### Task 10: Integrate the Development Skill and Generated Instructions

**Files:**

- Modify: `docs/agent/instructions.md`
- Modify: `docs/agent/templates/agents-prefix.md`
- Modify: `AGENTS.md` through the repository generator
- Modify outside Git: `/home/yang/go-projects/stratum/.agents/skills/stratum-e2e-development/SKILL.md`
- Create: `scripts/quality/system-e2e-instructions-test.sh`

- [ ] **Step 1: Write failing tracked instruction tests**

Assert generated instructions require `make e2e-system-short`, risk-selected soak, a matching attestation, headless Chromium UI actions, database credential secrecy, process cleanup, and explicit residual-risk reporting.

- [ ] **Step 2: Verify RED**

Run: `bash scripts/quality/system-e2e-instructions-test.sh`

Expected: FAIL because current instructions only require ad hoc real-chain verification.

- [ ] **Step 3: Update tracked instructions and installed skill**

Generate `AGENTS.md` from tracked sources. Update the installed skill to classify the diff, invoke the canonical Make targets, monitor to completion, diagnose failures, verify the resulting attestation, and refuse completion when it is stale, partial, skipped, unreconciled, or secret-bearing. Keep diagnostic temporary specs explicitly insufficient for acceptance.

- [ ] **Step 4: Verify GREEN**

Run: `bash scripts/quality/system-e2e-instructions-test.sh && make agent-instructions-check`

Expected: PASS. Manually inspect the ignored skill diff without copying credentials or unrelated local content.

- [ ] **Step 5: Commit tracked changes**

```bash
git add docs/agent AGENTS.md scripts/quality/system-e2e-instructions-test.sh
git commit -m "[docs](e2e): enforce system acceptance workflow"
```

### Task 11: Execute Short and Soak Acceptance, Then Commit the Proof

**Files:**

- Create: `test/e2e/attestations/<source-digest>.json`
- Modify only if a verified defect is found: relevant product or test files

- [ ] **Step 1: Run focused framework verification**

Run:

```bash
go test ./internal/platform/e2eattestation ./cmd/e2e-attestation -count=1
npx --prefix web vitest run e2e/stateful
bash scripts/e2e/system-stateful-test.sh
bash scripts/quality/e2e-attestation-test.sh
```

Expected: all PASS.

- [ ] **Step 2: Run full short acceptance**

Run: `make e2e-system-short`

Expected: all domain and cross-domain packs PASS in headless Chromium, with UI/HTTP/DB reconciliation and no skipped required capability.

- [ ] **Step 3: Run the release soak**

Run: `STATEFUL_E2E_DURATION_SEC=3600 STATEFUL_E2E_PACKS=all make e2e-system-soak`

Expected: 60-minute PASS, deterministic seed recorded, all owned processes stopped, exact residual rows attested.

- [ ] **Step 4: Verify proof and repository guardrails**

Run:

```bash
make e2e-attestation-check
make risk-guardrails
stratum-verify frontend-full
git diff --check
```

Expected: all PASS; build-size warnings may remain non-failing but must be reported.

- [ ] **Step 5: Commit the matching attestation**

```bash
git add test/e2e/attestations
git commit -m "[test](e2e): attest system stateful acceptance"
```

After committing, rerun `make e2e-attestation-check`. It must pass against committed source without rerunning Chromium.

### Task 12: Review and Ship

**Files:**

- No new implementation files unless review identifies a defect.

- [ ] **Step 1: Review behavior and evidence gaps**

Use `superpowers:requesting-code-review`. Review manifest completeness, secret boundaries, tenant isolation, action validity, cleanup, source-digest exclusions, and the difference between CI integrity validation and runtime proof.

- [ ] **Step 2: Run final verification after review fixes**

Any source fix invalidates the attestation. Rerun Task 11 short acceptance and any risk-required soak, generate a new attestation, and verify all required commands again.

- [ ] **Step 3: Push and create the PR**

Use a PR title such as `[test](e2e): add system stateful acceptance`. The PR body must include What, Why, HowToTest, short/soak duration, seed, attestation path, residual data, and the explicit statement that CI validates but does not execute the browser suite.
