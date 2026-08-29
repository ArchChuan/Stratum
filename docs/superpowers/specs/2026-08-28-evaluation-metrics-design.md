# 运行态评测与评测指标体系设计

日期：2026-08-28
状态：设计（待 review）

## 1. 背景与目标

### 1.1 核心论点

**整个平台的参数都是可变的，修改参数就相当于修改代码。** 平台参数通过版本化快照热更新（`internal/parameters`：draft → publish → rollback），变更即时生效。这意味着：

- 每次参数变更都是一次"发布"，都可能引入能力回归，且**无需等待代码上线流程**；
- 参数的"好与坏"只能在**真实运行效果**上评判，不能靠静态审查；
- 因此需要**运行态评测**：持续观测生产流量下参数的真实效果；
- 评测需要**全链路可观测**：从用户输入到检索/工具/LLM 每一步都可归因；
- 需要定义**评测指标体系**：统一衡量"质量 / 安全 / 价值 / 成本"的多维语言。

### 1.2 与既有评测能力的边界（关键）

平台已有两条评测通道，本设计**与之互补、不替代**：

| 通道 | 定位 | 载体 | 本设计的关系 |
|---|---|---|---|
| `cmd/e2e-eval-check`（2026-08-26 设计） | **开发态/测试态/CI 的离线回归**：固定 golden 评测集 + 录制基线 + 变更拦截 | `test/e2e/<kind>/golden/cases.yaml`（YAML 文件，CLI 驱动） | 互补。它是"开发前门禁"，本设计是"上线后观测" |
| `internal/evaluation`（suite/baseline/run/worker/experiment） | **运行态参数优化**：评测集评测 + 基线/金丝雀实验 + 候选/审批/进化 | DB 持久化 `EvalSuiteRevision`（HTTP 驱动） | **复用**。本设计的判定闸门就落在它上面 |

**本设计的定位修正（经确认）：**

- **运行态评测 = 观测层**：监控 + case 收集来源。持续观测生产流量，产出信号/指标/告警（监控）和候选 case 池（素材），**不做权威判定**。
- **真正评测 = 评测集评测**：判定闸门。case 来自批量生成或自造，批量跑评测集，产出多维指标结果和评测归因。
- **门禁 = 动作层**：消费信号与判定，做出拦截/告警/回滚。
- **归因与改进 = 决策支持层**：消费信号，产出根因与建议。

### 1.3 目标

在"运行态观测 → 评测集判定 → 门禁动作 → 归因改进"的分层上，建设完整的评测能力：指标体系、运行态观测、评测集生成录入、评测集评测（多维指标+归因）、配置版本绑定、分层门禁、归因与改进、展示分析、评测自身运行监控。

### 1.4 非目标

- 不替代 `cmd/e2e-eval-check` 的开发态回归（保持互补）。
- 不新建平行于 Opik 的证据存储（Opik 是证据权威，评测结果作为 trace 属性挂回）。评测 DB 只存**控制面聚合状态**（同 PostgreSQL 控制面语义），证据与 payload 权威仍是 Opik + MinIO。
- 运行态 judge 不做权威判定（受控的评测集评测才是判定出口）。
- 不自动执行不可逆清理或自动改代码；自动动作仅限参数版本化回滚（可审计）。

## 2. 定位与总体架构

### 2.1 分层架构

```
 运行态观测层（监控 + case 收集）
   规则护栏(内联真动作) · 采样观测(judge/行为/主动分层) · 判异采集
   → 信号/指标/告警（监控） · → 候选 case 池（喂给评测集）
                │
                ▼
 case 来源 ──→ 评测集（draft → 人工审核 → publish 版本化回归基线）
   自造 · 反馈采样 · KB派生 · 运行态沉淀 · 失败提炼
                │ EnqueueRun（批量跑 EvalSuiteRevision）
                ▼
 真正评测（评测集评测 = 判定闸门）
   规则 assertion（exact/contains/regex）+ judge assertion（rubric）
   → 多维指标结果 · 评测归因（同集版本对比 / case聚类 / trace下钻）
                │
    ┌───────────┼───────────────┐
    ▼           ▼               ▼
 分层门禁     归因与改进       展示分析
 (动作层)    (决策支持层)     (视图层)
```

观测与判定产出按风险**上升人类**（§4.5）：飞书告警（T2）→ 人工评审池（T3）→ 平台参数强制人工（T4）。

### 2.2 方案选型：独立评测管线（旁路评分器）

评测逻辑与执行链路解耦：

- **规则护栏内联**（快路径）：确定性、零延迟，在 agent 执行链路内同步执行（安全/格式/PII/注入）。
- **judge / 行为 / 归因全部异步离线**：执行完成后仅经 NATS 发轻量引用事件（trace_id），评测服务从 Opik 拉取证据（评测/实验 trace 100% 保留），采样后打分。
- 数据来源**复用现有证据链**（Opik trace + 参数版本快照 + feedback 事件），不重复造证据管线。

理由：不侵入热路径（除规则护栏）、judge 可独立校准/换模型/加维度、采样率可控、门禁分层结构天然对应、judge 故障不阻断执行（评估器不阻断执行铁律）。

## 3. 评测指标体系

### 3.1 多维指标（三源 + 成本性能）

| 维度组 | 指标 | 来源 | 判定方式 |
|---|---|---|---|
| **语义质量（judge）** | faithfulness / relevance / completeness | LLM judge 按 rubric 打分 | 采样（运行态）/ 全量（评测集） |
| **规则安全（rule）** | safety（注入/越权）/ format / PII 命中 | 确定性规则 | 内联即时 |
| **行为价值（behavior）** | retry / escalation / abandonment | 用户行为事件（thumbs/升级/放弃） | 异步采集 |
| **过程质量（process）** | tool_pass / step_reasoning / tool_cycle | 工具序列规则断言 + 步骤级 judge（§6.5） | 规则即时 / judge 异步 |
| **成本性能（cost/perf）** | latency / tokens / cost_usd | trace / span 统计 | 全程记录 |

judge 指标定义（业界无参考评测主流口径）：

- **faithfulness（忠实度）**：回答是否严格基于给定上下文，不编造。
- **relevance（相关性）**：回答是否针对用户问题，不答非所问。
- **completeness（完整度）**：回答是否覆盖问题的全部关键点。
- **consistency（稳定性）**：非 judge 指标——同一输入多次执行，输出与推理过程的稳定性（与 temperature 直接相关，运行态多次采样对比）。

### 3.2 参数 × 指标因果矩阵（归因搜索空间）

每个评测指标维度只归因到"声明影响该维度"的参数，7 个参数不全进每次归因：

| 参数 | faithfulness | relevance | completeness | consistency | cost | behavior | process |
|---|---|---|---|---|---|---|---|---|
| system_prompt | ✅ | ✅ | ✅ | | | ✅ | | |
| temperature | | | | ✅ | | | | |
| max_tokens | | | ✅(截断) | | ✅ | | | |
| max_context_tokens | ✅(覆盖) | ✅ | ✅ | | ✅ | | | |
| memory_*_prompt | | | | | | ✅(记忆质量) | | |
| tool 集 / max_iterations | | | | | ✅(多余调用) | | ✅(步骤/循环/工具序列) |

矩阵的落点：`Tunable` 接口需新增**评测归因声明**（见 §9.2），登记时声明影响维度、归因策略、改进方向。

### 3.3 分层报告（辛普森悖论防护）

所有指标按 `resource × 参数版本 × tenant-tier × 难度分层` 聚合报告，禁止只看整体。业界反例：整体 +5% 但困难分层 −10.8%（辛普森悖论）。

### 3.4 参数作用域分层

参数按作用域分两级，归因时区分**来源层级**：

| 作用域 | 载体 | 影响面 |
|---|---|---|
| **平台参数** | `internal/parameters` 的 `platform_config_versions`（两级回退解析的 platform 层） | **全租户** |
| **租户资源参数** | 租户自己 Agent/Skill/MCP/Knowledge 的资源快照（model / 工具集 / 指令 / 分块 / 检索 / 提示词） | 单租户 / 单资源 |

同一参数（如 system_prompt）在平台层有默认值、在租户资源层可覆盖。**评测观察须记录每个关键参数的实际生效来源层级**（platform 默认 vs resource 覆盖），否则无法区分"平台改动影响"与"租户配置叠加"。

### 3.5 评测点（Evaluation Point）与分开评测

**评测点 = 平台参数快照 × 租户资源配置快照的组合**，是可独立评测、独立基线、独立归因的最小单位：

```go
type EvaluationPoint struct {
    PlatformVersion int64      // platform_config_versions 的 version_seq（platform 层）
    ResourceRef     ResourceRef // 资源 (agent/skill/mcp/knowledge) + 其配置版本（resource 层）
}
```

**分开评测的机制 = 正交控制变量**（因子实验思想）：平台与资源是两个正交维度，分离效应靠"固定一方、对比另一方"：

1. **平台效应隔离**：同一资源配置下，平台版本 V1 vs V2 → 同评测集结果差异 = **平台参数净效应**。
2. **租户配置效应隔离**：同一平台版本下，资源配置 X vs Y → 差异 = **租户配置净效应**。
3. **每个评测观察 / 评测集 Run 记录双版本锚点**（platform_seq + resource_version），归因时按锚点分组。

平台参数发布后无需跑全组合（防组合爆炸）：对**受影响的点**跑评测集回归 + 运行态按点持续观测，每个点都是自身前后对照。

### 3.6 评测集规模分级（成本治理）

评测集评测消耗模型（judge + case 执行），成本必须显式治理。**分层金字塔**：

| 层级 | 规模（每资源） | 触发时机 | 用途 |
|---|---|---|---|
| **哨兵集** | 5–15 case | 每次参数发布 / 门禁判异验证 / 快速回归 | 覆盖关键路径 + 高风险回归点，快且便宜 |
| **标准集** | 30–100 case | 发布验证 / 版本对比 / 例行回归 | 全维度覆盖 |
| **深度集** | 100–300 case | 重大变更 / 专项分析 / 新资源首评 | 边界 + 对抗 + 链路级 |

三层共享 case 分层标签（`tier`），哨兵集 = 标准集的精选子集。按变更粒度决定跑哪级：参数小改 → 哨兵；影响维度多 → 标准；重大重构 → 深度。

**judge 成本控制**：

- judge 用小/便宜模型（`judgeAdapter` 模型可配），独立于执行模型。
- 多维打分一次调用出全部维度，不按维度拆调用。
- **渐进式判定**：规则断言能判定的（exact/contains/regex）不调 judge，只有规则判不了才走 judge。
- 运行态采样而非全量；评测集哨兵级全量、深度级按需抽样。

**持续瘦身**：每个 case 记录成本与价值；基线漂移监控（§11.2 `eval_baseline_drift`）+ 相似 case 聚类去重（保留代表 case）+ 长期冗余 case 归档。评测集"瘦而不漏"是持续治理项。

## 4. 运行态观测层（监控 + case 收集）

### 4.1 规则护栏（内联、快路径）

- 安全/格式/PII 规则在 agent 执行链路内同步执行，命中即**即时拦截**（门禁第 1 层）。
- 零 LLM 依赖、零额外延迟；规则命中同时产出一条评测观察。
- 复用/扩展现有安全护栏能力，规则可配置。

### 4.2 采样观测（异步）

| 模式 | 触发 | 说明 |
|---|---|---|
| judge 采样 | 按 (resource × stratum) 分层随机采样生产 trace | 采样率可配，judge 异步打分 |
| 行为信号 | feedback 事件（thumbs/escalation/abandon） | 被动，用户主动反馈 |
| 主动分层采样 | 定期按 (resource × tenant-tier × outcome) 抽样 | 补被动采样的分布偏差（多数坏 case 用户不反馈） |
| 判异触发 | judge 得分跌阈 / 规则命中 / 行为异常 | 采样进待审核池 |

### 4.3 评测观察 EvalObservation（新领域对象）

```
EvalObservation {
  trace_id, resource(kind, id),
  param_version: {                            // 双版本锚点（§3.4/§3.5/§7）
    platform: { group_key, version_seq },      // 平台层生效版本
    resource: { ref, version },                // 租户资源配置版本
    source: platform | resource | both,        // 关键参数实际生效来源层级
  },
  signals: { rule: [...], judge: {dimension: score, confidence}, behavior: {...} },
  cost_perf: { latency_ms, tokens, cost_usd },
  stratum: tenant_tier,                        // 分层
  verdict: pass | flag | block,                // 仅信号级结论，非权威判定
  created_at
}
```

存储：评测 DB（明细，按参数版本聚合）+ Prometheus（滚动/告警）+ 证据挂回 Opik（trace 属性）。

### 4.4 产出

- **监控信号**：指标 + 告警（能力健康监控，见 §11）。
- **候选 case 池**：判异/异常的交互 → 待审核池 → 人工确认 → 评测集（§5 产线之一）。

### 4.5 上升人类与飞书报警

运行态评测的本质是**监控报警机制**：观测持续跑，人类只在需要时介入。信号按风险分级**上升人类**：

| 级别 | 信号 | 处置 | 是否打扰人 |
|---|---|---|---|
| T1 | 规则护栏命中 | 自动即时拦截（fail closed） | 不打扰（计数进指标） |
| T2 | judge 跌阈 / 行为异常 / 多租户分化 | **告警**：Prometheus → Alertmanager → **飞书** + 评测中心告警队列 | 通知，人工到操作台处理 |
| T3 | 低置信 / 判定分歧 / 副作用越界 / 复杂 case | **上升评审池**（§6.6），飞书通知 reviewer | 必须人工 review 才放行 |
| T4 | 平台参数判异（红线） | 强制人工确认 + 事前哨兵回归 + 事后多租户验证（§8 L3+） | 强制人工 |

**飞书报警接入**：复用 `docs/agent/observability.md` 的 Alertmanager → 飞书适配器，不新建平行通道。评测新增告警规则挂到既有飞书 route：

- 评测判异 / 平台参数判异 / 多租户分化 → 飞书通知，带评测中心链接（直达具体 run / 评审池条目）。
- 评审池新增待审 → 飞书通知 reviewer；评审池积压超标（`eval_review_backlog`）→ 飞书告警。
- 告警必须带 runbook_url（衔接 observability.md 由 `scripts/quality/monitoring-config-test.sh` 守卫的要求）。

**上升路径是单向的**：T3/T4 上升后未处理前，相关门禁保持"待确认"状态，禁止自动放行（fail closed，衔接 §14）。

## 5. 评测集生成与录入

### 5.1 生成产线（批量生成 + 自造）

| 产线 | 机制 | 现状 |
|---|---|---|
| 自造-手工录入 | 单条表单（UpdateDraftCase） | ✅ 已有 |
| 自造-批量导入 | JSON/CSV 批量 | 🆕 |
| 批量-反馈采样 | 生产反馈 → 采样 → LLM 生成 → 去重质检 | ✅ 已有（`CaseSampleSource` + `TestCaseGenerator` + `POST /suites/:id/generate`） |
| 批量-KB 派生 | 知识库 chunk → QA 对（RAG 用例） | 🆕 |
| 批量-运行态沉淀 | 运行态判异 case → 待审核池 → 人工确认 | 🆕 |
| 批量-失败提炼 | 门禁拦截 / 告警命中失败 case → 提炼 | 🆕 |

### 5.2 质量控制

- 所有自动生成的用例带 **provenance**（`SourceTraceID / FeedbackRef / GenerateReason`，已入 `evaluator_config` JSONB），可审计。
- **generator 永不自动 publish**（已有约束）：生成进 draft，人工审核后才 publish 为回归基线——防止自动污染。
- 新用例进评测集需 review（pending → approved）。
- **回归基线不宜频繁变更**：基线漂移率本身是监控指标（§11.2），基线频繁变动 = 评测失效信号。

## 6. 评测集评测执行（真正评测 / 判定闸门）

### 6.1 执行

`EnqueueRun`（已有）：对 **评测点（§3.5）× EvalSuiteRevision × tier（§3.6）** 批量跑——Run 记录**双版本锚点**（platform_seq + resource_version）。断言：规则 assertion（exact/contains/regex）+ judge assertion（按 rubric，输出多维度分数）。

**覆盖范围原则（回应"全链路覆盖"的顾虑）**：评测集按**链路环节**建能力（agent 执行链 / 检索链 / 记忆链 / 工具链各有一套 case，见 §6.4），但**每次执行不跑全链路**——按变更点选最小必要集：参数改了什么环节，就跑该环节的哨兵子集；链路级 case 只进深度集。

- **同参数版本 × 同 case 结果缓存**：版本快照不可变 → 结果可缓存复用，重复发布不重跑。
- **离线链路 case 走管道直调**（§6.4），不跑完整对话。

### 6.2 多维指标结果（EvaluationRun 扩展）

现有 per-case 只有 `passed/tokens/cost/duration`，扩展为按维度聚合：

```jsonc
run.metrics = {
  overall_pass_rate,
  by_dimension: {              // §3.1 指标体系逐维度
    faithfulness: { avg_score, pass_rate, samples },
    relevance: { avg_score, pass_rate },
    completeness: { avg_score, pass_rate },
    safety: { pass_rate }, format: { pass_rate },
    tool_pass: { pass_rate }, step_reasoning: { avg_score },  // process（§6.5）
    behavior: { retry_rate, escalation_rate, abandonment_rate },
  },
  by_category: { normal, boundary, adversarial },  // case 分类
  cost: { total_usd, avg_usd },
  latency: { p50, p95, max },
  version: { suite_revision_id, platform_seq, resource_version },  // 双版本锚点（§3.5）
}
// 单 result 增加：
result.dimensions = { faithfulness: {score, passed, reason, confidence}, ... }  // confidence 低 → 进人工评审池（§6.6）
result.failure_reason = "dimension:faithfulness | assert:regex | span:retriever"
```

### 6.3 评测归因（评测集归因，比运行态更精确）

评测集归因有**受控性优势**（case 固定，变量只有参数版本），是参数归因的第一优先级证据：

1. **同集版本对比归因**——同一评测集跑 V1 与 V2，逐维度指标差异表。`faithfulness` 掉 12% → 归因到 V1→V2 的改动参数。受控实验天然排除运行态混杂。
2. **case 聚类归因**——失败 case 按 `失败类型 × 维度 × 资源` 聚类 → 系统性根因（"faithfulness 批量失分 → 检索或 prompt 问题"）。
3. **trace 组件级下钻**——单个失败 case → trace 哪个 span 劣化（retriever/tool/LLM 截断）。

输出：**失败归因报告** = 哪个维度、哪些 case 聚类、根因假设、建议方向（接入 §9 改进闭环）。

### 6.4 离线链路评测（记忆 / knowledge 管道）

记忆是**异步离线管道**（outbox → embed → enrich → summary），没有同步"输入→输出"可断言。评测的关键抽象：**把管道的每个阶段看成"输入 → 产物"的变换，评测集对变换结果做断言**，不依赖在线对话：

| 管道阶段 | 评测集 case | 断言 |
|---|---|---|
| 提取（entities/facts） | 会话文本 → 期望提取的实体/事实/重要片段 | recall / precision 或包含断言 |
| 摘要（summary） | 对话 + 现有记忆 → 期望摘要要点 | judge faithfulness / 要点覆盖 |
| 检索（retrieve） | 查询 → 期望命中记忆 | 与 knowledge 检索评测同构（recall@k / mrr） |
| 丰富 / 触发 | 消息 → 期望触发的记忆/丰富内容 | 包含 / 触发断言 |

**执行方式（按成本升序）**：

1. **管道直调**（首选）：评测 runner 喂会话文本 → 直接触发记忆管道 → 断言产物。异步链路变同步评测，成本最低。
2. **回放驱动**：从运行态 trace 截取会话片段 → 重放进管道 → 对比实际产出（运行态观测喂 case）。
3. **链路级**：构造多轮对话场景，断言"后续轮是否正确使用记忆"（少量，只进深度集）。

观测层补充记忆信号：记忆产物质量对比（当评测集有对应版本时）、下游记忆使用率、用/不用记忆的 judge 对比（判异信号）。已有 `memory_*` 指标（embed/enrich/summary 计数）继续监控管道健康。

### 6.5 多步推理与工具调用评测

agent/skill 是多步推理 + 工具调用的链路，评测**不能只看最终输出**——"结果对但过程错"（用了禁用工具、多余调用、循环调用）同样是缺陷，且可归因到具体步骤。

**工具调用的"门道"（需覆盖的失败模式）**：

| 失败模式 | 例子 | 检测 |
|---|---|---|
| 工具选择错 | 该查知识库，调了通用搜索 | must_call 断言 |
| 参数构造错 | 检索参数、写操作参数错 | 步骤级 judge / 参数断言 |
| 顺序错 | 先写后查（应反序） | order 断言 |
| 多余调用 | 一次能完成的调了 3 次 | max_calls 断言 |
| 循环/死锁 | 反复调同一工具不收敛 | 调用次数上限 + 运行态 tool_cycle 率 |
| 副作用越界 | 只读任务里调了写工具 | must_not_call 断言（写操作/危险工具） |
| 失败处理差 | 工具失败后不降级、不告知 | 步骤级 judge |

**评测集 case 扩展**（除 input / expected_output 外）：

```yaml
case:
  input: "..."
  expected_final: "..."            # 期望最终输出（原有）
  tool_spec:                       # 期望工具行为（新）
    must_call: [search, read]       # 必须调用的工具
    must_not_call: [write_task]     # 禁止调用（写操作/危险工具）
    order: [search, read]           # 顺序约束
    max_calls: 6                    # 调用次数上限（防循环、防多余）
  step_judge:                       # 步骤级 judge rubric（新）
    criteria: "每步推理是否合理、信息是否正确传递、失败是否降级处理"
```

**工具序列断言（确定性，规则优先）**：`must_call / must_not_call / order / max_calls` 全部用规则断言，**不耗 LLM**——渐进式判定的重要落点：工具行为大部分可确定性验证，只有"推理是否合理"才走 judge。

**步骤级 judge**：对每步推理/工具调用打分（合理 / 信息传递 / 降级处理），比整体 judge 更能定位劣化步骤，衔接 §9.1 的 trace 组件级下钻与 §6.3 归因。

**过程 vs 结果分离报告**：

- `output_pass`：最终答案对否。
- `process_pass`：工具序列 / 步骤质量对否。
- 两者分开报告：`output fail` 是产品级缺陷；`output pass 但 process fail`（多余调用 / 禁用工具）是质量与成本缺陷，**同样进门禁**。

**运行态观测补充**（已有 ReAct span 数据，`react.tool` 事件带 tool_name/step/latency）：工具序列异常率、多余调用率、工具循环率、工具失败降级率——作为 process 维度的信号（§4.2），并入判异触发。

### 6.6 人工评审通道（低置信度判定兜底）

**问题**：judge 是 LLM，复杂/边界/对抗 case 的自动判定不可靠；自动判定误判会污染评测结果、触发错误门禁。需**判定后**的人工评审通道兜底。

**触发条件（进人工评审池）**：

1. **judge 置信度低**：judge 打分同时输出 `confidence`（0-1），低于阈值（默认 0.6，可配）→ 进池。规则断言天然确定，不走此通道。
2. **判定分歧**：多次 judge 结果矛盾 / judge 与规则断言冲突 → 进池。
3. **case 复杂标记**：case 标注 `needs_review: true`（复杂/边界/对抗，标注者声明自动判定不可靠）→ 必进池。
4. **过程 vs 结果不一致**：`output_pass` 与 `process_pass` 矛盾（尤其副作用越界类）→ 进池确认。
5. **归因证据不足**：版本对比结果混杂、无法归因 → 进池（衔接 §14 禁止伪归因）。

**评审流程**：

1. 触发 → 评审池（带 case、实际输出、工具序列、judge 打分理由、trace 下钻链接）。
2. reviewer 判定：pass / fail / 修正 case / 判定为 judge 误判。
3. 结论回写：
   - **产品缺陷** → 进归因/改进闭环（§9）。
   - **case 过时/标注错** → 修正 case 再重跑（衔接 §5.2 与 dev-eval"先改 case 再重录基线"）。
   - **judge 误判** → 作为校准样本喂 §11.2 黄金集校准，分歧入校准一致性监控。
4. **人工判定是金标准**：回写为该 case 的最终结论（`reviewed` 标记 + 人工结论 + reviewer + reason），后续同 case 不自动覆盖。

**与已有通道区分**：

- **case 入库审核**（draft → 人工审核 → publish）：审"case 本身对不对"。
- **判定评审**（本节）：审"这次判定可不可信"。两通道不同阶段，互补不重复。
- 衔接 L2 门禁（§8）：L2 的"告警 + 人工确认"是评审池的消费方之一。

**规模控制**：阈值默认保守（低置信才进）；评审池按风险排序（安全/写操作/高危资源优先）；评审队列积压是 meta 指标（§11.2 `eval_review_backlog`），积压超标告警。

**置信度机制**：judge 输出 confidence（低/中/高或 0-1）；分数落在边界（如 0.45–0.55）或打分理由含糊也视为低置信；规则断言天然确定，不参与。

## 7. 配置版本机制绑定

### 7.1 版本机制现状（代码事实）

`internal/parameters` + `platform_config_versions`：

- 版本链：`group_key + version_seq`，状态机 `draft → published`，draft 快照**写入后不可变**；Publish 记录 `base_version_id`。
- 生效指针：`platform_config_labels` 的 `production/latest` label；**Rollback 不产生新版本**，只挪 label 指回历史版本。
- 现成事件：Publish/Rollback 在**同一事务**写审计行（`ResourceChangeAuditEvent` + Before/After 投影）。

### 7.2 六个绑定点

1. **版本边界 = 归因时间窗**：每次 Publish = 一次"参数变更=代码变更"，开启新版本观察窗口。评测观察记录生效时 `production label → version_id`。**版本内 drift ≠ 参数因素**（同版本内劣化排除参数，指向外部依赖/内容/流量）。
2. **评测观察携带版本锚点**：`EvalObservation.param_version`（§4.3）；span 上已有参数快照 + prompt 版本指纹。快照不可变 → 同版本内观察可比、结果可复现。
3. **发布/回滚事件 = 评测触发源**：复用审计/事件。Publish → 开新版本观察窗 + 触发评测集回归（离线，用新版本快照跑）；Rollback → 对比回滚前后，验证恢复。高风险参数（ImpactMajor）→ Publish 即自动进金丝雀观察窗。
4. **评测集回归基线对任意历史版本可跑**：快照不可变 + 版本链 → 评测集评测可用**任意历史版本快照重跑**，离线回归可对比 "V1 好不好，V2 行不行"。
5. **门禁回滚走版本机制**：评测判异 → 不自己回滚，调 `internal/parameters.Rollback`（挪 label + 审计留痕，原子、可审计）。评测服务只做"判异"，回滚动作永远是版本机制 + 审计完成。
6. **tunable_registry 决定评测靶心**：只有登记的参数变更进"参数归因"；未登记按代码变更走发布，不进评测归因。impact 分类决定门禁强度（金丝雀 vs 仅告警）。

### 7.3 数据流

```
Publish(V2, base=V1) ──→ 审计/事件 ──┬─→ 开 V2 观察窗（分层采样标 V2）
                                     ├─→ 触发评测集回归基线（V2 快照）
                                     └─→ ImpactMajor → 金丝雀窗(1-5%)
生产流量执行 ──→ span 带当前 production 版本 V2 + prompt 指纹
        └─→ EvalObservation{trace_id, group_key, version_seq, 三源信号}
归因窗口：V1 vs V2 对比（分层防辛普森）· 版本内 drift 排除 · 回滚前后验证
判异 → 门禁：规则即时 / judge·行为告警+人工 / 高风险 → parameters.Rollback(审计)
```

## 8. 分层门禁

| 层 | 信号 | 动作 | 延迟 | 自动/人工 |
|---|---|---|---|---|
| L1 | 规则护栏命中 | **即时拦截**（fail closed） | 内联零延迟 | 自动 |
| L2 | judge 得分跌阈 / 行为异常 | **告警（飞书）+ 人工确认**（§4.5） | 异步秒级 | 告警自动，确认人工 |
| L3 | 租户资源高风险参数（ImpactMajor）判异 | **可配自动回滚**（走 `parameters.Rollback`，租户级） | 异步 | 策略可配，默认人工确认 |
| L3+ | **平台参数**判异（影响全租户） | **禁止自动回滚**；事前哨兵集回归 + 事后多租户验证（§9.4），强制人工确认 | 异步 | 人工 |

- 门禁判定结果写回参数版本记录（该版本的评测结论）。
- L3 自动回滚默认关闭（人工确认），仅在显式策略开启时自动，且每次动作留审计。
- **平台参数回滚影响全租户，永远人工确认，禁止任何自动路径**——平台层只允许"告警 + 人工决策"。

## 9. 归因分析与改进建议

### 9.1 三级根因

| 层 | 问题 | 方法 |
|---|---|---|
| case 级 | 这次为什么差？ | trace 组件级归因：哪个 span 劣化（retriever 召回差 / tool 失败 / LLM 截断 / 上下文丢失） |
| 参数级 | 是不是改参数导致的？ | 版本前后对比 + 扰动式（一次改一个）归因，分层报告防辛普森悖论；评测集同集版本对比优先采信（§6.3） |
| 模式级 | 是不是系统性根因？ | 跨 case 失败聚类（按 error/span/规则命中类别） |

### 9.2 参数归因声明（Tunable 接口扩展）

```go
// 新增：每个参数登记时声明
type TunableEvalProfile struct {
    Scope           ParamScope        // platform | resource（平台影响全租户，见 §3.4/§3.5）
    Dimensions      []EvalDimension    // 影响哪些指标维度（§3.2 矩阵）
    AttributionMode AttributionMode    // version_diff | perturbation | both
    ImprovementHint string             // 该维度劣化时的调节方向，喂给建议生成
}
```

**参数纳入归因的三个必要条件**：可观测（运行时能拿到实际值）、可归因（声明了因果边）、可调节（TunableRegistry 已登记 Read/Write/Validate/SearchSpace）。三者缺一不纳入。

### 9.3 改进闭环

```
指标维度 X 劣化
  → 候选参数集 P_X = registry 中声明影响 X 的参数（因果矩阵命中）
  → 归因筛选：P_X 中谁在观察窗内真变了 / 与劣化相关（版本对比 + 扰动式）
  → 建议生成（三种形态）：
       ① 连续参数 → SearchSpace 出候选值 + 金丝雀验证
       ② prompt 参数 → LLM 基于失败 case 生成修改建议 + 金丝雀
       ③ 归因命中"非参数因素"（外部依赖/内容/检索）→ 建议走代码变更，不硬调参
  → 流转：CandidateService + 审批 + 金丝雀窗口验证指标 → promote / 回滚（复用现有）
```

第 ③ 条是归因的**排除价值**——很多"评测差"不是参数问题，硬调参是负优化。

**复用**：`Tunable` 的 Read/Write/Validate/SearchSpace、`ApplyPatches`+`TunableChange`、`ResourceTunableCategories[kind]`、`CandidateService`、`ExperimentService`、审批流。
**新增**：`TunableEvalProfile` 声明、归因服务（矩阵路由 + 版本对比/扰动）、建议生成器。

### 9.4 全链路参数归因、正交分离与平台参数多租户效应

**归因范围扩展为全链路**，不限于 7 个平台 tunable：按链路环节覆盖模型配置、上下文、提示词、检索、工具、记忆的参数，且按作用域（§3.4）区分来源层级。每个关键参数在评测观察中记录**实际生效来源**（platform 默认 / resource 覆盖）。

**正交分离**（§3.5）：归因先按双版本锚点分组——同一资源配置下平台版本对比 = 平台净效应；同一平台版本下资源配置对比 = 租户配置净效应。平台与资源效应不混同。

**平台参数多租户效应的归因（痛点专项）**：平台参数发布影响全租户，走"多租户受控实验"逻辑：

1. **全租户对照**：平台参数发布 = 处理组全开。归因对比全租户聚合 delta + 按 tier/行业/流量规模分层 delta（防辛普森，§3.3）。
2. **多租户分化检测**：平台参数让多数租户改善但少数劣化（分布效应）→ 告警"多租户分化"，劣化租户名单下钻其资源配置，区分"平台效应"与"租户配置叠加交互效应"。
3. **平台 × 租户配置交互**：平台参数改动可能只影响"用了某类配置的租户"——按租户配置特征分层归因，识别交互效应。
4. **回滚高门槛**：平台参数回滚影响全租户，走 §8 的 L3+（禁止自动回滚，强制人工确认 + 事前哨兵回归 + 事后多租户验证）。

## 10. 展示分析

在评测中心（已有 `EvaluationCenterPage`：overview/resources/suites/runs/candidates/experiments）之上，补 5 个运行态视图：

1. **运行态健康分趋势**：按资源 × 参数版本 × 时间窗的评分时间序列（rule 命中率 / judge 得分 / 行为异常率），Prometheus 数据源。
2. **分层门禁状态**：当前告警、待人工确认队列、规则即时拦截计数、回滚记录——门禁 L2/L3/L3+ 操作台。
3. **归因对比视图**：参数变更（发布/回滚）前后指标对比，分层报告（防辛普森悖论）。
4. **单 case 三源信号下钻**：一次评测观察的 rule 明细 + judge 打分理由 + 行为信号，下钻 Opik trace 全链路。
5. **人工评审操作台**：评审池（触发原因 + case + 实际输出 + judge 理由 + trace 下钻）+ 判定 + 结论回写（§6.6）；衔接 L2 待确认队列。

## 11. 评测运行指标监控

`docs/agent/observability.md` 已明确告警不覆盖 LLM/Agent/Token/业务指标——本次补齐评测相关。

### 11.1 对象能力指标（运行态评测产出）

按 `resource × 参数版本 × tenant-tier` 分层，落 Prometheus + 告警：

```
eval_rule_hit_total{rule, resource, verdict}
eval_judge_score{resource, dimension}        // Histogram
eval_behavior_anomaly_total{resource, signal}
eval_observation_total{resource, stratum}    // 采样
eval_sample_coverage{resource}               // Gauge 主动采样覆盖率
```

### 11.2 评测系统自身 meta 指标

```
eval_judge_latency_seconds        eval_judge_cost_total      eval_judge_failure_total
eval_queue_backlog{queue}         eval_backfill_lag_seconds
eval_judge_calibration_agreement{judge_model}   // Gauge，judge vs 人工校准一致性
eval_baseline_drift{suite}                     // Gauge，回归基线变更率
eval_gate_action_total{layer, action}          // 拦截/告警/待确认/回滚计数
eval_review_backlog                            // 人工评审池积压（Gauge，§6.6）
```

- **judge 是异步外部依赖**，按项目原则必须有超时预算、有限重试、熔断/隔离。
- **Goodhart 指标腐化防线**：刚性基线守下限（judge 校准一致性跌阈告警）+ 人工评审校准方向（黄金集季度刷新 10-20%）。judge 需 50-100 人工样本校准（约 85% 一致性），禁止自评。

## 12. 全链路可观测埋点

沿用 `docs/agent/observability.md` 的 GenAI 语义规范（`gen_ai.span.kind`: LLM/TOOL/AGENT/RETRIEVER/...），评测埋点只做**引用**、不做 payload 双写：

- 执行 span 已携带参数快照 + prompt 版本指纹（`cb2bd781` 已实现）。
- 评测观察以 `opik.metadata.stratum.eval_*` 属性挂回原 trace，不复制证据。
- payload 走现有 AES-256-GCM → MinIO，span 只带 `payload_ref/sha256/size`。

> **实施分解**：本规格按 §13 三期拆为多个独立实施计划（一期一个），每期单独规划、评审、交付。单期内部如需再拆（如 Phase 2 的评测集扩展与配置版本绑定分属不同 worker），在对应实施计划中进一步拆分。

## 13. 三期落地路径

### Phase 1：运行态观测层（监控 + case 收集）

- 规则护栏内联（快路径）+ judge 异步采样 + 行为信号采集 → `EvalObservation`。
- 评测集补两条产线：KB 派生、运行态沉淀（判异 → 待审核池 → 人工确认）。
- 评测系统 meta 指标 + 告警补齐（judge 健康、队列积压、采样覆盖）。
- 上升人类分级（T1–T4）+ 飞书报警接入（§4.5）。
- 交付：运行态健康分趋势 + 门禁状态视图（§10.1/10.2）。

### Phase 2：配置版本绑定 + 判定闸门

- 六个绑定点落地（§7）：版本锚点、发布/回滚触发、历史版本回归重跑。
- 评测集评测输出升级：多维指标结果（§6.2）+ 评测归因三层（§6.3）+ 离线链路评测（§6.4）+ 多步推理工具序列（§6.5）+ 人工评审通道（§6.6）。
- 分层门禁 L2/L3/L3+ 集成（告警+人工 / 租户回滚策略 / 平台参数人工红线）。
- 交付：归因对比视图 + 单 case 下钻 + 人工评审操作台（§10.3/10.4/10.5）。

### Phase 3：参数归因 + 改进闭环

- `TunableEvalProfile` 声明 + 因果矩阵路由 + 归因服务（§9.2）。
- 建议生成器接入 `CandidateService` + 金丝雀实验闭环（§9.3）。
- 交付：改进建议操作台 + 优化循环。

## 14. 错误处理与 fail-closed

| 场景 | 行为 |
|---|---|
| judge 不可用 | 评测采样降级跳过（不阻断执行）；已缓存校准黄金集对比不中断 |
| 规则护栏命中 | fail closed：即时拦截，禁止默认放行 |
| 参数版本锚点缺失 | 观察标记 `param_version=unknown`，归因排除（不参与版本对比） |
| 主动采样覆盖率不足 | 告警（`eval_sample_coverage` 跌阈），禁止静默跳过某层 |
| 判异 → 自动回滚 | 仅租户级（§8 L3）：默认人工确认，显式策略开启时自动，每次动作留审计；平台参数永不自动（§8 L3+） |
| 评测归因数据不足 | 显式报"证据不足"（样本量/分层覆盖），禁止输出伪归因 |
| Opik 不可用 | 证据查询返回 503（沿用现有 `TraceEvidenceProvider` 语义），评测采样暂停不伪成功 |

## 15. 测试策略

- **复用**：`internal/evaluation` 现有测试模式（suite/baseline/repository mock）、`CaseSampleSource` 集成测试、测试 agent 验收流程。
- **新增**：
  - 规则护栏内联：规则命中即时拦截、fail-closed 测试。
  - 采样策略：分层覆盖率、采样不重复、判异触发入池。
  - 上升人类：T1–T4 分级路由、飞书告警（runbook_url 守卫）、评审池入池触发。
  - 版本绑定：发布触发回归、回滚前后对比、历史版本重跑、锚点缺失归因排除。
  - 多维指标聚合：按维度/分类聚合、辛普森分层报告、双版本锚点分组。
  - 评测归因：同集版本对比、case 聚类、trace 下钻、多租户分化检测（§9.4）。
  - 离线链路（记忆）：管道直调断言、回放驱动。
  - 多步推理：工具序列断言（must_call / must_not_call / order / max_calls）、过程 vs 结果分离。
  - 人工评审：低置信/分歧入池、金标准回写、judge 误判进校准。
  - 门禁：L1 即时拦截、L2 告警、L3 租户回滚策略（默认人工）、L3+ 平台参数永不自动。
  - 评测系统 meta 指标：judge 健康、队列积压、校准漂移告警。
- **契约**：改参数契约走 `proto/` + `make proto-gen`；评测观察以 JSON 属性挂回 trace，不新增平行客户端。
- **质量门禁**：新增 Go 函数满足复杂度 ≤10 / 认知 ≤15 / 行数 ≤120 / 嵌套 ≤4。

## 16. 交付项清单

### git 交付项

1. 运行态观测：规则护栏内联 + 采样观测（judge/行为/主动/判异）+ `EvalObservation` 模型与存储（含 confidence）。
2. 评测集产线：KB 派生、运行态沉淀（待审核池 + 人工确认）、批量导入。
3. 评测集评测扩展：多维指标聚合（`EvaluationRun.metrics` 扩展，含 confidence）+ 评测归因服务 + 多步推理工具序列断言（§6.5）。
4. 配置版本绑定：发布/回滚触发、历史版本重跑、门禁回滚适配（`parameters.Rollback`）。
5. 分层门禁：L1 拦截 / L2 告警+人工 / L3 租户回滚策略 / L3+ 平台参数人工红线。
6. 人工评审通道：评审池 + 判定回写 + 金标准结论（§6.6）。
7. 参数归因：`TunableEvalProfile` + 因果矩阵 + 建议生成器 + Candidate 接入。
8. 展示：5 个运行态视图（含人工评审操作台）。
9. 监控：对象能力指标 + 评测系统 meta 指标（含 `eval_review_backlog`）+ 告警规则（`monitoring/remote/`）。
10. 埋点：评测观察挂回 Opik trace（`opik.metadata.stratum.eval_*`）。
11. 测试与文档（§15）。

### 本地配置交付项

1. 测试 agent 提示词：评测采样与门禁流程进入验收流程。

## 17. 成功标准

- 运行态观测层持续产出监控信号与候选 case 池；规则护栏即时拦截 fail closed。
- 评测集六条产线（§5.1）case 可生成、人工审核、版本化回归；provenance 可审计。
- 评测集评测输出多维指标 + 归因；参数版本变更后判定闸门给出明确结论。
- 配置版本绑定生效：发布/回滚触发评测、历史版本可重跑、门禁回滚走版本机制。
- 评测自身运行可监控：judge 健康、采样覆盖、校准漂移、基线漂移告警可达。
- 无静默失败、无伪归因、无自动不可逆清理。
