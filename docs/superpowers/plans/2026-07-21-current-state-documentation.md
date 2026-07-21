# Current-State Documentation Refresh Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Align `README.md` and active documentation under `docs/` with the behavior and deployment contract at revision `37d8f05`.

**Architecture:** Treat source, tests, manifests, and build tooling as the facts layer. Refresh reader-facing overview and operational guides first, then module context and package indexes; preserve audits, completed plans, and historical specifications as immutable records except for explicit supersession notes.

**Tech Stack:** Markdown, Go 1.25.12, React 18.3, Vite 6.4, Gin 1.9, K3s, Traefik, Helm, Bash quality checks.

---

## Task 1: Build the documentation fact inventory

**Files:**

- Read: `go.mod`
- Read: `web/package.json`
- Read: `Makefile`
- Read: `api/http/router.go`
- Read: `api/wiring/`
- Read: `internal/`
- Read: `.github/workflows/`
- Read: `helm/`
- Read: `k8s/`
- Read: `.env.example`
- Create: `docs/documentation-map.md`

- [ ] **Step 1: Inventory active and historical Markdown**

Run:

```bash
find docs -type f -name '*.md' | sort
```

Classify top-level guides, `docs/agent/`, `docs/architecture/`, `docs/deployment/`, `docs/operations/`, and `docs/go-package-architecture/` as active. Classify `docs/audits/` and `docs/superpowers/{plans,specs}/` as historical.

- [ ] **Step 2: Capture current implementation facts**

Run:

```bash
find internal -mindepth 1 -maxdepth 1 -type d -printf '%f\n' | sort
rg -n '^go |github.com/gin-gonic/gin|go.opentelemetry.io/otel|github.com/nats-io/nats.go' go.mod
node -e "const p=require('./web/package.json'); console.log(p.dependencies.react,p.devDependencies.vite,p.dependencies.antd)"
rg -n 'GET\(|POST\(|PUT\(|PATCH\(|DELETE\(' api/http/router.go
```

Expected facts include ten current `internal` contexts, Go `1.25.12`, React `18.3.1`, Vite `6.4.3`, and the registered health/readiness endpoints.

- [ ] **Step 3: Document the reader-facing map**

Create `docs/documentation-map.md` as the canonical index. For each active document, state its audience and authority; link historical collections without describing them as current instructions.

- [ ] **Step 4: Verify the inventory**

Run:

```bash
rg -n 'README|部署|开发|架构|模块|历史' docs/documentation-map.md
```

Expected: the index distinguishes current guidance from historical records and contains no placeholder text.

## Task 2: Refresh the project overview

**Files:**

- Modify: `README.md`
- Modify: `docs/SPEC.md`
- Modify: `docs/DEPENDENCIES.md`
- Modify: `docs/quick-ref.md`
- Modify: `docs/engineering-standards.md`

- [ ] **Step 1: Correct public deployment links and claims**

Change every reader-facing Demo URL to `http://101.200.181.141:6879` and the health example to `http://101.200.181.141:6879/api/health`. Explain that Traefik accepts public port `6879` and forwards to the internal HTTP service; do not require a domain or HTTPS for this profile. Describe OAuth as configuration-dependent.

- [ ] **Step 2: Correct architecture and feature scope**

Replace the obsolete eight-context model with the ten current contexts: `agent`, `evaluation`, `iam`, `knowledge`, `llmgateway`, `mcp`, `memory`, `platform`, `skill`, and `workflow`. Remove roadmap items already implemented and describe only behavior supported by current routes, services, or tests.

- [ ] **Step 3: Synchronize dependency versions and commands**

Use exact versions from `go.mod` and `web/package.json` where the documents promise a version. Verify every documented `make` target against `Makefile`; remove or correct nonexistent commands.

- [ ] **Step 4: Check overview consistency**

Run:

```bash
rg -n '8 个 bounded context|101\.200\.181\.141([^:]|$)|Vite 4|OpenTelemetry v1\.21|NATS JetStream v1\.31' README.md docs/SPEC.md docs/DEPENDENCIES.md docs/quick-ref.md docs/engineering-standards.md
```

Expected: no stale current-state claim remains.

## Task 3: Refresh deployment and operations documentation

**Files:**

- Modify: `docs/DEPLOYMENT_GUIDE.md`
- Modify: `docs/STARTUP_GUIDE.md`
- Modify: `docs/k8s-deployment.md`
- Modify: `docs/deployment/CI_CD_GUIDE.md`
- Modify: `docs/deployment/HELM_GUIDE.md`
- Modify: `docs/deployment/k3s-demo.md`
- Modify: `docs/agent/deployment-architecture.md`
- Modify: `docs/operations/mcp-governor-baseline.md` only if current commands or paths drifted

- [ ] **Step 1: Establish the four-layer port model**

Document public URL, public health URL, Traefik `web2` entrypoint, and internal service target separately. Verify names and values against current Helm/K3s manifests and deployment scripts.

- [ ] **Step 2: Document the HTTP-only IP deployment exception**

State that the current remote production profile intentionally has no domain and no HTTPS. Ensure examples do not trigger domain/TLS prerequisites while retaining a clear warning that traffic is unencrypted.

- [ ] **Step 3: Synchronize deployment workflows**

Compare commands, namespaces, release names, image names, health probes, and rollback/diagnostic steps with `Makefile`, `.github/workflows/`, `helm/`, `k8s/`, and `scripts/`.

- [ ] **Step 4: Search for conflicting operational guidance**

Run:

```bash
rg -n '101\.200\.181\.141|web2|6879|https://|域名|端口 80|:80\b' README.md docs \
  --glob '*.md' --glob '!docs/audits/**' --glob '!docs/superpowers/**'
```

Expected: every current deployment instruction consistently distinguishes external `6879` from internal port `80`.

## Task 4: Refresh development, integration, and module guides

**Files:**

- Modify as facts require: `docs/local-dev.md`
- Modify as facts require: `docs/LOCAL_CICD_SETUP.md`
- Modify as facts require: `docs/DATA_PERSISTENCE.md`
- Modify as facts require: `docs/LLM_INTEGRATION.md`
- Modify as facts require: `docs/QUICKSTART_LLM.md`
- Modify as facts require: `docs/mcp-implementation-summary.md`
- Modify as facts require: `docs/mcp-integration.md`
- Modify as facts require: `docs/mcp-quickstart.md`
- Modify as facts require: `docs/agent/*.md`
- Modify as facts require: `docs/architecture/EVOLUTION.md`

- [ ] **Step 1: Validate setup and configuration examples**

Compare environment variable names with `.env.example` and config loading code. Redact secret-shaped example values and remove references to nonexistent files or commands.

- [ ] **Step 2: Validate module behavior**

For each active module guide, compare claimed responsibilities, state transitions, storage, events, APIs, and failure behavior with its `internal/<context>/` implementation and focused tests. Add `workflow` and `evaluation` navigation where absent.

- [ ] **Step 3: Consolidate duplicated guidance**

Keep one authoritative explanation for each workflow and replace duplicated stale instructions with links to the authoritative page. Do not alter valid module-specific details merely for stylistic uniformity.

- [ ] **Step 4: Validate local references**

Run a repository script or a small read-only shell check that extracts Markdown links and reports missing relative files. Fix every missing path in active documentation.

## Task 5: Reconcile package architecture documentation

**Files:**

- Modify: `docs/go-package-architecture/README.md`
- Modify/add/remove as source packages require: `docs/go-package-architecture/*.md`

- [ ] **Step 1: Compare documented packages with source packages**

Run:

```bash
find internal pkg -type f -name '*.go' -printf '%h\n' | sort -u
find docs/go-package-architecture -type f -name '*.md' | sort
```

Expected: identify missing documentation for current `evaluation` and `workflow` packages and obsolete pages for packages no longer present.

- [ ] **Step 2: Update only package-level facts**

For each mismatch, document purpose, public contracts, important dependencies, and tests from current source. Remove a package page only when the source package no longer exists and no current document links to it.

- [ ] **Step 3: Refresh the package index**

Ensure `docs/go-package-architecture/README.md` links every package page and labels the source revision used for the refresh.

## Task 6: Verify the complete documentation set

**Files:**

- Verify: `README.md`
- Verify: `docs/**/*.md`

- [ ] **Step 1: Run stale-fact and placeholder scans**

Run:

```bash
rg -n '101\.200\.181\.141([^:]|$)|8 个 bounded context|Vite 4|OpenTelemetry v1\.21|TBD|TODO' README.md docs \
  --glob '*.md' --glob '!docs/audits/**' --glob '!docs/superpowers/plans/**' \
  --glob '!docs/superpowers/specs/**'
```

Review every match; expected remaining matches are either absent or explicitly historical.

- [ ] **Step 2: Run formatting and repository guardrails**

Run:

```bash
pre-commit run --all-files
make risk-guardrails
```

Expected: both commands exit `0`.

- [ ] **Step 3: Re-run project verification**

Run:

```bash
stratum-verify go-test
npm --prefix web run lint
npm --prefix web run build
```

Expected: Go tests, frontend lint, and production build exit `0`.

- [ ] **Step 4: Review scope and history preservation**

Run:

```bash
git diff --stat origin/main...HEAD
git diff --name-only origin/main...HEAD
git status --short
```

Expected: no business source or runtime configuration changes, no unintended edits to audits or previously completed plans/specifications, and a clean worktree after the final documentation commit.
