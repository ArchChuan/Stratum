# Remote HTTP Deployment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deploy the remote single-host K3s environment through `http://<public-ip>:6879` without a domain or TLS while keeping every unrelated production security gate intact.

**Architecture:** Keep the external 6879-to-80 forwarding rule outside Kubernetes. Layer a small remote-HTTP Helm values file over the existing HTTPS demo values, inject a validated GitHub Environment `PUBLIC_BASE_URL`, and narrow the deployment safety exception to that one profile.

**Tech Stack:** GitHub Actions, Bash, Helm 3, Kubernetes Ingress, K3s Traefik, Go/React application configuration

---

## Task 1: Verify External Contracts And Risk Scope

**Files:**

- Reference: `docs/superpowers/specs/2026-07-21-remote-http-deployment-design.md`
- Reference: `scripts/quality/risk-regression-guard.sh`

- [ ] **Step 1: Run the risk guard explanation from the feature worktree**

Run:

```bash
bash scripts/quality/risk-regression-guard.sh --explain
```

Expected: exit 0 and output identifying deployment/supply-chain checks for changes under `.github/workflows`, `helm`, and `scripts/quality`.

- [ ] **Step 2: Verify hostless Ingress against Kubernetes documentation**

Open the current official Kubernetes Ingress documentation and confirm that an Ingress rule may omit `host`, causing traffic for the configured IP to match. Record the accessed URL and date in the implementation closeout; if current Kubernetes semantics differ, stop and revise the design before editing configuration.

- [ ] **Step 3: Verify GitHub OAuth callback constraints**

Open the current official GitHub OAuth App authorization documentation and confirm callback URL matching behavior for an IP-literal HTTP callback. Record the accessed URL and date. If GitHub rejects this callback form, retain site/health deployment work but report OAuth as an external blocker rather than weakening state validation.

## Task 2: Validate The Public Base URL

**Files:**

- Create: `scripts/quality/validate-remote-http-base-url.sh`
- Create: `scripts/quality/validate-remote-http-base-url-test.sh`

- [ ] **Step 1: Write the failing table-driven shell test**

Create a test that invokes the validator with these exact cases:

```bash
accept 'http://203.0.113.10:6879'
reject ''
reject 'https://203.0.113.10:6879'
reject 'http://demo.example.com:6879'
reject 'http://203.0.113.10'
reject 'http://203.0.113.10:80'
reject 'http://203.0.113.10:6879/'
reject 'http://user@203.0.113.10:6879'
reject 'http://203.0.113.999:6879'
reject 'http://127.0.0.1:6879'
reject 'http://0.0.0.0:6879'
```

The helpers must fail with the input label only and must never print environment variables.

- [ ] **Step 2: Run the test and verify it fails**

Run:

```bash
bash scripts/quality/validate-remote-http-base-url-test.sh
```

Expected: FAIL because `validate-remote-http-base-url.sh` does not exist.

- [ ] **Step 3: Implement the minimal validator**

Implement a Bash script with `set -euo pipefail` that accepts exactly one argument, parses `http://A.B.C.D:6879` with a regular expression, validates each octet is `0..255`, and rejects loopback, unspecified, multicast, and broadcast addresses. On success it prints nothing; on failure it prints only `invalid PUBLIC_BASE_URL: expected http://<public-ip>:6879` to stderr and exits non-zero.

- [ ] **Step 4: Run focused tests**

Run:

```bash
bash scripts/quality/validate-remote-http-base-url-test.sh
bash -n scripts/quality/validate-remote-http-base-url.sh scripts/quality/validate-remote-http-base-url-test.sh
```

Expected: both commands exit 0 and the test prints its pass message.

- [ ] **Step 5: Commit the validator**

```bash
git add scripts/quality/validate-remote-http-base-url.sh scripts/quality/validate-remote-http-base-url-test.sh
git commit -m "fix(deploy): validate remote HTTP base URL"
```

## Task 3: Add The Remote HTTP Helm Overlay

**Files:**

- Create: `helm/values-demo-remote-http.yaml`
- Modify: `scripts/quality/check-helm-image-rendering-test.sh`

- [ ] **Step 1: Add failing rendered-manifest assertions**

Extend `check-helm-image-rendering-test.sh` to render both files in order:

```bash
helm template stratum "${ROOT}/helm" \
  -f "${ROOT}/helm/values-demo.yaml" \
  -f "${ROOT}/helm/values-demo-remote-http.yaml" \
  --set-string config.frontendUrl=http://203.0.113.10:6879 \
  --set-string config.githubCallbackUrl=http://203.0.113.10:6879/api/auth/github/callback \
  >"${REMOTE_HTTP_RENDER}"
```

Assert the render contains `FRONTEND_URL: "http://203.0.113.10:6879"`, the matching callback, `SECURE_COOKIES: "false"`, and Traefik entrypoint `web`. Assert the Ingress contains neither a `host:` field nor a `tls:` section.

- [ ] **Step 2: Run the rendering test and verify it fails**

Run:

```bash
bash scripts/quality/check-helm-image-rendering-test.sh
```

Expected: FAIL because the remote HTTP overlay does not exist.

- [ ] **Step 3: Create the minimal overlay**

Create:

```yaml
config:
  secureCookies: "false"

ingress:
  enabled: true
  className: "traefik"
  annotations:
    traefik.ingress.kubernetes.io/router.entrypoints: "web"
  hosts:
    - host: ""
      paths:
        - path: /
          pathType: Prefix
          service: frontend
  tls: []
```

- [ ] **Step 4: Run Helm verification**

Run:

```bash
bash scripts/quality/check-helm-image-rendering-test.sh
helm lint ./helm -f helm/values-demo.yaml -f helm/values-demo-remote-http.yaml
```

Expected: both commands exit 0.

- [ ] **Step 5: Commit the overlay**

```bash
git add helm/values-demo-remote-http.yaml scripts/quality/check-helm-image-rendering-test.sh
git commit -m "fix(deploy): add remote HTTP Helm profile"
```

## Task 4: Narrow The Deployment Safety Contract

**Files:**

- Modify: `scripts/quality/check-deployment-safety-test.sh`

- [ ] **Step 1: Write failing profile-specific assertions**

Add `REMOTE_HTTP_VALUES` and assertions that require:

```text
HTTPS demo: https URLs, secureCookies true, websecure, non-empty TLS
local demo: localhost-only HTTP
remote HTTP overlay: secureCookies false, entrypoint web, empty host, tls []
production: existing TLS and database TLS checks unchanged
workflow: validator invocation, two values files, and injected frontend/callback URLs
```

Delete only the blanket rule that rejects all non-localhost demo HTTP URLs.

- [ ] **Step 2: Run the safety test and verify it fails**

Run:

```bash
bash scripts/quality/check-deployment-safety-test.sh
```

Expected: FAIL because the workflow does not yet validate or inject `PUBLIC_BASE_URL`.

- [ ] **Step 3: Keep the test failure for Task 5**

Do not weaken the new assertions or commit a passing false contract. Review the diff to confirm SSH host verification, kubeconfig TLS, immutable digests, external secret checksum, scanner version, coverage baseline, and PostgreSQL TLS assertions remain present.

## Task 5: Wire The Deployment Workflow

**Files:**

- Modify: `.github/workflows/deploy.yml`
- Test: `scripts/quality/check-deployment-safety-test.sh`

- [ ] **Step 1: Add the environment variable and pre-deploy validation**

Expose the GitHub Production Environment variable only in the deploy job:

```yaml
environment: production
env:
  PUBLIC_BASE_URL: ${{ vars.PUBLIC_BASE_URL }}
```

Before any cluster mutation, invoke:

```bash
bash scripts/quality/validate-remote-http-base-url.sh "$PUBLIC_BASE_URL"
```

- [ ] **Step 2: Apply both values files and inject public URLs**

Change the Helm command to include:

```bash
-f helm/values-demo.yaml \
-f helm/values-demo-remote-http.yaml \
--set-string config.frontendUrl="$PUBLIC_BASE_URL" \
--set-string config.githubCallbackUrl="$PUBLIC_BASE_URL/api/auth/github/callback" \
--set-string config.secureCookies="false"
```

Keep all digest and secret checksum flags unchanged.

- [ ] **Step 3: Add public health verification**

After rollout verification, run:

```bash
curl --fail --silent --show-error --max-time 15 \
  "$PUBLIC_BASE_URL/api/health" >/dev/null
```

Do not use `--insecure`, print response bodies, or echo the complete workflow environment.

- [ ] **Step 4: Run workflow and safety checks**

Run:

```bash
bash scripts/quality/check-deployment-safety-test.sh
bash scripts/quality/validate-remote-http-base-url-test.sh
bash scripts/quality/check-helm-image-rendering-test.sh
```

Expected: all commands exit 0.

- [ ] **Step 5: Commit workflow and contract changes**

```bash
git add .github/workflows/deploy.yml scripts/quality/check-deployment-safety-test.sh
git commit -m "fix(deploy): support constrained remote HTTP ingress"
```

## Task 6: Update Operator Documentation

**Files:**

- Modify: `docs/deployment/k3s-demo.md`
- Modify: `docs/deployment/HELM_GUIDE.md`
- Modify: `docs/engineering-standards.md`

- [ ] **Step 1: Document the actual network and configuration contract**

Document `public :6879 -> host :80 -> Traefik web`, the Production Environment variable `PUBLIC_BASE_URL=http://<public-ip>:6879`, the two-file Helm overlay order, and the exact GitHub OAuth callback URL. State explicitly that HTTP traffic and session material are not encrypted in transit.

- [ ] **Step 2: Document verification and HTTPS migration**

Add sanitized verification commands for `/` and `/api/health`. Explain that migration to a domain and TLS removes the HTTP overlay, uses the existing HTTPS demo profile, and restores secure cookies; do not describe disabling global safety checks.

- [ ] **Step 3: Validate documentation**

Run:

```bash
git diff --check
pre-commit run markdownlint --files \
  docs/deployment/k3s-demo.md \
  docs/deployment/HELM_GUIDE.md \
  docs/engineering-standards.md
```

Expected: exit 0.

- [ ] **Step 4: Commit documentation**

```bash
git add docs/deployment/k3s-demo.md docs/deployment/HELM_GUIDE.md docs/engineering-standards.md
git commit -m "docs(deploy): document public HTTP demo access"
```

## Task 7: Full Local Verification

**Files:**

- Verify: all changed files

- [ ] **Step 1: Run deployment-focused checks**

```bash
bash scripts/quality/validate-remote-http-base-url-test.sh
bash scripts/quality/check-deployment-safety-test.sh
bash scripts/quality/check-helm-image-rendering-test.sh
helm lint ./helm -f helm/values-demo.yaml -f helm/values-demo-remote-http.yaml
```

Expected: all pass.

- [ ] **Step 2: Run required repository guardrails**

```bash
make risk-guardrails
go vet ./...
go test -short ./...
```

Expected: all pass. Run frontend lint/build only if the final diff touches `web/`.

- [ ] **Step 3: Inspect the final diff and secret boundary**

```bash
git diff origin/main...HEAD --check
git diff origin/main...HEAD --stat
git grep -nE '101\.200\.|PUBLIC_BASE_URL=.*[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' -- ':!docs/superpowers'
```

Expected: no hard-coded deployment IP outside examples/specification and no whitespace errors.

## Task 8: Remote End-To-End Verification

**Files:**

- Verify: GitHub Production Environment and deployed K3s release

- [ ] **Step 1: Configure external deployment metadata**

Set the GitHub Production Environment variable `PUBLIC_BASE_URL` to the real `http://<public-ip>:6879`. Configure the GitHub OAuth App callback to `$PUBLIC_BASE_URL/api/auth/github/callback`. Do not store either value in a tracked production values file.

- [ ] **Step 2: Deploy through the real workflow**

Trigger the approved deployment path and verify the workflow passes URL validation, Helm rollout, and public health verification. Do not manually patch the live release in a way that is absent from Git.

- [ ] **Step 3: Verify the real user path**

Using the project `stratum-e2e-development` skill, verify in a real browser/API flow:

```text
GET http://<public-ip>:6879/ -> frontend loads
GET http://<public-ip>:6879/api/health -> success
GitHub login -> callback -> authenticated application session
```

Check desktop and mobile navigation for the login redirect only; no visual redesign is in scope. Capture sanitized status evidence without tokens, cookies, authorization codes, PII, or response bodies.

- [ ] **Step 4: Record residual risk or blockers**

If GitHub rejects the IP-literal HTTP callback or remote authority is unavailable, report OAuth or deployment as externally blocked. Do not mark the full task complete based only on Helm rendering or `/api/health`.
