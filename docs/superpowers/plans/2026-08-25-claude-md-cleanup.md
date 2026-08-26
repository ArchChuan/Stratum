# CLAUDE.md 大清洗实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 按系统现状全量评估并清洗 CLAUDE.md 文档体系（instructions.md + 25 个 docs/agent/ 文件），并重构端到端测试机制（测试门槛 + 专用测试 agent）。

**Architecture:** 两阶段交付。第一阶段用并行子代理评估 25 个文档、对照系统现状产出评估报告（**用户确认门**，报告确认后才进入第二阶段）；第二阶段按报告修正 `docs/agent/instructions.md` 与各 doc，`make agent-instructions` 重新生成 CLAUDE.md/AGENTS.md，并创建本地测试 agent `stratum-e2e-tester`。

**Tech Stack:** Markdown、bash（`scripts/quality/generate-agent-instructions.sh`）、make（`agent-instructions` / `agent-instructions-check`）、markdownlint。

## Global Constraints

- **只改源文件**：`docs/agent/instructions.md` 是唯一事实源；`CLAUDE.md`/`AGENTS.md` 由 `make agent-instructions` 生成，**禁止手改**。
- **Git 规则**：禁止在 main 直接提交；当前在 `feat/doc-cleanup` worktree（`/home/yang/go-projects/stratum-doc-cleanup`），所有 git 命令须 `cd /home/yang/go-projects/stratum-doc-cleanup` 后执行（hooks 校验 effective cwd）。
- **.claude/ 不入库**：`stratum-e2e-tester` agent 定义放 `.claude/agents/`（gitignore），本地配置，不随 git 提交。
- **R3 验证闭环**：`docs/agent/**`、`CLAUDE.md` 命中 `.test/verification.yaml` 的 `agent-governance` R3 规则；本 PR 须走 `make test-verify-before-pr` 完整验证。
- **commit 规范**：`[type](scope): description`，type 用 `docs`，末尾加 `Co-Authored-By: Claude <noreply@anthropic.com>`。

---

### Task 1: 全量评估文档体系，产出评估报告

**Files:**

- Create: `docs/superpowers/plans/2026-08-25-claude-md-cleanup-audit.md`

**Interfaces:**

- Produces: `2026-08-25-claude-md-cleanup-audit.md` —— 每文档一行：`文档名 | 最后修改 | 角色 | 状态(✅⚠️❌) | 过时点清单 | 处理建议(修改/保留/归档/删除/合并)`。Task 3/4 以其中"处理建议=修改"的行为执行清单。

- [ ] **Step 1: 建立评估清单**

先确认 25 个评估对象，写入报告的"评估范围"节：

- `docs/agent/instructions.md` + `docs/agent/templates/{claude,agents}-prefix.md`
- 16 个被引用：project / architecture / backend-go / constants / migration-tenant / api / agent / agent-chat-flow / milvus / nats / memory-facts / frontend / product / observability / deployment-architecture / knowledge-workspace
- 6 个未引用：bug-lessons / e2e-standards / memory-trajectory-reflection / review-platform-config-versions / tiered-memory / verification-ci-authority

- [ ] **Step 2: 派发并行评估子代理**

按角色分成 4 组，用 Agent 工具并行派发（general-purpose / Explore），每组一个 agent，对照系统现状验证：

- 组 1（结构类）：architecture、project、backend-go —— 对照 `internal/`、`pkg/`、`api/` 目录
- 组 2（模块类）：api、agent、agent-chat-flow、milvus、nats、memory-facts —— 对照 handler/middleware/pipeline 实际文件与 API
- 组 3（新/较新类）：constants、migration-tenant、frontend、product、observability、deployment-architecture、knowledge-workspace —— 对照 constants 文件、Makefile、前端 package.json
- 组 4（未引用 + 模板）：bug-lessons、e2e-standards、memory-trajectory-reflection、review-platform-config-versions、tiered-memory、verification-ci-authority、instructions.md、templates/ —— 评估价值、是否过时、是否应与 CLAUDE.md 索引关联

每个 agent 的 prompt 模板（以组 1 为例）：

```
评估 docs/agent/ 下这些文档是否与实际系统一致：architecture.md、project.md、backend-go.md。
以系统现状为基准（代码、测试、CI、配置，勿用文档自证）：
1. 读每个文档，提取可验证的事实断言（目录结构、依赖版本、文件路径、API 签名、流程描述）
2. 对照系统验证：ls 目录、读 go.mod/web/package.json、检查引用的文件/API 是否仍存在
3. 输出表格：文档名 | 状态(✅准确/⚠️部分过时/❌过时) | 过时点清单(每条含:文档声称 vs 系统实际) | 处理建议(修改/保留/归档/删除/合并)
仅输出评估结论，不要修改任何文件。仓库根目录: /home/yang/go-projects/stratum-doc-cleanup
```

- [ ] **Step 3: 汇总去重、交叉核对成报告**

收集 4 组子代理结果，在 `2026-08-25-claude-md-cleanup-audit.md` 中：

- 合并同文档的多组结果，去重过时点
- 对"状态"有分歧的文档，主 agent 亲自复核（读文档 + 对照代码）
- 补一份"全局过时点"节（跨文档共性问题：OTEL 1.42、React Router 7、14 contexts、pkg 清单、config/prod.yaml 幽灵引用）
- 报告包含"处理建议汇总表"：`处理方式 | 文档列表`

- [ ] **Step 4: 自审报告完整性**

检查：25 个对象每文档有且仅一行结论；无 TBD/未评估项；过时点都能追溯到具体代码/文件证据。

- [ ] **Step 5: 提交（用户确认门）**

```bash
cd /home/yang/go-projects/stratum-doc-cleanup
git add docs/superpowers/plans/2026-08-25-claude-md-cleanup-audit.md
git commit -m "docs(agent): CLAUDE.md 文档体系全量评估报告

逐文档对照系统现状（代码/测试/CI/配置）评估 25 个对象，
产出处理建议（改/保留/归档/删除/合并）供确认。

Co-Authored-By: Claude <noreply@anthropic.com>"
```

⏸ **暂停**：报告提交后等待用户确认处理建议。**用户确认前不得开始 Task 2**。

---

### Task 2: 修正 instructions.md 硬事实

**Files:**

- Modify: `docs/agent/instructions.md`

**Interfaces:**

- Consumes: Task 1 报告中"处理建议=修改"且属于 instructions.md 的条目
- Produces: 修正后的 `instructions.md`；后续任务重新生成 CLAUDE.md/AGENTS.md

- [ ] **Step 1: 更新 Technology and directory map 的 context 列表**

把 "当前上下文为 `agent`、`evaluation`、`iam`、`knowledge`、`llmgateway`、`mcp`、`memory`、`platform`、`scheduler`、`skill`、`workflow`" 改为 14 个完整列表，补 `audit`、`collab`、`parameters`，按字母序：

```
agent、audit、collab、evaluation、iam、knowledge、llmgateway、mcp、memory、parameters、platform、scheduler、skill、workflow
```

- [ ] **Step 2: 更新依赖版本与清单**

`go.mod` 为准（Go 1.25.12、gin v1.9.1、nats.go v1.51.0、milvus-sdk v2.4.2、pgx v5.9.2、go-redis v9.7.3、golang-jwt v5.3.1、zap v1.27.1）：

- OTEL `v1.40.0` → `v1.42.0`
- 补充关键依赖：minio-go v7、unidoc/unioffice、modelcontextprotocol/go-sdk、robfig/cron、bufbuild/protocompile
- `web/package.json` 为准：React Router `6.26` → `7.18.2`（其余 React 18.3 / Vite 6.4 / AntD 5.20 / Axios 1.18 不变）

- [ ] **Step 3: 更新 pkg/ 结构描述**

按实际 `ls pkg/`：补 `dag`、`jsonschema`、`messaging`、`safetext`、`timeutil`、`tokenutil`、`postgres`；`storage/` 下注明含 `{milvus,postgres,redis,filestore,objectstore,tenantnaming}`；保留 `pkg/vector` 仅兼容旧 import 的说明。

- [ ] **Step 4: 处理 config/prod.yaml 幽灵引用**

"禁止修改 `config/prod.yaml`" 引用了不存在的文件。改为反映实际配置方式：配置由 `config/config.go` 直接读环境变量（无 Viper/yaml 装载链），部署覆盖在 `helm/values-prod.yaml`；表述改为"禁止修改 `config/config.go` 的默认值；生产覆盖走 `helm/values-prod.yaml`"。

- [ ] **Step 5: 更新 middleware 与前端 modules 描述**

- middleware：补 `body_limit`、`rate_limit`、`public_error`、`require_default_tenant`、`system_role_check`
- web modules：补 `approvals`、`audit`、`collab`、`operation-gate`、`parameters`、`scheduled-task`

- [ ] **Step 6: 验证并重新生成**

```bash
cd /home/yang/go-projects/stratum-doc-cleanup
make agent-instructions      # 重新生成 CLAUDE.md / AGENTS.md
make agent-instructions-check
npx markdownlint --config .markdownlint.json docs/agent/instructions.md CLAUDE.md AGENTS.md
```

预期：agent-instructions-check 通过，markdownlint 无报错。

- [ ] **Step 7: 提交**

```bash
git add docs/agent/instructions.md CLAUDE.md AGENTS.md
git commit -m "docs(agent): 修正 instructions.md 硬事实（14 contexts/OTEL1.42/ReactRouter7/pkg 清单/prod.yaml 引用）

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: 按报告修正被引用的 doc

**Files:**

- Modify: 按 Task 1 报告中"处理建议=修改"的被引用 doc（预期包括 architecture.md、project.md；其余以报告为准）

**Interfaces:**

- Consumes: Task 1 报告 `2026-08-25-claude-md-cleanup-audit.md` 中每文档的"过时点清单"与"处理建议"

- [ ] **Step 1: 列出待修改清单**

从报告中筛选"处理建议=修改"且为 16 个被引用 doc 的条目，按文档分组。对每个文档逐条应用其"过时点清单"。

- [ ] **Step 2: 修正 architecture.md**

- "目录骨架"：`internal/<ctx>` 从 11 个 context 更新为 14 个；`pkg/storage/{postgres,redis,milvus,tenantnaming}` 补 `filestore,objectstore`；`pkg/` 列表补 `dag,jsonschema,messaging,safetext,timeutil,tokenutil,postgres`
- "11 个 bounded context" → "14 个 bounded context"，列表补 `audit · collab · parameters`

- [ ] **Step 3: 修正 project.md**

- 目录结构：internal 各 context 描述补 audit/collab/parameters；middleware 列表补新项；pkg 列表补新包；依赖表 OTEL v1.40 → v1.42、补 minio 等
- `pkg/vector` 描述与现状核对（保留"仅兼容旧 import"说明）

- [ ] **Step 4: 逐个应用其余被引用 doc 的过时点**

对报告中其余标注"修改"的被引用 doc（如 api.md 的 route 列表、agent.md、observability.md 等），逐条按其"过时点清单"修正。每个文档修正后运行 markdownlint。

- [ ] **Step 5: 验证并提交**

```bash
cd /home/yang/go-projects/stratum-doc-cleanup
npx markdownlint --config .markdownlint.json docs/agent/*.md
git add docs/agent/
git commit -m "docs(agent): 按评估报告修正被引用的 doc

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: 按报告处理未引用文档

**Files:**

- Modify/Delete/Move: 按 Task 1 报告处理 6 个未引用 doc（bug-lessons / e2e-standards / memory-trajectory-reflection / review-platform-config-versions / tiered-memory / verification-ci-authority）

**Interfaces:**

- Consumes: Task 1 报告中这 6 个文档的"处理建议"

- [ ] **Step 1: 按报告执行处理**

对每个未引用 doc 按其处理建议执行：

- **保留**：无需改动（内容有效但未被索引引用，保持现状）
- **归档**：移动到 `docs/agent/archive/`（若目录不存在则创建），并确认无其他文件引用
- **合并**：将有效内容并入对应被引用 doc（如 tiered-memory 并入 memory-facts、e2e-standards 并入 verification-ci-authority 或 frontend），删除源文件
- **删除**：确认无引用后 `git rm`

- [ ] **Step 2: 校验引用完整性**

用 `grep -rn "docs/agent/<已删/已移文件名>" docs/ CLAUDE.md AGENTS.md` 确认无残留引用（预期无输出）。

- [ ] **Step 3: 提交**

```bash
cd /home/yang/go-projects/stratum-doc-cleanup
git add -A docs/agent/
git commit -m "docs(agent): 处理未引用文档（保留/归档/合并/删除）

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 5: e2e 测试重构 —— 门槛原则与测试 agent 机制写入 instructions.md

**Files:**

- Modify: `docs/agent/instructions.md`（"Development and end-to-end verification" 节）

**Interfaces:**

- Produces: instructions.md 中新增的测试门槛原则与 stratum-e2e-tester 机制描述（Task 6 创建实际 agent）

- [ ] **Step 1: 重写 e2e 验证小节，提升地位**

把 "Development and end-to-end verification" 节中 e2e 部分改为独立小节"端到端测试与验收"，内容：

- **测试门槛原则**：只改字段 / 只改单个小 bug / 常量值调整 → 最小验证（unit + contract）；其余所有改动（功能、Bug 修复、前后端联调、数据库链路、Agent/Skill/MCP/Memory/Knowledge/IAM、文档体系本身）→ 必须完整测试（`make test-verify-before-pr`），按 `.test/verification.yaml` 风险级升级（R2→e2e-short，R3→+e2e-soak，R4→+release-soak）
- **验证执行方**：系统验收由专用测试 agent `stratum-e2e-tester` 执行（封装 stratum-e2e-development skill，定义见 `.claude/agents/stratum-e2e-tester.md`）；测试编写/设计/覆盖分析用 `agent-skills:test-engineer`
- 保留原有红线（无头浏览器、禁止输出敏感信息、failed/skipped/unreconciled 阻断、禁止绕过 skill 手工拼装 E2E）

- [ ] **Step 2: 校验与重新生成**

```bash
cd /home/yang/go-projects/stratum-doc-cleanup
make agent-instructions
make agent-instructions-check
npx markdownlint --config .markdownlint.json docs/agent/instructions.md CLAUDE.md AGENTS.md
```

- [ ] **Step 3: 提交**

```bash
git add docs/agent/instructions.md CLAUDE.md AGENTS.md
git commit -m "docs(agent): 重构端到端测试机制（测试门槛 + 专用测试 agent 说明）

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 6: 创建 stratum-e2e-tester agent（本地，不入库）

**Files:**

- Create: `.claude/agents/stratum-e2e-tester.md`（`.claude/` 被 gitignore，**本地配置，不随 git 提交**）

**Interfaces:**

- Consumes: Task 5 写入 instructions.md 的机制描述；`stratum-e2e-development` skill（`.claude/skills/stratum-e2e-development/SKILL.md`）
- Produces: 可被主 agent 用 Agent 工具派发的测试 agent（name: `stratum-e2e-tester`）

- [ ] **Step 1: 写 agent 定义**

创建 `.claude/agents/stratum-e2e-tester.md`：

```markdown
---
name: stratum-e2e-tester
description: 在 Stratum 功能开发完成、Bug 修复验证、或 PR 前系统验收时使用。封装 stratum-e2e-development skill，独立执行端到端验证（后端/前端/数据库/Agent 链路），产出结构化验证报告。不用于需求分析或方案设计。
---

# Stratum E2E Tester

你是 Stratum 的端到端验证执行 agent，职责是独立完成系统验收并产出结构化报告。

## 操作手册
严格遵循 `stratum-e2e-development` skill 的完整工作流程（用 Skill 工具调用，或读 `.claude/skills/stratum-e2e-development/SKILL.md`）：
1. 从任务描述与代码契约推导验收标准
2. 按 `.test/verification.yaml` 确定风险级别（R0-R4）
3. 执行对应层级验证：后端（启动服务 + 真实 API）、前端（无头浏览器）、数据库（写入/读取/约束/迁移）、Agent/Tool 链路
4. 失败后循环修复验证，直到目标闭环

## 输入
主 agent 派发时传入：变更范围（改动文件清单）+ 验收标准 + 风险级别提示（如有）。

## 输出（结构化验证报告）
- 验证用例清单（按风险排序）
- 每用例证据：HTTP 响应、UI 断言、DB 记录、trace
- 结果：passed / failed / skipped / unreconciled
- 清理结果：启动的服务已停止、临时脚本已删除、残留实体说明

## 红线（继承 skill）
- 禁止输出 token、cookie、密钥、密码、原始 API key
- 远端 k3s/生产只读；写入操作必须获用户明确许可
- 临时脚本用 `tmp-` 前缀，完成前必须删除
- 自己启动的进程完成前必须停止

## 规则
- 验证驱动：先确认验收标准，再启动服务
- 不能只断言 toast；每个 E2E 用例至少包含 UI 断言 + HTTP 断言 + 持久化证据
- 浏览器只用无头模式；禁止有头浏览器
- failed/skipped/unreconciled capability、清理失败、残留实体必须阻断
```

- [ ] **Step 2: 派发 smoke test 验证 agent 可用**

用 Agent 工具派发 `stratum-e2e-tester`，传入最小验证任务（如"验证 `make test-verify-plan` 可运行"），确认 agent 能被识别并返回结构化报告。失败则修正 frontmatter（如 description 触发条件）。

---

### Task 7: 全量验证与提交

**Files:**

- 验证对象：全部改动

- [ ] **Step 1: 静态验证**

```bash
cd /home/yang/go-projects/stratum-doc-cleanup
make agent-instructions-check          # 生成物与源一致
npx markdownlint --config .markdownlint.json docs/ CLAUDE.md AGENTS.md
git status                              # 确认无意外文件
```

- [ ] **Step 2: 交叉引用完整性**

```bash
grep -n "docs/agent" CLAUDE.md AGENTS.md | grep -oE 'docs/agent/[a-z-]+\.md' | sort -u   # 提取索引引用
for f in $(上一步输出的文件清单); do [ -e "$f" ] || echo "MISSING: $f"; done              # 逐一确认存在
```

预期：无 MISSING 输出；已删除/归档的文档不在 CLAUDE.md 索引中。

- [ ] **Step 3: R3 验证闭环**

```bash
cd /home/yang/go-projects/stratum-doc-cleanup
make test-verify-before-pr
```

预期：R3 路径（docs/agent/**、CLAUDE.md）触发 e2e-short；失败则修复文档后重跑，直到通过。

- [ ] **Step 4: 最终提交与推送**

确认所有 commit 已就位，推送分支：

```bash
git push -u origin feat/doc-cleanup
gh pr create --base main
```

PR 描述：What（清洗 CLAUDE.md 文档体系 + 重构 e2e 测试机制）、Why（文档与系统现状脱节）、HowToTest（agent-instructions-check、markdownlint、R3 验证闭环）。

---

## 自审记录

- **Spec 覆盖**：设计文档四节（评估基准/清洗执行/e2e 重构/验证交付）+ Git 工作流均有对应 Task；成功标准 6 条可映射到 Task 2-7 的验证步骤。
- **占位符扫描**：Task 3/4 依赖 Task 1 报告的过时点清单（属计划内依赖，非占位符——报告是已定义产出的输入）；无 TBD/TODO。
- **类型一致性**：报告路径、agent 名、skill 路径在各 Task 间一致（`2026-08-25-claude-md-cleanup-audit.md`、`stratum-e2e-tester`、`.claude/skills/stratum-e2e-development/SKILL.md`）。
