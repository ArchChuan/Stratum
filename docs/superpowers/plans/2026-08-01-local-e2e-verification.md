# Local E2E Verification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make local headless-browser E2E the before-PR authority while CI remains the non-browser merge authority and
the release pipeline verifies the exact current `main` commit and immutable deployed digests.

**Architecture:** `.test/verification.yaml` declares separate local and CI check ownership. The existing deterministic
planner selects risk and local browser mode; a focused local reporter validates attestation v2, source freshness, and
cleanup before atomically recording a developer assertion. CI contract tests map manifest checks to real non-browser
jobs, while deployment derives its candidate from the triggering workflow's `head_sha` and records cluster-observed
digests in a separate release schema.

**Tech Stack:** Go 1.25, Bash, Make, JSON Schema 2020-12, GitHub Actions YAML, jq, headless Playwright/Chromium.

---

## File Structure

- `.test/verification.yaml`: risk classification plus separate local and CI check ownership.
- `.test/schemas/local-verification.schema.json`: local browser assertion contract.
- `.test/schemas/ci-verification.schema.json`: documentation/schema contract for GitHub check state.
- `.test/schemas/release-verification.schema.json`: deployment evidence contract.
- `internal/platform/verificationplan/manifest.go`: typed manifest loading and ownership validation.
- `cmd/verification-plan/main.go`: produces local and CI plans from one risk result.
- `scripts/quality/write-local-verification-report.sh`: atomically writes local terminal evidence.
- `scripts/quality/check-local-verification-report.sh`: validates schema, HEAD, manifest digest, source cleanliness, and
  attestation.
- `scripts/quality/run-planned-checks.sh`: runs focused non-browser checks selected by the local plan.
- `scripts/quality/verification-ci-contract-test.sh`: maps CI check IDs to real workflow jobs and rejects browser jobs.
- `.github/workflows/ci.yml`: parallel fail-closed merge authority with no browser setup.
- `.github/workflows/deploy.yml`: exact candidate SHA propagation and release evidence.
- `.agents/skills/stratum-e2e-development/`: the sole Test Skill and its local-before-PR operating references.
- `docs/agent/instructions.md`, `docs/agent/e2e-standards.md`, `docs/agent/verification-ci-authority.md`: generated source
  and operator-facing authority documentation.

### Task 1: Separate Manifest Ownership

**Files:**

- Modify: `.test/verification.yaml`
- Modify: `internal/platform/verificationplan/manifest.go`
- Modify: `internal/platform/verificationplan/manifest_test.go`
- Modify: `cmd/verification-plan/main.go`
- Modify: `cmd/verification-plan/main_test.go`
- Modify: `.test/schemas/verification-plan.schema.json`
- Modify: `scripts/quality/verification-manifest-test.sh`

- [ ] **Step 1: Write failing table-driven tests for authority separation**

Add cases proving `browser_e2e_authority: local`, `merge_authority: ci`, and `deployment_authority: release_pipeline` are
required; local and CI checks are distinct; and any `e2e-short`, `e2e-soak`, Playwright, or Chromium check owned by CI
is rejected.

```go
tests := []struct {
 name    string
 mutate  func(*Manifest)
 wantErr string
}{
 {name: "accepts separate authorities"},
 {name: "rejects browser check owned by CI", mutate: addCIBrowserCheck, wantErr: "browser check"},
}
```

- [ ] **Step 2: Run the focused tests and confirm the new cases fail**

Run: `go test ./internal/platform/verificationplan ./cmd/verification-plan -count=1`

Expected: FAIL because the manifest has one shared `checks` list and one `authority`.

- [ ] **Step 3: Implement typed local and CI check sets**

Use one risk classification and emit explicit ownership without adding flag parameters:

```go
type Level struct {
 Mode        string   `yaml:"mode"`
 LocalChecks []string `yaml:"local_checks"`
 CIChecks    []string `yaml:"ci_checks"`
}

type plan struct {
 LocalChecks []string `json:"local_checks"`
 CIChecks    []string `json:"ci_checks"`
}
```

Update the plan schema and shell contract to reject legacy shared authority fields.

- [ ] **Step 4: Run manifest, schema, and planner tests**

Run: `make verification-manifest-test verification-schemas-test && go test ./internal/platform/verificationplan ./cmd/verification-plan -count=1`

Expected: PASS.

- [ ] **Step 5: Commit manifest ownership**

```bash
git add .test/verification.yaml .test/schemas/verification-plan.schema.json internal/platform/verificationplan \
  cmd/verification-plan scripts/quality/verification-manifest-test.sh
git commit -m "[refactor](test): separate local and CI verification ownership"
```

### Task 2: Replace the Mixed Completion Report

**Files:**

- Create: `.test/schemas/local-verification.schema.json`
- Create: `.test/schemas/ci-verification.schema.json`
- Create: `.test/schemas/release-verification.schema.json`
- Create: `scripts/quality/write-local-verification-report.sh`
- Create: `scripts/quality/check-local-verification-report.sh`
- Create: `scripts/quality/local-verification-report-test.sh`
- Modify: `scripts/quality/schema-test/verification_schemas_test.go`
- Remove: `.test/schemas/completion-report.schema.json`
- Remove: `scripts/quality/test-verification-report.sh`
- Remove: `scripts/quality/test-verification-report-test.sh`
- Remove: `scripts/quality/write-verification-check-receipt.sh`
- Remove: `scripts/quality/write-verification-review-receipt.sh`

- [ ] **Step 1: Write failing schema tests for three independent records**

Add table cases that require local status values `passed|failed|not_run|stale|infra_failed`, zero blocking capability
counts for `passed`, complete cleanup, immutable release digests, and no reviews, CI receipts, or Sigstore fields in the
local record.

```go
{name: "rejects passed local report with skipped capability", schema: local, value: changedCount(report, "skipped", 1), wantErr: true},
{name: "rejects release report with mutable image", schema: release, value: releaseWithImage("repo:latest"), wantErr: true},
```

- [ ] **Step 2: Run schema tests and confirm missing schemas fail**

Run: `go test ./scripts/quality/schema-test -count=1`

Expected: FAIL because the three authority schemas do not exist.

- [ ] **Step 3: Add schemas and a local report writer**

The writer accepts a plan and verified attestation, computes capability and cleanup summaries with `jq`, writes to a
same-directory temporary file, then renames it:

```bash
temporary=$(mktemp "$(dirname "$output")/.local-verification.XXXXXX")
trap 'rm -f "$temporary"' EXIT
jq -n --arg status "$status" --arg commit "$commit" '{version:1,status:$status,tested_commit:$commit}' >"$temporary"
mv "$temporary" "$output"
trap - EXIT
```

The checker recomputes HEAD, manifest SHA-256, and source cleanliness and returns nonzero for stale or non-passed
reports. Runtime attestation outputs are excluded from dirty-source classification.

- [ ] **Step 4: Add behavior tests for atomic publication and freshness**

Cover clean pass, dirty tracked source, relevant untracked source, manifest change, failed capability, skipped
capability, unreconciled evidence, incomplete cleanup, infrastructure failure, and preserving a prior failed run in a
run-history directory.

Run: `bash scripts/quality/local-verification-report-test.sh && make verification-schemas-test`

Expected: PASS.

- [ ] **Step 5: Remove obsolete mixed-authority machinery after reference search reaches zero**

Run: `rg -n 'completion-report|write-verification-(check|review)-receipt|github-actions-sigstore' . --glob '!docs/superpowers/**'`

Expected: no live implementation references. Remove the old schema and scripts only then.

- [ ] **Step 6: Commit report contracts**

```bash
git add .test/schemas scripts/quality
git commit -m "[refactor](test): split verification authority records"
```

### Task 3: Add the Local Before-PR Entrypoint

**Files:**

- Modify: `Makefile`
- Modify: `scripts/quality/run-planned-checks.sh`
- Modify: `scripts/quality/test-verification-entrypoints-test.sh`
- Create: `scripts/quality/test-verify-before-pr.sh`
- Create: `scripts/quality/test-verify-before-pr-test.sh`

- [ ] **Step 1: Write failing orchestration tests**

Use fake `make` and report commands to assert R0/R1 never starts Chromium, R2 runs short, R3 runs short then the
600-second test-profile soak, R4 adds release soak only for explicit release intent, and every exit path attempts owned
infrastructure shutdown.

```bash
assert_log R2 'focused e2e-system-short write-report check-report'
assert_log R3 'focused e2e-system-short e2e-system-soak write-report check-report'
reject_log R1 'e2e-system-'
```

- [ ] **Step 2: Run entrypoint tests and confirm missing targets fail**

Run: `bash scripts/quality/test-verification-entrypoints-test.sh && bash scripts/quality/test-verify-before-pr-test.sh`

Expected: FAIL because `test-verify-fast` and `test-verify-before-pr` do not exist.

- [ ] **Step 3: Implement explicit orchestration functions without boolean flags**

```bash
run_short() { make e2e-system-short; }
run_soak() { STATEFUL_E2E_PROFILE=test STATEFUL_E2E_DURATION_SEC=600 STATEFUL_E2E_PACKS=all make e2e-system-soak; }
run_release_soak() { make e2e-system-release-soak; }
```

Preserve the first terminal failure, write its local report, then stop only infrastructure owned by the current run.
Only a clean source snapshot may result in `passed`.

- [ ] **Step 4: Run focused orchestration and shell syntax tests**

Run: `bash -n scripts/quality/test-verify-before-pr.sh scripts/quality/run-planned-checks.sh && make test-verification-entrypoints-test`

Expected: PASS.

- [ ] **Step 5: Commit the local entrypoint**

```bash
git add Makefile scripts/quality/run-planned-checks.sh scripts/quality/test-verification-entrypoints-test.sh \
  scripts/quality/test-verify-before-pr.sh scripts/quality/test-verify-before-pr-test.sh
git commit -m "[feat](test): add local before-PR verification"
```

### Task 4: Enforce the Non-Browser CI Contract

**Files:**

- Create: `scripts/quality/verification-ci-contract-test.sh`
- Modify: `.github/workflows/ci.yml`
- Modify: `Makefile`
- Modify: `scripts/quality/system-e2e-instructions-test.sh`

- [ ] **Step 1: Write a failing manifest-to-workflow contract test**

Parse manifest CI check IDs and require a checked-in mapping to real job IDs. Reject Playwright installation,
`chromium`, `e2e-system-short`, `e2e-system-soak`, and any aggregate job that does not test every dependency result.

```bash
browser_pattern='playwright.*install|chromium|e2e-system-(short|soak|release-soak)'
if grep -Eiq "$browser_pattern" "$workflow"; then
  fail 'CI must not execute browser E2E'
fi
```

- [ ] **Step 2: Run the contract and confirm the aggregate is not fail closed**

Run: `bash scripts/quality/verification-ci-contract-test.sh`

Expected: FAIL because `Migration Guardrails` currently echoes success without inspecting `needs.*.result`.

- [ ] **Step 3: Add explicit check mapping and fail-closed aggregation**

Give the aggregate `if: ${{ always() }}` and reject anything except success:

```bash
results='${{ toJSON(needs) }}'
jq -e 'all(.[]; .result == "success")' <<<"$results"
```

Keep #238's parallel jobs and add no browser setup or browser execution.

- [ ] **Step 4: Run workflow contracts**

Run: `make verification-manifest-test verification-ci-contract-test test-verification-entrypoints-test`

Expected: PASS.

- [ ] **Step 5: Commit CI alignment**

```bash
git add .github/workflows/ci.yml Makefile scripts/quality/verification-ci-contract-test.sh \
  scripts/quality/system-e2e-instructions-test.sh
git commit -m "[ci](test): enforce non-browser merge authority"
```

### Task 5: Bind Release Verification to the Exact Candidate

**Files:**

- Modify: `.github/workflows/deploy.yml`
- Modify: `scripts/quality/check-deployment-safety-test.sh`
- Create: `scripts/quality/release-verification-test.sh`

- [ ] **Step 1: Write failing candidate and receipt tests**

Assert every build and deploy checkout uses an explicit candidate SHA, image tags use that SHA, stale workflow runs
are rejected by comparison with the current main tip, and the release record includes migration, health, rollback, and
cluster-observed digests.

```bash
require 'CANDIDATE_SHA:.*workflow_run\.head_sha' 'workflow-run candidate binding'
require 'ref:[[:space:]]*\$\{\{ env\.CANDIDATE_SHA \}\}' 'candidate checkout'
reject 'type=raw,value=\$\{\{ github\.sha \}\}' 'workflow-file SHA image tag'
```

- [ ] **Step 2: Run deployment contracts and confirm candidate propagation fails**

Run: `bash scripts/quality/check-deployment-safety-test.sh && bash scripts/quality/release-verification-test.sh`

Expected: FAIL because build jobs use default checkout and `github.sha`.

- [ ] **Step 3: Implement candidate resolution before build jobs**

Add a `candidate` job whose output is used by all build jobs. For `workflow_run`, require conclusion success, branch
main, and `head_sha == current main`; for tags and manual dispatch, resolve the event-specific immutable SHA. Every
checkout and image tag consumes `needs.candidate.outputs.sha`.

```yaml
outputs:
  sha: ${{ steps.resolve.outputs.sha }}
steps:
  - id: resolve
    env:
      WORKFLOW_RUN_SHA: ${{ github.event.workflow_run.head_sha }}
```

- [ ] **Step 4: Emit and validate release evidence**

Record `status`, candidate commit, actual `@sha256:` workload images, migration result, health result, and rollback
availability. Validate with `release-verification.schema.json` before attesting and uploading.

- [ ] **Step 5: Run deployment and schema contracts**

Run: `make deployment-safety-test verification-schemas-test && bash scripts/quality/release-verification-test.sh`

Expected: PASS.

- [ ] **Step 6: Commit release alignment**

```bash
git add .github/workflows/deploy.yml scripts/quality/check-deployment-safety-test.sh \
  scripts/quality/release-verification-test.sh
git commit -m "[fix](deploy): bind release to successful CI head SHA"
```

### Task 6: Make the Test Skill the Single Source of Practice

**Files:**

- Modify: `.agents/skills/stratum-e2e-development/SKILL.md`
- Modify: `.agents/skills/stratum-e2e-development/references/verification-manifest.md`
- Modify: `.agents/skills/stratum-e2e-development/references/review-contract.md`
- Modify: `docs/agent/instructions.md`
- Modify: `docs/agent/e2e-standards.md`
- Modify: `docs/agent/verification-ci-authority.md`
- Modify: `.github/pull_request_template.md`
- Regenerate: `AGENTS.md`
- Regenerate: `CLAUDE.md`

- [ ] **Step 1: Extend the instruction contract test with forbidden authority claims**

Reject claims that CI runs or signs browser E2E, require `make test-verify-before-pr`, and require local evidence to be
described as an audit assertion rather than a GitHub status.

```bash
reject 'CI.*(Chromium|browser E2E|Sigstore.*attestation)' 'obsolete CI browser authority claim'
require 'test-verify-before-pr' 'canonical local handoff command'
```

- [ ] **Step 2: Run instruction tests and confirm stale guidance fails**

Run: `bash scripts/quality/system-e2e-instructions-test.sh`

Expected: FAIL on existing CI/Sigstore authority language.

- [ ] **Step 3: Update the sole Test Skill, references, docs, and PR summary**

Document local R2 short, local R3 600-second soak, explicit local R4 release soak, parallel non-browser CI, and separate
release verification. Add PR fields for local status, mode, tested commit, capabilities, and cleanup.

- [ ] **Step 4: Regenerate and verify agent instructions**

Run: `make agent-instructions-generate agent-instructions-check && make verification-manifest-test verification-ci-contract-test`

Expected: PASS and exactly one `stratum-e2e-development` skill directory in the repository.

- [ ] **Step 5: Commit the unified Test Skill guidance**

```bash
git add .agents/skills/stratum-e2e-development docs/agent .github/pull_request_template.md AGENTS.md CLAUDE.md
git commit -m "[docs](test): make browser E2E a local before-PR practice"
```

### Task 7: Global Acceptance and Delivery

**Files:**

- Modify only if verification exposes a confirmed defect.

- [ ] **Step 1: Run focused regression contracts**

Run: `make verification-manifest-test verification-schemas-test verification-ci-contract-test \
test-verification-entrypoints-test deployment-safety-test`

Expected: PASS.

- [ ] **Step 2: Run repository quality gates**

Run: `make risk-guardrails code-quality && go vet ./... && go test -short ./... && make fe-lint fe-build`

Expected: PASS with no new complexity violation.

- [ ] **Step 3: Use the Test Skill for local browser acceptance**

Run once after source is committed: `make test-verify-before-pr`

Expected: risk R3, headless short plus 600-second all-pack soak, fresh passed local report, zero failed/skipped/blocked/
unreconciled capabilities, complete cleanup, and no residual owned resources. Do not rerun the browser suite for
unrelated documentation-only corrections after this point; rerun only when the report correctly becomes stale.

- [ ] **Step 4: Verify the final commit and push the branch**

Run: `git diff origin/main...HEAD --check && git status --short && git push -u origin docs/local-e2e-verification-design`

Expected: clean worktree and successful push.

- [ ] **Step 5: Create the PR and monitor parallel CI**

```bash
gh pr create --base main --title "[refactor](test): separate local E2E and CI authority" \
  --body-file tmp/pr-body.md
gh pr checks --watch
```

Expected: all required non-browser checks pass; no CI job installs or starts Chromium.

- [ ] **Step 6: Merge and verify CD**

Run: `gh pr merge --squash --delete-branch`, then locate the `Build and Deploy` run for the merge commit with
`gh run list --workflow deploy.yml` and follow it with `gh run watch <run-id> --exit-status`.

Expected: PR merged into `main`; deployment run succeeds for that exact merge SHA; release evidence records immutable
cluster-observed application digests and terminal deployed status.
