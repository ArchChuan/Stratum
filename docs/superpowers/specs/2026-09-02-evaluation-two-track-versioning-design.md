# 评测参数双轨版本化（全链路版本快照）设计

日期：2026-09-02
范围：`internal/evaluation` + `internal/agent` + `internal/parameters` + `api/wiring`
前置：评测总路线 Phase 2 现状（A3 原方案暂停重设计）

## 1. 背景

评测 run 的「版本保证」存在两轨参数：

1. **被测对象参数**（租户/资源级）：agent 的 temperature / max_tokens / max_context_tokens 等，锚定在 `AgentRevision` 快照，`ExecuteRevision` 可重放。
2. **评测器平台参数**（平台级）：judge / observe / casegen / ruleguard 的 enabled、model、sample_rate 等，走 `PlatformValues(ctx)`，**未版本化**。

现状缺口（均有代码证据）：

- **skill 场景不可重放**：`ExecuteSkillScenario`（agent_execution.go:657-679）通过 `Registry.Get(ctx, agentID)` 取**当前生产 agent 配置**，与 `ExecuteRevision`（:26-56，revision 快照）语义不一致。评测 skill 场景时被测对象用的是运行时当前值，非创建时点值。
- **参数补参混入运行时默认**：`assembleOptions` → `resolveEffectiveParameters`（agent_options.go:453-497）对快照中 unset 的参数用 `ResolveForResource` **两级回退**（声明值 → 平台默认 → 定义默认），补入的是**运行时**当前平台默认，可能覆盖/污染创建时点的被测语义。
- **评测器参数无版本**：`judgeAdapter.Enabled/judgeModel/Judge`、`observationEnabled/SampleRate`、`casegenAdapter.genModel`、`ruleGuardEnabled` 全部从 `PlatformValues(ctx)` 取**运行时当前值**。同一 run 执行期间平台参数被改，前后 case 用不同 judge 配置，结果不可复现。
- **canary 分流无固定**：`assembleOptions` 中 MCP/Knowledge revision resolver 做实时分流，评测执行期间 canary 放量变化会改变被测环境，run 不可复现。

用户裁决：**「平台参数也有版本，经过的链路都需要被锚定」**，边界为**「全链路版本快照」**——评测执行读 run 创建时点的快照，同时隔离 canary 分流。

## 2. 目标与成功标准

目标：

- 评测 run 一经创建，其执行全程（被测对象、评测器参数、执行窗口、canary 分流）取值自 run 创建时点的**版本快照**，不读运行时当前值。
- 同一 run 内每个 case 使用完全一致的评测上下文，run 可重放、可复现。
- 快照捕获失败时拒绝创建 run（fail-closed），执行中快照缺失/解析失败显式失败，不静默降级当前值。
- 不污染非评测执行路径（生产 agent 执行走现有路径）。

成功标准（HowToTest）：

- 创建 run 后修改平台 judge/observe 参数，再跑同批 case：结果与创建时点参数一致（与修改前创建 run 结果相同），证明执行走快照。
- skill 场景 run 创建后修改承载 agent 生产配置：case 结果不受影响（锁 revision）。
- canary 放量调整后重跑同 run：结果不变（pin assignments）。
- 快照缺失（旧 run）执行时返回明确失败与提示，不静默降级。
- 非评测执行（`Execute` 生产路径）行为与改动前一致，回归测试通过。

## 3. 设计决策

| # | 问题 | 决策 |
|---|------|------|
| D1 | 快照语义 | **值复制进 run 记录**（创建时点完整复制，非 seq 引用）——不依赖 parameters 域版本 trim 保留历史 |
| D2 | 执行取值来源 | **执行全程从 run 内快照取值**，不读运行时当前值 |
| D3 | A3 平台版本事件 | **保留但用途收缩**：parameters 域 publish/rollback 版本管理保留，供观测 `param_version.platform.version_seq` 指标 + 版本历史展示；执行取值走快照，不走事件消费 |
| D4 | canary 分流 | **评测执行 pin 固定**：快照 `pinnedAssignments` 固定 MCP/Knowledge/Skill revision，替代实时分流 |
| D5 | 失败语义 | **fail-closed，锚定创建时**：快照捕获失败 → 拒绝创建 run；执行中快照缺失/解析失败 → run/case 显式失败 |
| D6 | 生产路径隔离 | **注入快照参数上下文**（port 级接口，nil = 非评测执行），`resolveEffectiveParameters` / resolver 内部识别并切换数据源；不在共享路径硬编码评测分支 |
| D7 | skill 场景 | **改走 `ExecuteRevision` 锁承载 agent revision**（替代 `ExecuteSkillScenario` 读当前生产配置） |
| D8 | 多租户 | 快照落 `eval_runs` 新列（tenant schema），受租户边界保护 |

## 4. 架构

### 4.1 核心概念：版本化执行上下文（`EvaluationContextSnapshot`）

run 创建时捕获评测上下文快照，执行全程从快照取值：

```go
// internal/evaluation/domain（示意）
type EvaluationContextSnapshot struct {
    Subjects         map[string]SubjectAnchor     // 被测对象 revision（已存在 resource_version 机制）
    Evaluation       GroupSnapshot                // evaluation 组：versionSeq + values
    Execution        []GroupSnapshot              // 被测执行相关组：agent 组（+ 被测启用 memory 时 memory 组）
    ResolvedExecution ResolvedExecution           // contextWindow / outputReserve 固化值
    PinnedAssignments PinnedAssignments           // canary 隔离：固定 MCP/Knowledge/Skill revision
    CapturedAt       time.Time
    CapturedBy       string
}

type GroupSnapshot struct {
    GroupKey   string         // 如 GroupEvaluation / GroupAgent
    VersionSeq int64          // 分组独立版本序号
    Values     map[string]any // 创建时点值（已复制）
}
```

### 4.2 数据流：两个时点

**创建时捕获**（`EnqueueRun` 路径）：

```
1. subjects          ← 被测对象 revisions（已有 resource_version 机制，不重复造）
2. evaluation        ← 读 evaluation 组 production label：versionSeq + 复制快照值
3. execution         ← 读被测执行相关组 production label：agent 组（+ 被测启用 memory 时 memory 组）
4. resolvedExecution ← 现场解析并固化：模型上下文窗口 / 输出保留 / 被测模型存在性
5. pinnedAssignments ← skill 场景：解析 skill → 承载 agent，锁 agent 当前 revision
                       MCP/Knowledge：锁当前 revision（替代实时 canary 分流）
```

**执行时使用**（worker 跑每个 case）：整个执行从 run 内快照取值，不触运行时当前状态。

## 5. 每轨接入改动点

| 轨 | 现状读取点 | 改为 |
|---|---|---|
| 评测器 judge | `judgeAdapter.Enabled/judgeModel/Judge` 读 `PlatformValues(ctx)` | 从 `snapshot.Evaluation` 读 `judge.enabled/model/temperature` |
| 评测器 observe | `observationEnabled/SampleRate` | 从 `snapshot.Evaluation` 读 `observe.enabled/sample_rate` |
| 评测器 casegen | `casegenAdapter.genModel` | 从 `snapshot.Evaluation` 读 `optimizer.model` |
| 评测器 ruleguard | `ruleGuardEnabled` | 从 `snapshot.Evaluation` 读 `ruleguard.*` |
| 被测执行参数 | `assembleOptions` → `resolveEffectiveParameters` 的 `ResolveForResource` 读当前平台默认 | 评测执行注入快照 provider：从 `snapshot.Execution` 解析，unset 用创建时点默认 |
| 被测执行窗口 | `resolveExecutionWindow/OutputReserve` 读 DB 模型权威 | 评测执行用 `snapshot.ResolvedExecution` 固化值 |
| skill 场景承载 agent | `ExecuteSkillScenario` → `Registry.Get` 当前生产 | 改为 `ExecuteRevision`（锁 `pinnedAssignments` 的 agent revision） |
| canary 分流 | `assembleOptions` 里 `MCPRevisionResolver/KnowledgeRevisionResolver` 实时分流 | 评测执行用 `snapshot.PinnedAssignments` 固定 revision |

### 5.1 生产路径隔离机制

不能在 `assembleOptions` 硬编码评测分支。方案：评测执行注入一个「快照参数上下文」，由现有读取点内部识别并切换数据源：

```go
// port 级接口（evaluation 侧定义，agent/wiring 实现）
type EvaluationExecutionContext interface {
    EvalSnapshot() *EvaluationContextSnapshot // nil = 非评测执行，走现有路径
}
```

- 执行时：`assembleOptions` 检测到评测上下文 → `ResolveForResource` / resolver 从快照取值；否则原样走当前路径。
- 快照解析失败 → fail-closed（D5）。

## 6. 错误处理与边界

### 6.1 错误处理（fail-closed，锚定「创建时」）

| 失败点 | 处理 |
|---|---|
| evaluation 组 / agent 组读版本或值失败 | `EnqueueRun` 直接报错，run 不入队 |
| `resolvedExecution` 固化失败（被测模型不存在、窗口非法） | `EnqueueRun` 直接报错 |
| 执行中快照缺失（机制上线前创建的旧 run） | worker 标记 run 失败并返回「无版本快照，需重建 run」，**不静默走当前值** |
| 执行中快照值解析失败 | case 失败（`FailureReason="execution"`），不降级当前值 |

### 6.2 边界

- **向后兼容**：新机制只对新建 run 生效。存量 run 读到时明确失败提示重建（评测 run 幂等可重跑，代价可接受；比静默降级安全）。
- **快照大小**：被测 revision 参数 + 评测器参数 + pinned assignments，单 run 快照 KB 级，落库 JSONB 无压力。
- **A3 平台版本事件保留但用途收缩**：parameters 域 publish/rollback 的版本管理保留，供观测指标与版本历史展示；执行取值走 run 内快照，不走事件消费。
- **多租户**：快照落 `eval_runs` 新列（tenant schema），受租户边界保护。

## 7. 测试策略

- **单元**：快照捕获纯函数（parameters 域读 → snapshot struct）、`resolveEffectiveParameters` 快照注入分支、skill 场景 revision 锁定。
- **集成**：`runCase` 从快照取 judge/observe/casegen 配置；MCP/Knowledge resolver 评测走 pin 路径。
- **契约**：快照是内部数据，不进对外 proto 契约；run 详情接口展示 version anchor（suite revision / resource version / platform seq）作为可观测字段。
- **失败路径**：快照捕获失败 → 创建拒绝；执行中快照缺失 → run 失败；`resolveEffectiveParameters` 快照分支失败 → case 失败。
- **回归**：非评测执行路径（生产 `Execute`）行为不变，既有 agent application / wiring 测试全绿。

## 8. 落地范围与 PR 分组

对比原 A3：原方案只对评测器平台参数做版本绑定；新设计把被测对象、评测器、执行窗口、canary 分流全链锚定到 run 创建时点的快照。范围扩大，但数据结构收敛为一处（`EvaluationContextSnapshot`）。

- PR-1 = A1（§12 埋点，Task 1 已提交 `89d5a478`）+ A3（重新设计后的全链路版本快照）
- PR-2 = A2 + A4
- PR-3 = B1 + B4
- PR-4 = B2 + B3
- PR-5 = C1 + C2
- PR-6 = C3

## 9. 风险与对策

| 风险 | 对策 |
|---|---|
| 快照与运行时模型 drift（schema 演进后旧快照） | 快照 JSONB 带 schema version；解析失败 fail-closed；重建 run |
| skill 场景锁 revision 后评测 skill 行为与生产不一致 | 这是**预期语义**（评测锚定创建时点），文档明示；对比评测用生产执行基线单独做 |
| `resolveEffectiveParameters` 快照分支侵入共享函数 | 注入接口 nil 判空分流，非评测路径零分支开销；单测覆盖两分支 |
| 平台参数分组取值遗漏（新评测器参数接入） | 快照捕获从 parameters 域组内复制全量 values，天然覆盖新键 |
