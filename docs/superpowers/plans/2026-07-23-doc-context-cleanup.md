# Documentation Context Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove three confirmed duplicate current documents, preserve their unique operational details, and make the repository's automatic and on-demand context entry points explicit.

**Architecture:** Keep `AGENTS.md` and `CLAUDE.md` as instruction entry points, `docs/agent/` as task-specific context, and `docs/documentation-map.md` as the reader-facing index. Consolidate duplicate current guides into the retained authoritative documents; do not modify historical plans, specifications, or audits other than this approved plan artifact.

**Tech Stack:** Markdown, Bash, repository agent-instruction generator, pre-commit, Stratum risk guardrails.

---

## File Map

- Modify `docs/documentation-map.md`: explain automatic context, explicit imports/references, current reader guides, generated references, and historical records.
- Modify `docs/local-dev.md`: retain the useful local service-address reference currently duplicated in `docs/quick-ref.md`.
- Modify `docs/deployment/CI_CD_GUIDE.md`: retain the warning that `make ci-backend` formats source before running the backend pipeline.
- Modify `docs/deployment/HELM_GUIDE.md`: retain the warning that `make helm-diff` installs the helm-diff plugin when absent.
- Modify `docs/mcp-integration.md`: point readers to generated MCP package documentation instead of maintaining a second hand-written file inventory.
- Delete `docs/LOCAL_CICD_SETUP.md`: its workflow overview and commands are covered by `local-dev.md`, `deployment/CI_CD_GUIDE.md`, and `deployment/HELM_GUIDE.md`.
- Delete `docs/quick-ref.md`: its commands are a strict subset of `local-dev.md`; its service addresses move to that retained guide.
- Delete `docs/mcp-implementation-summary.md`: its status, constraints, and verification commands are covered by `mcp-integration.md`, `mcp-quickstart.md`, and generated package documentation.

### Task 1: Record the deletion evidence

**Files:**

- Verify: `docs/LOCAL_CICD_SETUP.md`
- Verify: `docs/quick-ref.md`
- Verify: `docs/mcp-implementation-summary.md`
- Verify: `README.md`
- Verify: `docs/documentation-map.md`

- [ ] **Step 1: Confirm current inbound references for the three candidates**

Run:

```bash
rg -n 'LOCAL_CICD_SETUP\.md|quick-ref\.md|mcp-implementation-summary\.md' \
  README.md AGENTS.md CLAUDE.md docs scripts .github Makefile \
  --glob '!docs/superpowers/**' --glob '!docs/audits/**'
```

Expected: only `docs/LOCAL_CICD_SETUP.md` links to `quick-ref.md`; no retained current entry point links to the other two candidates.

- [ ] **Step 2: Confirm replacement coverage**

Run:

```bash
rg -n 'ci-backend|helm-diff|localhost:9090|localhost:3000|localhost:16686' \
  Makefile docs/LOCAL_CICD_SETUP.md docs/quick-ref.md docs/local-dev.md \
  docs/deployment/CI_CD_GUIDE.md docs/deployment/HELM_GUIDE.md
rg -n 'MCPService|ClientManager|MCPToolRegistry|通用 HTTP 工具执行路由|go test -short ./internal/mcp' \
  docs/mcp-implementation-summary.md docs/mcp-integration.md docs/mcp-quickstart.md \
  docs/go-package-architecture/internal-mcp-*.md
```

Expected: each candidate's current behavior is already covered; the only details to migrate are the local service addresses and two Makefile side-effect warnings named in the file map.

### Task 2: Clarify context and documentation entry points

**Files:**

- Modify: `docs/documentation-map.md`

- [ ] **Step 1: Add a context-loading section after the fact-baseline note**

Add text with these exact contracts:

```markdown
## Agent 上下文入口

- Codex 自动加载适用范围内的 `AGENTS.md`；根文件由 `docs/agent/instructions.md` 和对应模板生成。
- Claude Code 通过根或子目录 `CLAUDE.md` 的 `@...` 引用加载选定的 `docs/agent/*.md`。
- `docs/agent/` 是按任务引用的模块上下文，不因文件位于该目录就全部常驻。
- 其他 `docs/` Markdown 是人工导航或任务证据，只有被入口引用或为当前任务主动打开时才进入上下文。
```

- [ ] **Step 2: Replace the vague module-material paragraph with explicit categories**

Document these categories without duplicating their contents:

```markdown
## 文档职责

- `docs/agent/`：Agent 可按任务直接引用的当前约束和项目事实。
- `docs/go-package-architecture/`：生成的包级代码参考，不作为常驻指令。
- 顶层及 `deployment/`、`operations/`：面向开发者和运维人员的当前指南。
- `audits/`、`superpowers/specs/`、`superpowers/plans/`：带日期的历史证据，不作为当前运行手册。
```

- [ ] **Step 3: Check the navigation language**

Run:

```bash
rg -n 'Agent 上下文入口|AGENTS\.md|CLAUDE\.md|按任务|文档职责|历史证据' docs/documentation-map.md
```

Expected: all context mechanisms and document categories appear once, with no claim that every file under `docs/` is automatically indexed or loaded.

- [ ] **Step 4: Commit the context-map change**

```bash
git add docs/documentation-map.md
git commit -m '[docs](agent): clarify documentation context entry points'
```

### Task 3: Preserve unique operational details in authoritative guides

**Files:**

- Modify: `docs/local-dev.md`
- Modify: `docs/deployment/CI_CD_GUIDE.md`
- Modify: `docs/deployment/HELM_GUIDE.md`
- Modify: `docs/mcp-integration.md`

- [ ] **Step 1: Add the local service address list to `docs/local-dev.md`**

Add after the optional `make obs-up` command:

```markdown
常用本地地址：

- API：<http://localhost:8080>
- 前端：<http://localhost:3002>
- Prometheus：<http://localhost:9090>
- Grafana：<http://localhost:3000>
- Jaeger：<http://localhost:16686>
```

- [ ] **Step 2: Add the formatting side-effect warning to `docs/deployment/CI_CD_GUIDE.md`**

Add after the local CI command block:

```markdown
`make ci-backend` 会组合上述后端步骤并启动依赖，但它还会执行 `go fmt` 和
`gofmt -s -w` 改写源码；只在准备接受格式化变更时使用。
```

- [ ] **Step 3: Add the plugin side-effect warning to `docs/deployment/HELM_GUIDE.md`**

Add after the Makefile validation commands:

```markdown
`make helm-diff` 需要 helm-diff 插件；插件缺失时，Makefile 会尝试从上游仓库安装。
```

- [ ] **Step 4: Link generated package details from `docs/mcp-integration.md`**

Add after the current architecture diagram:

```markdown
包级职责和依赖图见 [Go 包架构索引](go-package-architecture/README.md#internalmcp)；
本页只维护对使用者稳定的运行时、API 和权限契约。
```

- [ ] **Step 5: Verify transferred details**

Run:

```bash
rg -n 'localhost:9090|localhost:3000|localhost:16686' docs/local-dev.md
rg -n 'ci-backend|gofmt -s -w' docs/deployment/CI_CD_GUIDE.md
rg -n 'helm-diff 插件|尝试从上游仓库安装' docs/deployment/HELM_GUIDE.md
rg -n 'Go 包架构索引|internalmcp|只维护.*运行时' docs/mcp-integration.md
```

Expected: every migrated detail appears exactly in its authoritative guide.

- [ ] **Step 6: Commit the consolidation**

```bash
git add docs/local-dev.md docs/deployment/CI_CD_GUIDE.md \
  docs/deployment/HELM_GUIDE.md docs/mcp-integration.md
git commit -m '[docs](guides): consolidate current operational references'
```

### Task 4: Delete confirmed duplicate current documents

**Files:**

- Delete: `docs/LOCAL_CICD_SETUP.md`
- Delete: `docs/quick-ref.md`
- Delete: `docs/mcp-implementation-summary.md`

- [ ] **Step 1: Re-run the current-reference check before deletion**

Run:

```bash
rg -n 'LOCAL_CICD_SETUP\.md|quick-ref\.md|mcp-implementation-summary\.md' \
  README.md AGENTS.md CLAUDE.md docs scripts .github Makefile \
  --glob '!docs/superpowers/**' --glob '!docs/audits/**'
```

Expected: only links contained inside the three deletion candidates remain.

- [ ] **Step 2: Delete the three duplicate files**

```bash
rm docs/LOCAL_CICD_SETUP.md docs/quick-ref.md docs/mcp-implementation-summary.md
```

- [ ] **Step 3: Prove no retained current reference is broken**

Run:

```bash
rg -n 'LOCAL_CICD_SETUP\.md|quick-ref\.md|mcp-implementation-summary\.md' \
  README.md AGENTS.md CLAUDE.md docs scripts .github Makefile \
  --glob '!docs/superpowers/**' --glob '!docs/audits/**'
```

Expected: no output and exit status `1`.

- [ ] **Step 4: Commit the deletions**

```bash
git add -u docs/LOCAL_CICD_SETUP.md docs/quick-ref.md docs/mcp-implementation-summary.md
git commit -m '[docs](guides): remove duplicate current documents'
```

### Task 5: Verify documentation integrity and scope

**Files:**

- Verify: `README.md`
- Verify: `AGENTS.md`
- Verify: `CLAUDE.md`
- Verify: `docs/**/*.md`

- [ ] **Step 1: Check generated instruction entries**

Run:

```bash
bash scripts/quality/generate-agent-instructions.sh --check
```

Expected: `agent instructions: generated entries are current`.

- [ ] **Step 2: Check Markdown and repository policy hooks**

Run:

```bash
pre-commit run --all-files
```

Expected: all hooks pass, including markdownlint, secret detection, generated agent instructions, and risk checks.

- [ ] **Step 3: Run risk guardrails**

Run:

```bash
make risk-guardrails
```

Expected: exit status `0` with no swallowed or downgraded failure.

- [ ] **Step 4: Confirm historical evidence was preserved**

Run:

```bash
git diff origin/main...HEAD --name-only -- docs/audits docs/superpowers/specs docs/superpowers/plans
```

Expected: only the approved cleanup design and implementation plan are new under `docs/superpowers/`; no pre-existing historical file is modified or deleted.

- [ ] **Step 5: Review final scope and whitespace**

Run:

```bash
git diff --check origin/main...HEAD
git status --short --branch
git diff --stat origin/main...HEAD
```

Expected: no whitespace errors; changes are limited to the design/plan, five retained current guides, and three deleted duplicate guides.
