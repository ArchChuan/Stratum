# Plan 波次引擎原语化：Agent 执行图升级设计

日期：2026-08-09
状态：已实现（阶段 A–D 完成，全量测试通过）
关联：`docs/agent/architecture.md`、ADR `2026-07-22-react-dynamic-dag-fusion-design.md`

## 背景与问题

1. **plan 波次并行寄生于工具节点**：`ExecuteReadyPlanNodes` 在 `stratum_continue_plan` 工具节点内部用 goroutines + semaphore 实现并行，引擎本身（`graph.go` Invoke）是单指针串行 for 循环。引擎无 fan-out/fan-in 能力。
2. **引擎单后继**：`edges map[string]string`、`EdgeFunc[S] func(state S) string`，无法表达汇合（多个 plan 槽位完成后统一进入 finalize）和 fan-out。
3. **lazy-planning 触发逻辑休眠**：`StuckThreshold` 赋值后无人读取；`executePlanning` 与 `executeReAct` 实际行为相同，只是换 `BuildPlanExecuteGraph` 包装。

## 决策

把 plan 波次并行从工具侧提升为 StateGraph 引擎原语（LangGraph superstep 形态）：

- **波次化 Invoke**：edges 改多后继，Invoke 改 superstep 波次（pending 就绪集 + semaphore 并行 + 波次屏障）。
- **plan 动态图注入**：`stratum_continue_plan` 排程化触发后 plan 节点注入执行图（plan-0..plan-9 槽位 + plan-finalize 汇合节点），引擎统一跑波次。
- **plan_runtime.go 只保留** checkpoint/budget/状态转换领域逻辑。

非目标：不激活 lazy-planning（StuckThreshold 触发逻辑维持休眠）；不引入 workflow 域概念；`pkg/dag` 保留给 workflow 域，graph 包移除引用；不改变 checkpoint ID 格式、MaxLLMSteps 强制最终回答机制、HTTP 契约。

## 设计

### 1. 引擎波次化（graph.go）

```go
type EdgeFunc[S any] func(state S) []string   // 多目标，返回空 = 死路（报错）
type WaveResult[S any] struct { Node string; State S; Err error }

type RunConfig[S any] struct {
    MaxSteps    int    // 语义变化：波次上限（ReAct 图每波 1 节点，等价）
    MaxParallel int    // ≤0 = 串行（默认）；>0 波次内并发上限
    AfterStep   func(ctx context.Context, state S) error
    MergeWave   func(base S, results []WaveResult[S]) (S, error)  // nil = 按序 last-write-wins
}
```

**Invoke 波次循环**（pending 激活集模型）：

- `pending` 初始 `{entry}`；`executed` 记录已执行节点
- 每波：`ready = sorted({v ∈ pending : ∀u∈incoming(v)（静态边源，排除自环）→ executed[u]})`；**汇合只阻塞已激活的 incoming 源**——从未被激活的静态边源不计入阻塞（plan-finalize 立即汇合的关键）
- `pending` 空 → 正常终止；`pending` 非空但 `ready` 空 → 死锁错误
- 波次内 `MaxParallel` semaphore 并行执行 ready 节点，每节点 panic 恢复 + ctx 检查，结果收进 `results`
- 失败节点不路由（本波返回错误，向上传播）
- 路由：仅对**本波成功节点**评估——优先 `condEdges`，否则静态边 `edges`；目标追加 pending；`END` 终止
- `AfterStep` 每波一次（含终止波：checkpoint 必须观察最终状态）；`MergeWave` 每波合并 base + results
- 波次上限 `MaxSteps`，超出报 `max steps exceeded`

### 2. ReAct 图适配（BuildReActGraph）

```
llm --cond--> [tool] | [END]
tool --cond--> [llm]                     // 无 PlanWavePending 时
             | [plan-0..plan-9]          // PlanWavePending 非空：makeToolNext 返回槽位名列表
plan-0..plan-9 --static--> plan-finalize // 汇合
plan-finalize --static--> llm
```

- `llm` 条件边返回多后继形式，行为不变
- **makeToolNext**：`PlanWavePending`（[]int，plan.Nodes 索引）非空 → 槽位名列表；否则 `[llm]`
- **槽位节点** `makePlanSlotNode(i)`（i=0..MaxPlanSteps-1）：执行 `plan.Nodes[PlanWavePending[i]]`，panic recover → 失败 outcome（不中断波次）；childState 隔离（ActivePlan=nil、PlanToolsDisabled=true、清空波次簿记）；**不递增 Steps**（MaxLLMSteps 强制最终回答机制不受影响）；产出 `PlanWaveOutcome` 追加 `PlanWaveOutcomes`
- **plan-finalize 汇合节点**：调 `applyPlanOutcomes`（状态转换+checkpoint），清空 `PlanWavePending`，追加波次观察（`Role:"tool"`、`ToolCallID: PlanContinueCallID`、Content 为排程快照时的轻量 observation），路由到 llm

### 3. plan 运行时重写（plan_runtime.go / plan_tools.go）

- 删除：`ExecuteReadyPlanNodes`、`readyPlanNodes`、`executePlanNodeSafe`、`cloneRuntimePlan`（并行整体迁移至引擎槽位）
- `schedulePlanWave`：`planWaveReady`（轻量就绪判定 + `validatePlanStructure`：重复 ID/缺失依赖/重复依赖/自依赖/Kahn 环检测）→ 截断至 MaxPlanSteps → MaxRevisions 波次预算检查 → 设置 `PlanWavePending` → 返回轻量 observation
- `applyPlanOutcomes`：failed/uncertain → 失败状态 + Attempts 追加（PlanIDSource）、`plan.Revision++`、每 outcome 一个 checkpoint `${planID}-wave-${revision}-${nodeID}`、checkpoint 失败中止传播、`ErrPlanCheckpointRequired` 前置检查
- **排程化 continue**：`ExecutePlanTool` continue 分支调 `scheduleContinue`——排程成功时登记 `PlanContinueCallID`；tool 节点在 dispatch **之后**检测 `tc.ID == PlanContinueCallID` → 跳过 observation/消息追加（trace 与 AllToolCalls 审计保留），波次观察由 finalize 汇合后补全；无就绪节点时维持旧行为（直接 observation）
- `plan_checkpoint.go` 删除 `BuildPlanExecuteGraph` 包装（唯一调用方 executePlanning 改用统一 ReAct 图）

### 4. 调用方（agent.go）

- `executeReAct` / `executePlanning` 统一 `BuildReActGraph`；runCfg 增加 `MaxParallel: initState.PlanLimits.MaxConcurrentNodes`（默认 4）、`MergeWave: agentgraph.MergeReActWave`
- graphSteps 公式：`MaxSteps*2 + 1 + 2*MaxPlanSteps`（plan 波次一轮 ≈ 槽位波 + finalize 波，为 MaxPlanSteps=10 预留）
- `StuckThreshold` 赋值保留（字段声明维持，触发逻辑休眠）

### 5. 状态合并（react_merge.go MergeReActWave）

- 单节点波：直接采用节点状态（等价旧串行引擎）
- 多节点波（仅 plan 槽位，只 append 不 rewrite）：append-only 字段取增量（`appendDelta` 泛型，从 base 长度截取尾部）——Messages、AllToolCalls、ToolObservations、TraceEvents、AssistantToolArtifacts、ModelRoutedVia、StepResults、PlanWaveOutcomes；计数取差量——Steps、TotalTokens、TotalCostUSD；其余 last-write-wins——Output、ModelResolved、LastEstimatedTokens、TokenCorrection、ActivePlan、PlanCheckpointIdentity、PlanWavePending、PlanContinueCallID
- 只读 base：TenantID、TraceID、ConversationID、ExecutionID、Model、PlanLimits、PlanNodeExecutor

### 6. 文件拆分（react.go → 5 文件）

react.go（1374 行）→ `react_state.go`（状态/接口）、`react_llm.go`（图构建/LLM 节点/预算）、`react_tool.go`（工具节点/dispatch 链/观察）、`react_helpers.go`（工具集合/激活/字符串助手）、`react_merge.go`（MergeReActWave）、`plan_graph.go`（槽位/finalize/makeToolNext）。纯移动，零逻辑改动。

## 错误处理

- 排程化 continue 的 checkpoint 失败：`ExecutePlanTool` 返回 err（`ErrPlanCheckpointRequired`、持久化错误）→ tool 节点 fatalToolErr 传播 → 整图失败
- 槽位 panic：recover → 失败 outcome → finalize 写失败状态 + Attempts Error，不中断同波其他节点
- 波次 checkpoint 失败：中止 fold 并传播，plan 可能部分推进但整次执行报告失败
- 波次预算超限：`ErrPlanBudgetExceeded` 在排程时返回（不注册波次）

## 测试

- graph_test 新增：fan-out/汇合（阻塞只对已激活源）/死锁检测/MaxParallel 并发上限/MergeWave 自定义合并/AfterStep 每波一次
- react_test 34 用例零改动全绿（波次化对单后继图行为等价）
- plan_runtime_test 重写：4 个引擎级集成测试——排程波运行（并发峰值=MaxParallel、状态/Attempts/Revision 4、3 个 checkpoint、finalize 观察）、panic 恢复为失败 outcome、checkpoint 失败传播、Revision 预算超限
- 全量 `go test -v -race -timeout 30s ./...`、`make code-quality`（无阻断）、`make risk-guardrails` passed、contract_test 零改动通过

## 验证结果

- 阶段 A–D 全部完成，全量 race 测试全绿
- code-quality 棘轮：新增函数全部达标（applyPlanOutcomes 11→拆 applyPlanOutcome、validatePlanStructure 14/25→拆 3 helper、ExecutePlanTool 21→19 拆 scheduleContinue）
- 兼容性：HTTP 契约、checkpoint ID 格式、MaxLLMSteps 机制不变；`pkg/dag` 保留给 workflow 域
