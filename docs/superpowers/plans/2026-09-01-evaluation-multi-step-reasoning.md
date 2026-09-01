# 评测多步推理与工具调用（§6.5/§6.6）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development 逐任务实现本计划。Steps 用 `- [ ]` 跟踪。

**Goal:** 为 agent/skill 评测新增工具序列过程断言（tool_spec 确定性规则 + step_judge LLM 步骤 rubric），实现 output_pass/process_pass 分离报告与评审池冲突触发。

**Architecture:** `AgentResult.ToolObservations` 是唯一可靠工具序列数据源，由两个执行适配器投影进 `ExecutionResult.Tools`（port 层）；domain 新增 `ToolSpec`/`StepJudge`/`EvaluateToolSequence` 纯函数做确定性断言，step_judge 复用 `LLMJudge` 端口（`JudgeRequest` 加 `ToolSequence` 字段）；`EvalCaseResult` 加 `ProcessPass`/`ProcessFailure`/`Tools`，落库 `eval_case_results` 新列；评审池新增 `process_output_conflict` 触发；运行级指标加 `process_pass_rate`。

**Tech Stack:** Go 1.25（pgx v5 / Gin）、proto + protoc-gen-ginstruct、React 18 / AntD 5 / zod。

## Global Constraints

- **domain 不 import port**：`port/evaluation.go` 已 import domain。`ToolObservation` 结构体必须迁到 `internal/evaluation/domain`，port 用 `type ToolObservation = domain.ToolObservation` alias（透明兼容，现有 `evalport.ToolObservation{...}` 字面量照常）。
- **多租户 DDL**：`eval_case_results` 新列走 `pkg/storage/postgres/tenant_schema.sql`（tenant-only 唯一基线）：`CREATE TABLE IF NOT EXISTS` 同步 + `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` 升级历史租户；NOT NULL 带安全默认值（`process_pass BOOL NOT NULL DEFAULT true`）。禁止放 `pkg/migration/sql/`。
- **工具序列脱敏**：落库前 `Arguments` 复用 `sanitizeValue`（run_repository.go）、`RawText` 走 `sensitiveText` 正则（`$1=[REDACTED]`）。
- **step_judge fail-closed**：judge nil/disabled/解析失败 → `result.Error` + `FailureReason="execution"`，不静默 pass（与 `judgeCase` 同语义）。
- **process 冲突入池只在有冲突时**：规则 case 无 judge 信号，`TryEscalateCaseResult` 按 `AssertionMode` 分支——judge case 走完整 `TriggersForCaseResult`，规则 case 只 `TriggersForProcessConflict`（避免低置信误触发）。
- **proton 契约唯一事实源**：`proto/evaluation/evaluation.proto`；改后 `make proto-gen`（DTO 不入 git）。`UpdateDraftCaseRequest` 不携带 tool_spec/step_judge（沿用锁定）。
- **Go 质量门禁**：圈复杂度≤10、认知≤15、行≤120、嵌套≤4；行为数字禁止内联（`pkg/constants/evaluation.go` 加 `StepJudgeMaxTools`/`StepJudgeRawTextMaxChars`；`web/src/constants/index.ts` 加 `EVALUATION_MAX_CALLS_LIMIT`）。
- **前端**：唯一 Axios 实例 `client.ts`；错误 `message.error({ content, duration: 3 })`；用户可见文案中文；组件 <200 行；行为常量全大写下划线。
- **评审池 trigger 枚举**：新 `TriggerProcessOutputConflict ReviewTriggerReason = "process_output_conflict"` 并入 `Valid()`；前端 `REASON_LABELS` 加中文映射。
- **契约 golden**：`ProcessPass bool` 无 omitempty（仿 `passed`）会在 run 结果 JSON 出现，`api/http/contract_test.go` golden 同步。

## 执行顺序（依赖修正）

Task 4（JudgeRequest.ToolSequence 字段）必须先于 Task 3（judgeProcess 消费该字段）落地，否则 Task 3 编译失败。顺序：**T1 → T2 → T4 → T3 → {T5,T6,T7 并行} → T8 → T9**。Task 3 不改 `TryEscalateCaseResult` 签名（Task 6 落地）。

---

### Task 1: domain 类型与纯函数（ToolSpec/StepJudge/ProcessAssertion/EvaluateToolSequence/ToolObservation 迁移）

**Files:** Modify `internal/evaluation/domain/evaluation.go`、`internal/evaluation/domain/port/evaluation.go`；Create `internal/evaluation/domain/evaluation_test.go`

**Interfaces (domain/evaluation.go):**

```go
type ToolSpec struct {
    MustCall    []string `json:"must_call,omitempty"`
    MustNotCall []string `json:"must_not_call,omitempty"`
    Order       []string `json:"order,omitempty"`
    MaxCalls    int      `json:"max_calls,omitempty"` // <=0 = 不限
}
type StepJudge struct {
    Criteria string `json:"criteria,omitempty"` // 空回退平台默认步骤 rubric
}
type ProcessAssertion struct {
    Passed   bool
    Failures []string // 逐项归因，如 "process:must_not_call:delete"
}
// 纯函数：must_call 缺一即败、must_not_call 命中即败、order 子序列（greedy，允许跨多余调用）、max_calls 超限即败
func EvaluateToolSequence(toolNames []string, spec ToolSpec) ProcessAssertion
// judge 可读文本；截断用 constants.StepJudgeMaxTools / StepJudgeRawTextMaxChars（本任务在 pkg/constants/evaluation.go 一并新增）
func FormatToolSequence(tools []ToolObservation) string
// ToolObservation 由 port 迁移至此（评审投影精简版）
type ToolObservation struct {
    ToolName string `json:"tool_name"`; ToolType string `json:"tool_type"`
    StepIndex int `json:"step_index"`; ProviderType string `json:"provider_type"`
    CapabilityID string `json:"capability_id"`
    Arguments map[string]any `json:"arguments,omitempty"`; RawText string `json:"raw_text,omitempty"`
}
// EvalCase 追加 ToolSpec *ToolSpec / StepJudge *StepJudge（omitempty）
// EvalCaseConfig 扩展：
type EvalCaseConfig struct {
    JudgeSpec  *JudgeSpec      `json:"judge_spec,omitempty"`
    ToolSpec   *ToolSpec       `json:"tool_spec,omitempty"`
    StepJudge  *StepJudge      `json:"step_judge,omitempty"`
    Generation *GenerationMeta `json:"generation,omitempty"`
}
```

**port/evaluation.go:** `type ToolObservation = domain.ToolObservation`；`ObservedTrace.Tools` 引用自动跟随。

**Steps (TDD):**

1. 写失败测试 `TestEvaluateToolSequence`（表驱动：全通过 / must_call 缺一→`["process:must_call:read"]` / must_not_call 命中→`["process:must_not_call:delete"]` / order 违背→`["process:order"]` / order 跨多余调用通过 / max_calls 超限 3>2→`["process:max_calls"]` / 空 spec→pass）。
2. 实现 `EvaluateToolSequence`（greedy 子序列匹配，逐项 append Failures）。
3. 迁移 `ToolObservation` 到 domain，port 加 alias；改 `ToConfig`/`ApplyConfig`（包裹布局优先分支条件更新为 `cfg.JudgeSpec != nil || cfg.ToolSpec != nil || cfg.StepJudge != nil || cfg.Generation != nil`；裸 JudgeSpec 回退保留）。
4. 测试 `ToConfig`/`ApplyConfig` round-trip（含 tool_spec+step_judge）、`FormatToolSequence`、编译断言 `var _ port.ToolObservation = domain.ToolObservation{}`。
5. `pkg/constants/evaluation.go` 加 `StepJudgeMaxTools = 20`、`StepJudgeRawTextMaxChars = 500`（`FormatToolSequence` 截断用；常量必须先于 Task 4 存在）。
6. `go test ./internal/evaluation/domain/...` 全绿 → commit。

### Task 2: 执行链路工具序列投影（ExecutionResult.Tools + 共享 helper）

**Files:** Modify `api/wiring/evaluation_agent_adapter.go`、`api/wiring/evaluation.go`；Append `api/wiring/evaluation_agent_adapter_test.go`

**Interfaces:**

```go
// port/evaluation.go — ExecutionResult 追加
    Tools []ToolObservation // §6.5 过程断言数据源；无工具调用/未采集时为空
// api/wiring/evaluation.go — 从 mapEvaluationEvidence 提取共享 helper
func mapToolObservations(tools []agentdomain.ToolObservation) []evalport.ToolObservation
```

**Steps (TDD):**

1. 失败测试 `TestAgentEvaluationAdapterPropagatesToolObservations`：fake executor 返回带 ToolObservations（含 Arguments/RawText）的 `*agentapp.AgentResult`，断言 Tools 逐字段拷贝、顺序保持。
2. 提取 `mapToolObservations`，`mapEvaluationEvidence` 内 tools 改用之（消除重复）。
3. `agentEvaluationAdapter.ExecuteRevision` 返回追加 `Tools: mapToolObservations(result.ToolObservations)`。
4. `agentScenarioEvaluationAdapter.ExecuteRevision`（evaluation.go）同样追加（`ExecuteSkillScenario` 返回 `*agentapp.AgentResult`）。
5. `go test ./api/wiring/... -run 'AgentEvaluation|SkillScenario|Evaluation'` → commit。

**注意**：Arguments 原样透传，脱敏在 Task 5 落库层做。

### Task 4: judgeAdapter 步骤级评分拼装

**Files:** Modify `port/evaluation.go`（JudgeRequest）、`api/wiring/evaluation.go`（`judgeAdapter.Judge`）；Create `api/wiring/evaluation_judge_adapter_test.go`

**Interfaces:**

```go
// port/evaluation.go — JudgeRequest 追加（按值 plain struct，向后兼容）
    ToolSequence string // 工具序列文本（step_judge 输入）；空 = 无需步骤级评分
```

**Steps (TDD):**

1. 失败测试：fake completer 捕获 `CompletionRequest`，断言 `ToolSequence` 非空时 user 消息含 `"Tool sequence:\n..."`；为空时与现状完全一致。
2. `judgeAdapter.Judge`：user content 拼接改变量，`req.ToolSequence != ""` 追加 `"\n\nTool sequence:\n"+req.ToolSequence`。system 消息、`parseJudgeResponse`/`parseJudgeDimensions` 零改动。
3. `go test ./api/wiring/... -run 'JudgeAdapter'` → commit。
4. 注：`StepJudgeMaxTools`/`StepJudgeRawTextMaxChars` 常量已在 Task 1 新增，本任务不再重复。

### Task 3: runCase 过程断言判定流

**Files:** Modify `internal/evaluation/application/service.go`、`internal/evaluation/domain/evaluation.go`；Append `internal/evaluation/application/service_test.go`

**Interfaces:**

```go
// domain/evaluation.go — EvalCaseResult 追加（json:"process_pass" 无 omitempty 仿 passed）
ProcessPass    bool               `json:"process_pass"`
ProcessFailure string             `json:"process_failure,omitempty"`
Tools          []ToolObservation  `json:"tools,omitempty"`

// service.go
type processVerdict struct { Passed bool; Failure string; Dimensions []domain.DimensionScore }
func (s *Service) evaluateProcess(ctx context.Context, testCase domain.EvalCase, result domain.EvalCaseResult) (processVerdict, error)
func (s *Service) judgeProcess(ctx context.Context, testCase domain.EvalCase, stepJudge domain.StepJudge, result domain.EvalCaseResult) (domain.AssertionResult, error)
```

**Steps (TDD):**

1. 失败测试（service_test.go，复用 fakeAdapter 扩展 tools）：
   - tool_spec `must_not_call:["delete"]` 命中 + output 通过 → `ProcessPass=false`/`Passed=false`/`ProcessFailure="process:must_not_call:delete"`；
   - step_judge pass（fake LLMJudge）→ `ProcessPass=true`、维度并入 `result.Dimensions`；
   - step_judge disabled → `result.Error` 非空、`FailureReason="execution"`；
   - 无 process 断言 → `ProcessPass=true`。
2. 实现 `judgeProcess`：`port.JudgeRequest{Model:"", Rubric:stepJudge.Criteria, Input:marshal(testCase.Input), ExpectedOutput:..., Actual:..., ToolSequence: domain.FormatToolSequence(result.Tools)}`；judge nil/disabled → fail-closed error。
3. 实现 `evaluateProcess`：toolNames 从 `result.Tools` 提取；`ToolSpec!=nil` → `EvaluateToolSequence`；`StepJudge!=nil` → `judgeProcess`，失败归因 `judgeFailureReason(ja)`；append 维度。
4. 重构 `runCase`：`result.Tools = execution.Tools`；调用 `evaluateProcess`（error → FailureReason="execution" 返回）；`result.ProcessPass/ProcessFailure` 赋值；judge 分支 `result.Passed = assertion.Passed && result.ProcessPass`，规则分支同。**本任务不改 `TryEscalateCaseResult` 调用签名**（接口新增参数在 Task 6 落地，届时统一更新两个调用点）。

   `FailureReason` 保持输出归因（judge 分支 `judgeFailureReason`、规则分支 `assert:mode`），过程归因单独在 `ProcessFailure`。
5. `go test ./internal/evaluation/application/...` → commit。

### Task 5: 持久化（DDL + run_repository + 脱敏）

**Files:** Modify `pkg/storage/postgres/tenant_schema.sql`、`internal/evaluation/infrastructure/persistence/run_repository.go`；Append `run_repository_test.go`

**DDL（tenant-only）：**

```sql
-- CREATE TABLE IF NOT EXISTS eval_case_results 同步加：
    process_pass     BOOL NOT NULL DEFAULT true,
    process_failure  TEXT NOT NULL DEFAULT '',
    tool_sequence    JSONB NOT NULL DEFAULT '[]'::jsonb,
-- 升级历史租户：
ALTER TABLE eval_case_results ADD COLUMN IF NOT EXISTS process_pass BOOL NOT NULL DEFAULT true;
ALTER TABLE eval_case_results ADD COLUMN IF NOT EXISTS process_failure TEXT NOT NULL DEFAULT '';
ALTER TABLE eval_case_results ADD COLUMN IF NOT EXISTS tool_sequence JSONB NOT NULL DEFAULT '[]'::jsonb;
```

**Interfaces:**

```go
// run_repository.go
func sanitizeTools(tools []domain.ToolObservation) []domain.ToolObservation
```

**Steps (TDD):**

1. 失败测试 `TestSanitizeTools`：Arguments 含 password/api_key → 替换；RawText 含 `Authorization=Bearer x` → 正则替换；普通字段原样。
2. `SaveRun` INSERT 14→17 列：process_pass、process_failure、tool_sequence（`json.Marshal(sanitizeTools(result.Tools))`，null→`[]`）。
3. `GetRun` SELECT 追加 3 列并 Scan。
4. round-trip 测试保留三字段。
5. `go test ./internal/evaluation/infrastructure/persistence/...` → commit。

### Task 6: 评审池 process_output_conflict

**Files:** Modify `internal/evaluation/domain/review_pool.go`、`internal/evaluation/domain/port/review.go`、`internal/evaluation/application/review_service.go`、`internal/evaluation/application/service.go`

**Interfaces:**

```go
// review_pool.go
TriggerProcessOutputConflict ReviewTriggerReason = "process_output_conflict" // 并入 Valid()
func TriggersForProcessConflict(outputPass, processPass bool) []ReviewTriggerReason
func TriggersForCaseResult(needsReview bool, outputPass, processPass bool, assertion AssertionResult, cfg ReviewConfig) []ReviewTriggerReason
// port/review.go — ReviewEscalator 签名追加
TryEscalateCaseResult(ctx, tenantID, runID string, result domain.EvalCaseResult, c domain.EvalCase, assertion domain.AssertionResult, outputPass, processPass bool) error
```

**Steps (TDD):**

1. 失败测试：`TestTriggersForProcessConflict`：(true,false)→`[process_output_conflict]`，(true,true)/(false,false)/(false,true)→空；`TestTriggersForCaseResult`：低置信+output pass+process fail→`[low_confidence, process_output_conflict]`；`TestTryEscalateCaseResultRuleCaseOnlyConflict`：规则 case + 冲突→仅 `process_output_conflict` 不误触发 low_confidence；`caseSnapshot` 含新字段。
2. `ReviewService.TryEscalateCaseResult` 按 `AssertionMode` 分支（judge → 完整 TriggersForCaseResult；规则 → 仅 TriggersForProcessConflict）。
3. `caseSnapshot` 加 `process_pass`/`process_failure`/`tool_sequence`。
4. `runCase` 两分支调用更新签名（fail-open 语义不变）。
5. `go test ./internal/evaluation/domain/... ./internal/evaluation/application/...` → commit。

### Task 7: metrics process_pass_rate

**Files:** Modify `internal/evaluation/application/metrics.go`；Append `metrics_test.go`

**Steps (TDD):**

1. 失败测试：2 结果（1 processPass）→ `0.5`；含 step_judge 维度的结果 → by_dimension 自动出现 tool_pass/step_reasoning。
2. `aggregateRunMetrics` init map 加 `"process_pass_rate": 0.0`；`len(run.Results)>0` 分支后按 ProcessPass 计数算率（分母=全部 evaluated 结果）。
3. `go test ./internal/evaluation/application/...` → commit。

### Task 8: 前端

**Files (11):** `web/src/modules/evaluation/model/evaluation.ts`、`components/CaseFields.tsx`、`components/CreateSuiteModal.tsx`、`components/CreateEvaluationModal.tsx`、`pages/EvaluationCenterPage.tsx`、`components/EditDraftCaseModal.tsx`、`components/SuiteDrawer.tsx`、`components/RunAttributionPanel.tsx`、`components/RunMetricPanel.tsx`、`components/ReviewPoolPanel.tsx`、`web/src/constants/index.ts`

**TS 类型（model/evaluation.ts）：**

```ts
export const toolObservationSchema = z.object({
  tool_name: z.string(), tool_type: z.string().optional(), step_index: z.number().optional(),
  provider_type: z.string().optional(), capability_id: z.string().optional(),
  arguments: z.record(z.string(), z.unknown()).optional(), raw_text: z.string().optional(),
});
export const toolSpecSchema = z.object({ must_call: z.array(z.string()).optional(), must_not_call: z.array(z.string()).optional(), order: z.array(z.string()).optional(), max_calls: z.number().optional() }).optional();
export const stepJudgeSchema = z.object({ criteria: z.string().optional() }).optional();
// evaluationCaseSchema 加 tool_spec/step_judge；run result schema 加 process_pass: z.boolean(), process_failure: z.string().optional(), tools: z.array(toolObservationSchema).optional()
```

**组件：**

- `CaseFields.tsx` 新增 `ToolSpecFields`（must_call/must_not_call/order 用 `Select mode="tags"`、max_calls 用 `InputNumber min={1} max={EVALUATION_MAX_CALLS_LIMIT}`）+ `StepJudgeFields`（criteria 用 `Input.TextArea`）——**始终可见，与 assertion_mode 正交**（不用 `Form.useWatch` 门控）。
- `CreateSuiteModal`/`CreateEvaluationModal`：Values 加 `must_call?/must_not_call?/tool_order?/max_calls?/step_criteria?`，渲染两组件。
- `EvaluationCenterPage` cases 载荷映射：`tool_spec: (must_call?.length||must_not_call?.length||tool_order?.length||max_calls) ? { must_call, must_not_call, order: tool_order, max_calls } : undefined`；`step_judge: step_criteria ? { criteria } : undefined`。
- `EditDraftCaseModal` Alert 扩为「AI 判定、工具序列与步骤判定在 case 进入草稿时确定，编辑不可修改」。
- `SuiteDrawer` `caseChildren` 加 tool_spec 摘要 + step_judge criteria 展示。
- `RunAttributionPanel` `CaseDrillDown` 加输出/过程双 Tag + process_failure + 工具序列（超 200 行抽 `ToolSequenceTable`）。
- `RunMetricPanel` `metricLabels` 加 `process_pass_rate: '过程通过率'`，百分比格式分支含它。
- `ReviewPoolPanel` `REASON_LABELS` 加 `process_output_conflict: '输出通过但过程未通过'`。
- `web/src/constants/index.ts`：`export const EVALUATION_MAX_CALLS_LIMIT = 50;`。

**Steps (TDD)：** 各组件先更新 `*.test.tsx` → 实现 → `npx vitest run` + `npx tsc --noEmit` → commit。

### Task 9: 契约同步 + proto-gen + 验收

**Files:** Modify `proto/evaluation/evaluation.proto`、`api/http/handler/evaluation_handler.go`、`api/http/testdata/contracts/*`（golden 变化时）、`test/e2e/`（§6.5 场景）

**proto（唯一事实源）：**

```proto
message ToolSpec {
  repeated string must_call = 1;
  repeated string must_not_call = 2;
  repeated string order = 3;
  int32 max_calls = 4; // 0 = 不限制
}
message StepJudge { string criteria = 1; }
// EvaluationCaseRequest 追加：optional ToolSpec tool_spec = 7; optional StepJudge step_judge = 8;
```

**handler（CreateSuite 同款拷贝）：** `item.ToolSpec != nil` → 拷入 `domain.ToolSpec`；`item.StepJudge != nil` → 拷入；caseArgs audit 载荷同步加。

**Steps:**

1. `make proto-gen`（生成 DTO，不入 git；`make be-test` 依赖）。
2. `go build ./...`；`go test ./api/http/...`——`ProcessPass` 出现在 run 结果 JSON 时同步 golden（重点 `get_evaluations_runs.golden.json`、`get_evaluations_suites.golden.json`）。
3. e2e（R3）：agent 评测含 tool_spec 用例，验证 output pass + 禁用工具命中 → run 失败 + 入评审池（process_output_conflict）。
4. `make test-verify-before-pr` 全量通过，clean commit。

---

## 验证清单

| 层 | 命令 |
|---|---|
| domain | `go test ./internal/evaluation/domain/...` |
| application | `go test ./internal/evaluation/application/...` |
| persistence | `go test ./internal/evaluation/infrastructure/persistence/...` |
| wiring | `go test ./api/wiring/... -run 'JudgeAdapter|AgentEvaluation|SkillScenario'` |
| handler/dto | `make proto-gen && go test ./api/http/...` |
| 全量 Go | `make be-test` |
| 前端 | `cd web && npx tsc --noEmit && npx vitest run` |
| 最终 | `make test-verify-before-pr`（PR 前）+ clean commit 系统验收（stratum-e2e-tester） |

## 主要风险与对策

1. **domain↔port 循环** → ToolObservation 迁 domain + port alias（Task 1 首做，编译断言护航）。
2. **规则分支低置信误触发** → TryEscalateCaseResult 按 AssertionMode 分支（Task 6 单测显式覆盖）。
3. **工具序列敏感数据** → sanitizeTools 复用 sanitizeValue/sensitiveText（Task 5 专测）。
4. **step_judge fail-closed** → evaluateProcess 返回 error，不静默 pass。
5. **golden 破坏** → ProcessPass 无 omitempty，Task 9 全量 be-test 兜底。
