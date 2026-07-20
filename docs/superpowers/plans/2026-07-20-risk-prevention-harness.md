# 风险预防 Harness 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 AR-001 至 AR-024 的修复经验固化为编码前规则、提交时快速守卫和 CI 回归保护，并完成本地历史报告风险正文收口。

**Architecture:** 新增一个不含 AI 判断的路径路由脚本，根据改动文件调用现有架构、迁移、部署、后端和前端检查。`AGENTS.md` 与 `CLAUDE.md` 只保留人工判断原则；pre-commit 和 CI 复用同一脚本；本地 `tmp` 报告通过一次性操作清理，不进入 Git。

**Tech Stack:** Bash、pre-commit、GitHub Actions、Go test、Vitest/npm、现有 Stratum quality scripts。

---

## 文件结构

- 新建 `scripts/quality/risk-regression-guard.sh`：按文件路径选择确定性检查并传播失败。
- 新建 `scripts/quality/risk-regression-guard-test.sh`：使用临时仓库和 fake executor 验证路由、去重、快速退出和失败传播。
- 新建 `scripts/quality/testdata/fake-risk-guard-executor.sh`：记录守卫准备执行的命令，并按指定 label 模拟失败。
- 修改 `.pre-commit-config.yaml`：加入快速风险守卫 hook。
- 修改 `.github/workflows/ci.yml`：Guardrails job 运行守卫全量模式，作为同一入口的远端验证。
- 修改 `Makefile`：增加开发者可直接调用的 `risk-guardrails` target。
- 修改 `AGENTS.md`、`CLAUDE.md`：加入内容一致的高风险编码检查表和命令入口。
- 删除 `docs/audits/service-governance-2026-07-20-generated-reports.md`：规则提炼完成后移除受版本控制的风险台账。
- 本地创建 `tmp/risk-consolidated/reports/latest.md` 并机械清理纳入范围的历史报告；这些文件不进入 Git。

### Task 1：快速守卫路由测试

**Files:**

- Create: `scripts/quality/risk-regression-guard-test.sh`
- Create: `scripts/quality/testdata/fake-risk-guard-executor.sh`
- Test: `scripts/quality/risk-regression-guard-test.sh`

- [ ] **Step 1：编写 fake executor**

实现接收 `label command...` 的脚本，将 label 与命令写入 `RISK_GUARD_COMMAND_LOG`；当 label 等于 `RISK_GUARD_FAIL_LABEL` 时返回 42，否则返回 0。测试不得执行真实 Go/npm/Helm 命令。

- [ ] **Step 2：编写失败测试**

测试依次验证：

```text
docs/readme.md                         -> 无命令，退出 0
api/wiring/platform.go                -> architecture
pkg/storage/postgres/tenant_schema.sql -> migration
.github/workflows/deploy.yml          -> deployment
api/http/handler/auth_handler.go      -> auth-http
internal/knowledge/application/x.go   -> knowledge
internal/memory/application/x.go      -> memory
internal/mcp/infrastructure/x.go      -> mcp
api/middleware/rate_limit.go          -> runtime-governance
web/src/modules/iam/x.ts              -> frontend-auth
web/package-lock.json                 -> frontend-supply-chain
--all                                 -> 所有唯一 label
```

同一个 label 被多个文件触发时只执行一次。设置 `RISK_GUARD_FAIL_LABEL=auth-http` 后，守卫必须返回 42。

- [ ] **Step 3：运行测试并确认 RED**

Run: `/bin/bash scripts/quality/risk-regression-guard-test.sh`

Expected: FAIL，提示 `risk-regression-guard.sh` 不存在。

### Task 2：实现快速守卫

**Files:**

- Create: `scripts/quality/risk-regression-guard.sh`
- Test: `scripts/quality/risk-regression-guard-test.sh`

- [ ] **Step 1：实现输入模式**

支持：

```bash
bash scripts/quality/risk-regression-guard.sh --all
bash scripts/quality/risk-regression-guard.sh path/to/a.go path/to/b.yml
bash scripts/quality/risk-regression-guard.sh
```

无参数时读取 `git diff --cached --name-only --diff-filter=ACMR`。无相关文件时输出 `risk regression guard: no relevant changes` 并退出 0。

- [ ] **Step 2：实现路径到 label 的去重路由**

使用 Bash `case` 和关联数组选择下列命令：

```text
architecture:
  bash scripts/quality/arch-guard-test.sh
  bash scripts/quality/arch-guard.sh api/wiring/*.go
migration:
  bash scripts/quality/check-migration-boundaries-test.sh
  bash scripts/quality/check-migration-boundaries.sh
  go test ./pkg/storage/postgres ./pkg/tenantdb
deployment:
  bash scripts/quality/check-deployment-safety-test.sh
auth-http:
  go test ./api/http/... ./internal/iam/...
knowledge:
  go test ./internal/knowledge/... ./pkg/storage/milvus
memory:
  go test ./internal/memory/...
mcp:
  go test ./internal/mcp/...
runtime-governance:
  go test ./api/middleware ./api/http ./cmd/server
frontend-auth:
  npm --prefix web run typecheck
  npm --prefix web test -- --run
frontend-supply-chain:
  npm --prefix web audit --audit-level=high
```

脚本使用固定命令表，不拼接来自文件名的 shell 文本。设置 `RISK_GUARD_EXECUTOR` 时，将 label 和命令参数交给该 executor；否则直接执行命令。

- [ ] **Step 3：运行测试并确认 GREEN**

Run: `/bin/bash scripts/quality/risk-regression-guard-test.sh`

Expected: `risk regression guard tests passed`

- [ ] **Step 4：运行格式和 shell 静态检查**

Run: `bash -n scripts/quality/risk-regression-guard.sh scripts/quality/risk-regression-guard-test.sh scripts/quality/testdata/fake-risk-guard-executor.sh`

Expected: exit 0。

- [ ] **Step 5：提交守卫实现**

```bash
git add scripts/quality/risk-regression-guard.sh \
  scripts/quality/risk-regression-guard-test.sh \
  scripts/quality/testdata/fake-risk-guard-executor.sh
git commit -m "feat(quality): add risk regression guard"
```

### Task 3：接入开发和 CI Harness

**Files:**

- Modify: `.pre-commit-config.yaml`
- Modify: `.github/workflows/ci.yml`
- Modify: `Makefile`
- Test: `scripts/quality/risk-regression-guard-test.sh`

- [ ] **Step 1：先扩展契约测试**

在守卫测试中断言：

```text
.pre-commit-config.yaml 包含 id: risk-regression-guard 和守卫脚本入口
.github/workflows/ci.yml 的 Guardrails job 调用 risk-regression-guard-test.sh 与 --all
Makefile 的 risk-guardrails target 调用守卫测试与 --all
```

- [ ] **Step 2：运行并确认 RED**

Run: `/bin/bash scripts/quality/risk-regression-guard-test.sh`

Expected: FAIL，提示缺少 pre-commit、CI 或 Makefile 接入。

- [ ] **Step 3：实现三个入口**

pre-commit hook 使用：

```yaml
- id: risk-regression-guard
  name: risk regression guard
  language: system
  entry: bash scripts/quality/risk-regression-guard.sh
  pass_filenames: true
  files: '^(api/|cmd/|internal/|pkg/|web/|helm/|k8s/|\.github/workflows/)'
```

CI Guardrails 先运行脚本自测，再运行 `bash scripts/quality/risk-regression-guard.sh --all`。Makefile target 使用同样两条命令。

- [ ] **Step 4：运行并确认 GREEN**

Run: `/bin/bash scripts/quality/risk-regression-guard-test.sh`

Expected: PASS。

- [ ] **Step 5：验证配置语法**

Run: `pre-commit validate-config && pre-commit run risk-regression-guard --all-files`

Expected: 配置有效，相关检查通过。

- [ ] **Step 6：提交 harness 接入**

```bash
git add .pre-commit-config.yaml .github/workflows/ci.yml Makefile \
  scripts/quality/risk-regression-guard-test.sh
git commit -m "ci(quality): enforce risk regression guard"
```

### Task 4：沉淀编码前高风险规则

**Files:**

- Modify: `AGENTS.md`
- Modify: `CLAUDE.md`
- Test: `scripts/quality/risk-regression-guard-test.sh`

- [ ] **Step 1：扩展文档一致性测试**

在测试中用 `<!-- risk-prevention:start -->` 和 `<!-- risk-prevention:end -->` 提取两份文档的区块，断言内容完全一致，并断言区块包含 `make risk-guardrails`。

- [ ] **Step 2：运行并确认 RED**

Run: `/bin/bash scripts/quality/risk-regression-guard-test.sh`

Expected: FAIL，提示缺少风险预防区块。

- [ ] **Step 3：加入相同规则区块**

区块内容覆盖规格中的七条人工检查原则，说明自动报告只是候选证据，并提供 `make risk-guardrails` 入口。不得复制 AR 编号和历史风险正文。

- [ ] **Step 4：运行并确认 GREEN**

Run: `/bin/bash scripts/quality/risk-regression-guard-test.sh`

Expected: PASS。

- [ ] **Step 5：运行 Markdown 检查并提交**

```bash
pre-commit run markdownlint --files AGENTS.md CLAUDE.md
git add AGENTS.md CLAUDE.md scripts/quality/risk-regression-guard-test.sh
git commit -m "docs(agent): add high-risk coding guardrails"
```

### Task 5：删除受版本控制的旧风险台账

**Files:**

- Delete: `docs/audits/service-governance-2026-07-20-generated-reports.md`
- Modify: references returned by `rg -n 'service-governance-2026-07-20-generated-reports' . --glob '!docs/superpowers/**'`

- [ ] **Step 1：检查引用**

Run: `rg -n 'service-governance-2026-07-20-generated-reports' . --glob '!docs/superpowers/**'`

Expected: 只列出确需删除或更新的活动入口；设计和计划保留历史说明。

- [ ] **Step 2：删除旧审计并清理活动引用**

使用补丁删除文件。任何活动入口改为指向 `make risk-guardrails` 或相关模块规则，不指向本地 `tmp`。

- [ ] **Step 3：验证无活动引用**

Run: `rg -n 'service-governance-2026-07-20-generated-reports' . --glob '!docs/superpowers/**'`

Expected: 无输出。

- [ ] **Step 4：提交删除**

```bash
git add -u docs/audits
git add <updated-active-reference-files>
git commit -m "docs(audit): retire generated risk register"
```

### Task 6：本地报告清理和汇总

**Files:**

- Create local only: `/home/yang/go-projects/stratum/tmp/risk-consolidated/reports/latest.md`
- Modify local only: `/home/yang/go-projects/stratum/tmp/{arch-guard,risk-scan,dep-scan,frontend-health,migration-check,change-summary,secret-scan}/reports/*.md`
- Preserve: `/home/yang/go-projects/stratum/tmp/agent-interview/reports/*.md`

- [ ] **Step 1：记录知识报告摘要校验值**

Run: `sha256sum /home/yang/go-projects/stratum/tmp/agent-interview/reports/*.md > /tmp/stratum-agent-interview-before.sha256`

- [ ] **Step 2：创建一次性清理脚本并执行**

脚本必须：

- 为每个纳入报告保留标题和以 `-` 开头的生成元数据；
- 对 change-summary 只删除 `## 风险与关注点` 至下一同级标题的内容；
- 对 risk-scan、arch-guard 和旧 dependency finding 报告，将已解决风险正文替换为迁移说明；
- 保留 scan exit code、计数和机读证据路径；
- 不跟随 symlink 重复改写；
- 不处理 JSON、SARIF 或 agent-interview。

一次性脚本放 `/tmp`，运行后删除，不提交仓库。

- [ ] **Step 3：生成本地统一报告**

报告包含来源清单、开放项（没有则明确写“无已确认开放项”）、24 项精简关闭索引、三层 harness 映射和 2026-07-20 验证证据。

- [ ] **Step 4：验证本地清理**

```bash
sha256sum -c /tmp/stratum-agent-interview-before.sha256
find /home/yang/go-projects/stratum/tmp -path '*/reports/*.md' \
  ! -path '*/agent-interview/*' -type f -exec grep -l 'AR-00[1-9]\|AR-01[0-9]\|AR-02[0-4]' {} +
test -s /home/yang/go-projects/stratum/tmp/risk-consolidated/reports/latest.md
```

Expected: 知识报告校验值全部通过；历史报告不再包含 AR 风险正文；本地统一报告非空。

### Task 7：完整验证与收尾

**Files:**

- Verify only.

- [ ] **Step 1：运行快速守卫完整模式**

Run: `make risk-guardrails`

Expected: 所有路由检查通过。

- [ ] **Step 2：运行项目完整验证**

```bash
stratum-verify go-full
stratum-verify frontend-full
go test -race -timeout 30s ./...
```

Expected: 全部退出 0。

- [ ] **Step 3：运行提交前检查**

Run: `pre-commit run --all-files`

Expected: 全部通过。

- [ ] **Step 4：检查最终差异和本地报告状态**

```bash
git diff --check
git status --short
test -s /home/yang/go-projects/stratum/tmp/risk-consolidated/reports/latest.md
```

Expected: Git 仅包含计划内提交；本地统一报告存在；没有临时脚本或进程残留。
