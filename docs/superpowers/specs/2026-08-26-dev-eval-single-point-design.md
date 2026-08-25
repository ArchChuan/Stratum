# 开发态单点评测基线：开发 / 测试 / CI 三阶段接入

日期：2026-08-26
状态：设计（待 review）

## 1. 背景与目标

### 现状问题

Stratum 有 4 类可评测资源：**skill / agent / mcp / knowledge**。它们的参数（模型、工具集、指令、分块策略、embedding）是**可变的**，任何一处的改动都可能引入回归——但当前没有一套贯穿「开发 → 测试 → CI」的统一单点评测手段：

- 已有 `cmd/e2e-rag-check`：RAG 检索的独立 golden 评测 CLI（golden 数据集 + 基线对比 + 显式录制，退出码 0/1/2），但只覆盖 knowledge 检索一个维度。
- 已有 `internal/evaluation`：**运行态**针对可变配置做优化的评测运行时（suite/baseline/run/worker/experiment/promotion）——解决「线上配置该不该换」的决策问题，与「开发期防止单点回归」目标不同，**不复用**。
- 测试/验收流程（`stratum-e2e-development` → 现为**测试 agent 驱动的 PR 前验收**）对 RAG 检索有 R3 风险触发的非阻塞抽查，但不是全部资源的正式 gate。

### 目标

在开发、测试、CI 三阶段接入**开发态单点评测**：输入一个配置快照点 → 运行固定的 golden 评测集 → 产出指标 → 对比录制基线 → 回归时拦截并允许用户介入（修复 / 接受回归 / 重录基线）。

### 非目标

- 不复用 `internal/evaluation` 运行时（运行态优化 vs 开发态回归，解决的问题不同）。
- 不做「全资源无差别评测」——只评测**变更触及的配置快照点**（单点评测的本质）。
- 不把评测逻辑塞进产品代码（`internal/`）——CLI 走真实 HTTP，指标用纯函数，产品代码不加评测特判（防过拟合红线）。
- CI 不跑依赖真实 LLM 的评测（skill/agent 真实执行），只跑确定性 kind。

## 2. 核心概念

**开发态单点评测**（Dev Single-Point Evaluation）：

```
单点配置快照 → 固定 golden 评测集 → 指标 → 对比录制基线 → 回归拦截 / 用户介入
```

- **单点（point）**：一份资源配置快照 + 对应的 golden 评测集 + 基线的组合。资源参数可变，因此评测以「点」为单位，而非抽象地评测整个资源。
- **golden 评测集**：针对某个 point 的固定用例集（cases.yaml），用例由人工标注，是「该配置下应当表现正确」的行为契约。
- **基线（baseline）**：某个 point 在历史 commit 上的录制指标，作为回归对比锚点。
- **拦截与介入**：当前 run 相对基线显著回退时，退出码非零；用户三选一——修复 / 接受回归（写 reason）/ 重录基线（显式确认）。

## 3. 架构

### 3.1 统一 CLI：`cmd/e2e-eval-check`

推广 `cmd/e2e-rag-check` 的独立 golden 模式到全部资源，统一入口 + `--kind` 分派：

```bash
cmd/e2e-eval-check --kind <skill|agent|mcp|knowledge> \
  --point <point-key> [--output <report.json>] \
  [--warn-delta 0.1] [--fail-on-warn] \
  [--record-baseline --confirm-record] \
  [--skip "<reason>"]
```

- `--kind` 决定 executor；每个 executor 有自己的 golden 数据集格式与执行路径，共享统一的分派、报告、基线、拦截骨架。
- `--point` 定位单点：`points/<point-key>.yaml` 声明资源配置快照 + 引用 golden 数据集与基线文件。
- 退出码沿用：`0` passed / `1` failed（缺陷或 WARN gate）/ `2` infra_failed（环境/auth/provider）。
- `--skip` 必须带 reason，产出 `not_run` 报告，禁止伪装成绿。
- 基线重录沿用 fail-closed gate：`--record-baseline` 必须配 `--confirm-record`。

### 3.2 目录结构

```
test/e2e/
  <kind>/                      # skill / agent / mcp / knowledge
    points/<point-key>.yaml    # 单点配置快照：资源配置 + 引用 golden 集与基线
    golden/cases.yaml          # 用例与标注（含 snapshot 段）
    golden/documents/          # knowledge 的源文档；其余 kind 视需要
    golden/README.md           # 用例设计原则（标注说明、防过拟合红线）
    baselines/<point-key>.json # 录制基线
```

`points/<point-key>.yaml` 示例：

```yaml
kind: knowledge
snapshot:
  embedding_model: embedding-3
  query_mode: hybrid
  top_k: 5
  chunk_size: 512
  reranking: ""
  query_rewrite: none
golden: golden/cases.yaml
baseline: baselines/<point-key>.json
```

### 3.3 `--kind` 分派

| kind | executor 执行路径 | 断言模式 | 指标 |
|---|---|---|---|
| knowledge | 复用 `e2e-rag-check` 逻辑：provision 临时 workspace → 灌入源文档 → 走 HTTP `/knowledge/query` → 检索评估 | 标注相关/引用/无答案 | recall@k、precision@k、mrr、ndcg、relevant_rate、citation_pass_rate、no_answer_pass_rate |
| mcp | 走 HTTP 调用 MCP 工具，对工具返回做字符串断言 | exact / contains / regex | pass_rate、avg_latency、avg_cost |
| skill / agent | 走 HTTP 执行（`/skills`、`/agents/:id/execute`），对输出做断言或 judge | exact / contains / regex / judge | pass_rate、judge_mean、avg_latency、avg_cost |

### 3.4 knowledge executor 迁移策略

`e2e-rag-check` 的 knowledge 逻辑**先并存、后删除**迁移进 `e2e-eval-check --kind knowledge`：

1. 新 CLI 的 knowledge executor 复用 rag-check 的 HTTP client、指标纯函数、报告结构（不重写）。
2. 迁移期间两个入口并存；skill / 测试 agent 的引用点切到新 CLI。
3. 迁移完成后删除 `cmd/e2e-rag-check`，存量 `test/e2e/knowledge/retrieval/` 数据集原位接管，不搬移。

## 4. 评估方法与指标

### 4.1 cases.yaml 格式（统一）

```yaml
version: 1
snapshot:            # 配置快照声明（与 point 的 snapshot 一致，用于指纹校验）
  ...
cases:
  - id: <unique-id>
    input: <输入：query / 指令 / 工具调用参数>
    # 断言型（exact / contains / regex）：声明期望
    expected_output: <期望输出或期望的子串/正则>
    assertion_mode: contains
    # judge 型：声明判题规格
    judge_spec:
      criteria: <判定标准>
    expect_no_answer: false   # knowledge 专用，其余 kind 忽略
    note: <标注理由，供 review>
```

knowledge 的 `cases.yaml` 沿用现有字段（`query / mode / relevant_documents / citation_documents / expect_no_answer / note`），不另起格式。

### 4.2 断言模式

- **exact**：输出与期望完全相等。
- **contains**：输出包含期望子串（适合 mcp 工具返回、skill/agent 输出中的关键事实）。
- **regex**：输出匹配正则（适合版本号、ID、时间格式）。
- **judge**：真实 LLM 判题——给输出 + criteria，judge 返回通过/不通过及分数。仅 skill/agent 使用，mcp 与 knowledge 走确定性断言。

### 4.3 统一指标

- **pass_rate**：断言通过用例 / 总用例（所有 kind 通用，是主回归信号）。
- **judge_mean**：judge 用例的平均分（skill/agent）。
- **avg_latency、avg_cost**：执行成本观测（不参与回归判定，仅记录）。
- knowledge 保留 RAG 专属指标（recall@k / mrr / ndcg / citation_pass / no_answer_pass），其 `pass_rate` 由各指标汇总得出。

### 4.4 统一执行流水线

```
load dataset → provision（临时资源/workspace，probe 外部依赖）→ execute cases
→ judge（必要时）→ aggregate → compare baseline → [record baseline] → report + exit code
```

失败时 deferred cleanup + report write 照常执行，残留实体进报告。

## 5. 三阶段接入

评测以「变更相关资源的全部单点评测」为内容，由 PR 前验收主承载；CI 只跑确定性子集；开发态保留手动打点入口。

| 阶段 | 触发者 | 范围 | 模型 | gate |
|---|---|---|---|---|
| ① 开发态 | **人主动**（`make eval-dev`） | 单个资源即时打点 | 真实 LLM（全 kind） | 非流程、非 gate，随点随跑 |
| ② 测试态（PR 前，主承载） | **测试 agent 驱动的验收流程**自动注入（`test-verify-before-pr.sh`） | 变更相关资源的全部单点评测 | 真实 LLM（全 kind） | 拦截 + 三选一，失败阻断 PR 前验证 |
| ③ CI | CI job 自动 | 确定性 kind（mcp + knowledge） | 确定性断言 | 硬门槛，`--fail-on-warn` 开启；skill/agent 显式 `not_run` |

### 5.1 ① 开发态：可选手动打点

- 入口 `make eval-dev --kind <kind> --point <point>`，开发中改完想立刻看单点效果时使用。
- 同一 CLI，不设 gate、不阻断流程；评测动作已由 ② 承载，此入口仅保留即时反馈。

### 5.2 ② 测试态：PR 前主承载

- 接入点：`test-verify-before-pr.sh` 的 **run-planned-checks 之后、浏览器模式之前**（确定性检查先行、重活在后）。
- 触发：测试 agent 驱动的 PR 前验收流程调用脚本，脚本自动注入评测 step——无人值守，防「人忘了跑」。
- 范围：`.test/verification.yaml` 新增 `id: eval-touched` 规则，按文件路径命中变更相关资源。命中集合 = 现有 `agent-tool-chain` 规则覆盖的资源路径（`internal/agent/**`、`internal/mcp/**`、`internal/skill/**`、`internal/knowledge/**`、`test/e2e/**`）∪ 评测目录（`cmd/e2e-eval-check/**`、`test/e2e/<kind>/**/golden/**`、`test/e2e/<kind>/points/**`）。
  - 命中即执行对应 point 的全部 golden cases（4 kind，真实 LLM）。
- 失败语义：任何 point 回归 → 整个 PR 前验证流程不通过，三选一后才放行到浏览器模式、才出 local report。

### 5.3 ③ CI：确定性子集硬门槛

- 新增 `eval` job（或并入现有 `test` job），与静态检查并行、独立 fail。
- 只跑 mcp + knowledge（确定性断言，无真实 LLM 依赖）；skill/agent 标记 `not_run`（`--skip` 带固定 reason），禁止伪装成绿。
- `--fail-on-warn` 开启，WARN 也阻断合并。

### 5.4 拦截与用户介入（② 的主交互）

回归（run < baseline - delta）时拦截，用户三选一：

1. **修复**——改产品代码后重跑。
2. **接受回归**——写 reason（metric、delta、commit），**持久化到基线文件的 `accepted_regressions[]`**，作为显式、可审计、跨运行的放行。持久化的原因：若只写进单次报告，下次运行仍回归、仍要求三选一，接受的放行不生效。
3. **重录基线**——`--record-baseline --confirm-record`；fail-closed gate 要求显式双 flag，禁止隐式更新基线。

## 6. 错误处理与 fail-closed

| 场景 | 行为 |
|---|---|
| 退出码 | 0 passed / 1 failed（缺陷或 WARN gate）/ 2 infra_failed（环境/auth/provider） |
| infra vs defect | 分离——provider/环境失败算 infra，不误判为产品回归 |
| `--skip` | 必须带 reason，产出 `not_run` 报告，禁止伪装成绿 |
| 基线缺失 | 显式区分「首次录制」（允许）vs「corrupt baseline」（报错） |
| 重录基线 | fail-closed gate：`--record-baseline` 必须配 `--confirm-record` |
| 失败传播 | 任何一步失败向上传播；deferred cleanup + report write 照常执行，残留实体进报告 |
| 接受回归 | `accepted_regressions[]` 持久化到基线文件，必须写 reason、metric、delta、commit——可审计、跨运行放行，不留静默路径 |

## 7. 报告格式

统一 JSON Report（从 `e2e-rag-check` 的 Report 推广到全 kind）：

- 继承现有字段：`status / snapshot / config / provider / cases / aggregate / baseline / baseline_delta / warnings / non_comparable / skip_reason / residual_entities / evidence`。
- **新增字段**：`kind`、`point`、`accepted_regressions[]`（从基线文件镜像当前已知接受的回归，权威落点是基线文件——见 5.4 第 2 条）。
- 路径：`tmp/eval-reports/<point>-<commit>.json`。
- 定位：local report 是 developer audit assertion，不是 GitHub trusted status（与 `.test/verification.yaml` 的 authority 模型一致）。
- 序列化约束：数组字段 normalize 为空数组，不 emit `null`（沿用 rag-check 的 schema 约束）。

## 8. 测试策略（评测工具自身的测试）

复用 `e2e-rag-check` 已验证的测试模式，不另起炉灶：

- **复用**：fake retriever mock HTTP、纯指标函数表驱动手算、fail-closed gate 测试、golden dataset 加载校验。
- **新增**：`--kind` 分派与 flag 解析、各 kind executor 的 mock、`accepted_regressions` 写入校验、统一 Report 序列化。
- **不动契约**：CLI 走真实 HTTP，不触碰 `proto/` 与 `contract_test.go`；指标复用 `internal/knowledge/application/metrics.go` 纯函数。

## 9. 评测集的与时俱进

golden 评测集必须随产品演进，否则退化为「用旧答案评新产品」。四层机制：

1. **配置指纹 → 过时显式化（核心）**：point 把资源配置指纹化（skill 工具集、agent 指令/模型、mcp 工具列表、knowledge embedding/分块参数）。配置一变、指纹不匹配 → 基线对比强制标 `non_comparable` → 拦截并要求人工决策。旧 case 无法静默伪装成「评测通过」。
2. **产品改动 = 同步改评测（流程强制）**：资源改动走测试 agent 验收流程，其 checklist 含「评测集同步更新」项——新增覆盖新行为的 case、修正过时标注、删除废弃场景。评测集变更随 PR 一起 review（cases.yaml 的 diff 是审查对象，git 历史是演进日志）。
3. **case 过时 vs 产品回归，两条路径禁止混用**：基线对比出大 delta 时先判断性质——产品回归 → 修复产品；评测集过时 → 先改 case（标注/预期/新 case）→ 再重录基线。**重录基线永远发生在 case 已 review 之后**，禁止用 `--record-baseline` 掩盖 case 过时。
4. **可审计 + 防过拟合**：每个 case 写 `note` 标注理由；负面 case（no-answer）随产品演进 review 是否仍成立。golden 只度量、不驱动实现；禁止为过测扭曲产品代码。

兜底：即使机制没拦住，`baseline_delta` 的累积偏离会暴露「评测集或产品在漂」，回到第 3 条判断性质。

## 10. 测试 agent 提示词更新（本地配置交付项）

PR 前验收由测试 agent 驱动，其提示词需加入评测执行与治理指令，否则 CLI 与 gate 无人调用：

1. **执行评测 step**：验收流程里 `test-verify-before-pr.sh` 的评测环节必须执行，失败按三选一处理，禁止静默放行。
2. **回归三选一**：判断性质——产品回归 → 修复；评测集过时 → 先改 case 再重录基线；确实接受 → 写 reason 入 `accepted_regressions[]`。
3. **评测集同步审查**：审查变更 PR 时，若涉及评测覆盖的资源，检查 cases.yaml 是否同步更新；把「评测集 diff」当作 review 对象。
4. **基线与 case 分离**：重录基线永远在 case 已 review 之后；`--record-baseline` 必须配 `--confirm-record`，不静默。

**落点**：验收测试 agent 的定义文件在本地 `.claude/`（gitignore，不进 git/CI）。本交付项是**本地配置变更**——改了提示词测试 agent 即生效，不随 PR 合入，不需要 CI 感知。具体定义文件路径在落地时定位。

## 11. 交付项清单

### git 交付项（随 PR 合入）

1. `cmd/e2e-eval-check/`：统一 CLI（flag 解析、`--kind` 分派、统一 Report、基线对比、fail-closed gate、`accepted_regressions`）。
2. knowledge executor 复用并迁移 `e2e-rag-check` 逻辑；迁移完成后删除 `cmd/e2e-rag-check`。
3. `test/e2e/<kind>/points/`、`test/e2e/<kind>/baselines/` 目录与首个 point 示例。
4. `test/e2e/mcp/`、`test/e2e/skill/`、`test/e2e/agent/` 的首期 golden 评测集（按本设计第 4 节格式）。
5. `scripts/quality/test-verify-before-pr.sh`：在 run-planned-checks 后注入评测 step。
6. `.test/verification.yaml`：新增 `eval-touched` risk rule。
7. `.github/workflows/ci.yml`：新增 `eval` job（mcp + knowledge 确定性，skill/agent `not_run`）。
8. `Makefile`：`eval-dev` / `eval-pr` / `eval-ci` 目标。
9. 评测工具自身的测试（第 8 节）+ 文档（各 kind 的 `golden/README.md`）。

### 本地配置交付项（不进 git/CI）

1. 验收测试 agent 提示词更新（第 10 节）。

## 12. 成功标准

- 开发态：改某个资源后 `make eval-dev --kind <kind> --point <point>` 能即时反映该单点效果。
- 测试态：PR 前验收自动评测变更相关资源的全部单点；回归时阻断并提供三选一；接受回归需显式 reason。
- CI：mcp + knowledge 确定性评测成为硬门槛，`not_run` 显式可查；skill/agent 不因真实 LLM 依赖造成 CI flaky。
- 评测集：资源配置或产品行为变更时，`non_comparable` 或拦截显式触发，无静默过时；基线重录全部走 `--record-baseline --confirm-record`。
- 迁移：`cmd/e2e-rag-check` 逻辑完整迁入 `e2e-eval-check --kind knowledge` 后，旧 CLI 删除，存量数据集原位接管，无行为回归。
