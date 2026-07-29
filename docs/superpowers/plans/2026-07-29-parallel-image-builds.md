# Parallel Image Builds Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build and push the backend, frontend, and Feishu alert adapter images concurrently with isolated GitHub Actions caches.

**Architecture:** Split the serial image steps into three jobs that all depend on `test`. Add a lightweight `build-and-push` fan-in job that preserves the outputs and dependency contract consumed by `deploy`.

**Tech Stack:** GitHub Actions YAML, Docker Buildx, GitHub Actions cache backend, shell-based static workflow assertions.

---

## Task 1: Specify The Workflow Graph And Cache Contract

**Files:**

- Modify: `.github/workflows/deploy.yml:30-97`
- Modify: `scripts/quality/check-deployment-safety-test.sh:54-58`

- [ ] **Step 1: Run a failing static assertion against the current workflow**

Run:

```bash
for job in build-backend build-frontend build-feishu-adapter; do
  rg -q "^  ${job}:" .github/workflows/deploy.yml
done
```

Expected: FAIL because the three parallel image jobs do not exist yet.

- [ ] **Step 2: Define the three independent image jobs**

Move the existing backend setup/build/push steps to `build-backend`, the frontend build/push step to `build-frontend`, and the adapter build/push step to `build-feishu-adapter`. Set `needs: test` on each job. Keep image names, tags, Dockerfiles, registry login, and push behavior unchanged.

- [ ] **Step 3: Isolate Buildx cache scopes**

Use matching scopes on each job:

```yaml
cache-from: type=gha,scope=<job-scope>
cache-to: type=gha,scope=<job-scope>,mode=max
```

The exact scopes are `backend`, `frontend`, and `feishu-alert-adapter`.

- [ ] **Step 4: Add the fan-in job and preserve deployment outputs**

Define `build-and-push` with:

```yaml
needs: [build-backend, build-frontend, build-feishu-adapter]
outputs:
  image-tag: ${{ github.sha }}
  adapter-digest: ${{ needs.build-feishu-adapter.outputs.digest }}
```

The job contains only a successful no-op command. Keep `deploy.needs: build-and-push` unchanged.

- [ ] **Step 5: Run graph and cache assertions**

Update the deployment safety guard to require all three job names, a `needs: test` declaration in each job, unique matching cache scopes, the fan-in dependency list, and both stages of adapter digest forwarding. Run the guard together with `rg` assertions for `deploy.needs: build-and-push`.

Expected: every assertion exits 0.

- [ ] **Step 6: Validate and commit the workflow change**

Run:

```bash
git diff --check
pre-commit run check-yaml --files .github/workflows/deploy.yml
make risk-guardrails
```

Expected: all commands pass. Then commit `.github/workflows/deploy.yml` with `[ci](deploy): parallelize image builds`.

## Task 2: Final Verification

**Files:**

- Verify: `.github/workflows/deploy.yml`
- Verify: `docs/superpowers/specs/2026-07-29-parallel-image-builds-design.md`
- Verify: `docs/superpowers/plans/2026-07-29-parallel-image-builds.md`

- [ ] **Step 1: Inspect the final diff and branch state**

Run `git diff origin/main...HEAD --check`, `git diff origin/main...HEAD -- .github/workflows/deploy.yml`, and `git status --short --branch`.

Expected: no whitespace errors; only the approved design, plan, and workflow changes are present; the worktree is clean.

- [ ] **Step 2: Confirm runtime verification boundary**

Record that local checks validate syntax and dependency structure. Actual concurrent scheduling and remote cache reuse require observing the next GitHub Actions run after the branch is pushed or merged.
