# Plan+Execute × ReAct 融合架构设计

**日期**: 2026-07-09
**状态**: 待实现
**作者**: byteBuilderX

---

## 1. 背景与动机

当前 ReAct 实现（`internal/agent/application/graph/react.go`）是两节点循环图（`llm → tool → llm`），以 `MaxLLMSteps` 硬截断兜底。核心问题：**无法保证复杂多步任务的确定性执行**——模型在隐式维护计划，context 随 tool observations 膨胀，超出迭代上限后强制结束而非优雅完成。

**目标**：简单任务零额外开销快速返回；复杂任务自动切换到结构化执行路径，每步有边界、可检查点、可恢复。

---

## 2. 设计原则

1. **零破坏兼容**：`BuildReActGraph` 和现有测试不做任何修改
2. **懒规划**：规划开销只在任务确实需要时才产生
3. **context 隔离**：步骤间只传 Summary，不传原始 tool observations
4. **持久化容错**：每步边界写 checkpoint，任何中断均可恢复
5. **复用优先**：sub-step 执行复用现有 `BuildReActGraph`

---

## 3. 整体架构

```
用户 Query
    ↓
[nodeLLM] ←─────────────── ReAct 主循环（保持不变）
    │  ↑
[nodeTool]
    │
    ├── (tool calls) → nodeTool
    ├── (done)       → END
    └── (stuck: steps≥StuckThreshold, 无Output) ──→ [nodeReflect]
                                                          ↓
                                                     [nodePlan]  ←─ 记忆模板召回
                                                          ↓
                                                  [nodeCheckpoint]  ←─ 流式推送计划（可配置）
                                                          ↓
                                               [nodeStepOrchestrator]  ←─ DAG 依赖分析
                                               ├── [nodeStepExecutor] (sub-ReActGraph)
                                               ├── [nodeStepExecutor] (并行)
                                               └── [nodeStepExecutor]
                                                          ↓
                                                  [nodeSynthesize]
                                                          ↓
                                                 [nodeMemoryStore]  ←─ 存计划模板
                                                          ↓
                                                         END
```

---

## 4. 状态设计

### 4.1 新增类型（`internal/agent/domain/checkpoint.go`）

```go
type PlanStep struct {
    Goal      string   `json:"goal"`
    HintTools []string `json:"hint_tools,omitempty"`
    DependsOn []int    `json:"depends_on,omitempty"` // 空 = 可与其他无依赖步骤并行
}

type StepResult struct {
    StepIndex int
    Goal      string
    Summary   string // 等于 sub-ReActGraph 的 ReActState.Output
    Success   bool
    Error     string
}

type CheckpointPhase string

const (
    PhasePlanned  CheckpointPhase = "planned"   // Plan 生成，未开始执行
    PhaseStepDone CheckpointPhase = "step_done" // 某步完成
)

type PlanCheckpoint struct {
    ID                string
    TenantID          string
    AgentID           string
    ConversationID    string
    TraceID           string
    Model             string
    LLMAPIKeys        map[string]string
    Messages          []port.LLMMessage
    ReActIterations   int
    ReflectionSummary string
    Plan              []PlanStep
    CurrentStepIndex  int
    StepResults       []StepResult
    Phase             CheckpointPhase
    CreatedAt         time.Time
    ExpiresAt         time.Time
}
```

### 4.2 ReActState 扩展（`react.go`，仅追加字段，不改现有字段）

```go
// 懒规划字段
StuckThreshold    int    // 0 = 禁用；>0 = K轮未收敛触发规划
PlanTriggered     bool

// 规划阶段
ReflectionSummary string
Plan              []PlanStep
PlanTemplateID    string // 空 = 全新生成

// 执行阶段
CurrentStepIndex  int
StepResults       []StepResult

// Checkpoint
CheckpointID      string // 非空 = 这是一次 resume 执行
CheckpointEnabled bool   // true = 流式推送计划给前端
```

**"stuck" 判定条件**：

```
s.Steps >= s.StuckThreshold && s.Output == "" && !s.PlanTriggered && s.StuckThreshold > 0
```

---

## 5. 图拓扑（`internal/agent/application/graph/plan_execute.go`）

`BuildPlanExecuteGraph` 在 `BuildReActGraph` 基础上扩展三路条件边：

```go
g.AddConditionalEdge(nodeLLM, func(s ReActState) string {
    if len(s.Messages) > 0 {
        last := s.Messages[len(s.Messages)-1]
        if last.Role == "assistant" && len(last.ToolCalls) > 0 {
            return nodeTool
        }
    }
    if isStuck(s) {
        return nodeReflect
    }
    return END
})

g.AddNode(nodeReflect,         makeReflectNode(...))
g.AddNode(nodePlan,            makePlanNode(...))
g.AddNode(nodeCheckpoint,      makeCheckpointNode(...))
g.AddNode(nodeStepOrchestrator,makeStepOrchestratorNode(...))
g.AddNode(nodeStepExecutor,    makeStepExecutorNode(...))
g.AddNode(nodeSynthesize,      makeSynthesizeNode(...))
g.AddNode(nodeMemoryStore,     makeMemoryStoreNode(...))

g.AddEdge(nodeReflect,         nodePlan)
g.AddEdge(nodePlan,            nodeCheckpoint)
g.AddEdge(nodeCheckpoint,      nodeStepOrchestrator)
g.AddEdge(nodeStepOrchestrator,nodeStepExecutor)
g.AddEdge(nodeStepExecutor,    nodeSynthesize)   // 所有 wave 完成后
g.AddEdge(nodeSynthesize,      nodeMemoryStore)
g.AddEdge(nodeMemoryStore,     END)
```

---

## 6. 各节点职责

### 6.1 nodeReflect

- 输入：当前 messages + 工具调用历史
- 一次 LLM 调用（精简 prompt，不含完整历史）
- 输出：`ReflectionSummary`，说明卡住原因 + 所需子目标
- 写入 checkpoint（Phase=executing 中间态）

### 6.2 nodePlan

- 先调用 `RecallMemoryFn` 查找相似计划模板
- 一次 LLM 调用，强制 JSON Schema 输出 `[]PlanStep`
- 写入 checkpoint（Phase=planned）
- 输出：`Plan []PlanStep`，`PlanTemplateID`

**JSON Schema 约束**：

```json
{
  "type": "array",
  "items": {
    "type": "object",
    "required": ["goal"],
    "properties": {
      "goal":       {"type": "string"},
      "hint_tools": {"type": "array", "items": {"type": "string"}},
      "depends_on": {"type": "array", "items": {"type": "integer"}}
    }
  }
}
```

### 6.3 nodeCheckpoint（非阻塞 v1）

- 若 `CheckpointEnabled`：通过 `OnToken` 推送 `event: plan_checkpoint` 事件（含 Plan JSON）
- 立即继续执行（不阻塞）
- 阻塞式人工审批为 Phase 2（需要执行态持久化 + Resume API）

### 6.4 nodeStepOrchestrator

- 分析 `Plan[*].DependsOn`，构建 DAG
- 分层为执行 waves：

```
Wave 0: 所有 DependsOn 为空的步骤（并行）
Wave 1: 所有依赖仅来自 Wave 0 的步骤
...
```

- 按 wave 顺序驱动 `nodeStepExecutor`

### 6.5 nodeStepExecutor

- 每步构建独立 `ReActState`：

```go
stepState := ReActState{
    TenantID:       s.TenantID,
    Model:          s.Model,
    LLMAPIKeys:     s.LLMAPIKeys,
    AvailableTools: s.AvailableTools,
    SkillToolIndex: s.SkillToolIndex,
    MaxLLMSteps:    3,           // 小 budget，外层 Plan 提供结构
    RAGSearchFn:    s.RAGSearchFn,
    RecallMemoryFn: s.RecallMemoryFn,
    Messages:       buildStepMessages(step, s.StepResults), // goal + 前序摘要
}
result, _ := compiledReActGraph.Run(ctx, stepState)
```

- 写入 `StepResult{Summary: result.Output, Success: result.Output != ""}`
- 写入 checkpoint（Phase=step_done，CurrentStepIndex=N）

**buildStepMessages**：

```
[system] agent system prompt
[user]   "当前任务目标：{step.Goal}\n\n前置步骤摘要：\n{join(prevResults.Summary)}"
```

### 6.6 nodeSynthesize

- 检查最后一步的 `DependsOn` 是否覆盖所有前序步骤：
  - 是 → 最后一步 Output 即最终答案，跳过 LLM 调用
  - 否（独立并行步骤）→ 一次 LLM 聚合所有 `StepResult.Summary`

### 6.7 nodeMemoryStore

- 仅当所有 `StepResult.Success == true` 时写入
- 存储 `{Plan, QueryEmbedding}` 作为未来召回的模板

---

## 7. Checkpoint + 容错恢复

### 7.1 存储接口（`internal/agent/domain/port/repository.go`）

```go
type PlanCheckpointRepo interface {
    Save(ctx context.Context, cp domain.PlanCheckpoint) error
    Load(ctx context.Context, id string) (domain.PlanCheckpoint, error)
    Delete(ctx context.Context, id string) error
}
```

### 7.2 实现（`internal/agent/infrastructure/persistence/checkpoint_store.go`）

- **Redis**（热）：TTL=24h，key=`plan_checkpoint:{id}`，value=JSON
- **PostgreSQL**（冷）：tenant schema 下 `plan_checkpoints` 表

```sql
-- pkg/storage/postgres/tenant_schema.sql
CREATE TABLE IF NOT EXISTS plan_checkpoints (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   TEXT NOT NULL,
    agent_id    UUID NOT NULL,
    phase       TEXT NOT NULL,
    state       JSONB NOT NULL,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    expires_at  TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_plan_checkpoints_tenant_agent
    ON plan_checkpoints(tenant_id, agent_id);
```

### 7.3 写入时机

| 时机 | Phase | 触发节点 |
|------|-------|---------|
| Plan 生成完成 | planned | nodePlan |
| Step N 完成 | step_done | nodeStepExecutor |
| 步骤失败 | 不写 | — |

### 7.4 Resume 流程（`agent_service.go`）

```
接收 ResumeRequest{CheckpointID, Approved, ModifiedPlan}
  → Load(checkpointID)
  → 若 ModifiedPlan 非空：覆盖 Plan，清空 StepResults
  → 重建 ReActState（注入 OnToken/RAGSearchFn/RecallMemoryFn）
  → 设 PlanTriggered=true，跳过 ReAct+Reflect+Plan
  → 入口节点：nodeStepOrchestrator
  → 新 SSE 流推送给客户端
```

### 7.5 覆盖的失败场景

| 场景 | 恢复点 |
|------|--------|
| 进程崩溃 / OOM | 最后 step_done checkpoint |
| AgentExecTimeout（90s）| 同上 |
| 单步 LLM 超时 | sub-ReActGraph 内重试3次，耗尽→报错，checkpoint 保留 |
| Skill 执行失败 | 同上 |
| 人工审批暂停（Phase 2）| planned checkpoint |

### 7.6 Resume API

```
POST /agents/:id/executions/resume
Body: {
  "checkpoint_id": "uuid",
  "approved": true,
  "modified_plan": [...]   // 可选，替换原计划
}
Response: SSE stream
```

---

## 8. 配置

```go
// pkg/constants/agent.go
DefaultStuckThreshold = 3        // K 轮未收敛触发规划
PlanCheckpointTTL     = 24 * time.Hour
MaxPlanSteps          = 10       // 单次规划最多步骤数
DefaultStepMaxLLMSteps= 3        // 每个 sub-step 的 LLM budget
```

`AgentConfig` 新增字段（`internal/agent/domain/agent.go`）：

```go
StuckThreshold    int  // 0 = 禁用懒规划；默认 DefaultStuckThreshold
CheckpointEnabled bool // 是否流式推送计划
```

---

## 9. 测试用例

### T1：懒规划触发边界

| ID | 场景 | K | 迭代 | 预期 |
|----|------|---|------|------|
| T1-1 | 简单任务 | 3 | 第2轮出答案 | 不触发规划 |
| T1-2 | 卡住任务 | 3 | 3轮无答案 | 触发 Reflect→Plan |
| T1-3 | 临界值 | 3 | 第3轮出答案 | 不触发 |
| T1-4 | 禁用 | 0 | 任意 | 永远纯 ReAct |

### T2：步骤编排

| ID | Plan 结构 | 预期执行 |
|----|-----------|---------|
| T2-1 | 3步全无依赖 | Wave 0: 3步并发 |
| T2-2 | A→B→C 链 | 3个 Wave 串行 |
| T2-3 | A,B独立→C | Wave 0: A∥B，Wave 1: C |
| T2-4 | 单步 | 不并发，Synthesize 直接取 Output |
| T2-5 | 空计划 | 返回错误，不执行 |

### T3：Context 隔离

```
T3-1: Step 0 的 tool messages 不出现在 Step 1 的 messages 中
T3-2: Step 0 的 Summary 出现在 Step 1 的 user context 里
T3-3: 各 StepResult.Summary 独立，不含其他步骤的 raw observations
```

### T4：Checkpoint 正确性

| ID | 操作 | 验证 |
|----|------|------|
| T4-1 | Plan 生成后 | Phase=planned, len(StepResults)=0 |
| T4-2 | Step 0 完成 | Phase=step_done, CurrentStepIndex=0, len(StepResults)=1 |
| T4-3 | Step 2 完成 | CurrentStepIndex=2, len(StepResults)=3 |
| T4-4 | 步骤失败 | Checkpoint 不更新，保留上一成功状态 |
| T4-5 | TTL 过期 | Load 返回 not found |

**不变量**：执行完成后 `len(StepResults) == CurrentStepIndex + 1`

### T5：Resume 场景

| ID | 恢复点 | 操作 | 预期 |
|----|--------|------|------|
| T5-1 | planned | 正常 resume | 从 Step 0 开始 |
| T5-2 | step_done(1) | 模拟崩溃后 resume | 跳过 Step 0-1，执行 Step 2+ |
| T5-3 | planned | modified_plan 提交 | 用新计划，清空 StepResults |
| T5-4 | 任意 | approved=false | 执行停止 |
| T5-5 | step_done(1) | 重复 resume | 幂等或冲突错误 |

### T6：记忆模板

```
T6-1: 全步成功 → MemoryStore 调用，模板写入
T6-2: 任一步失败 → MemoryStore 不调用
T6-3: 相似 query → RecallMemoryFn 返回模板，LLM 以模板为基础修订
T6-4: 无匹配模板 → LLM 从零生成，PlanTemplateID=""
```

### T7：E2E 黄金路径

```
输入: 多步任务（需要RAG + 数据分析 + 报告生成）
执行链:
  ReAct × 3（无法单步完成）
  → Reflect → Plan（3步：Step0∥Step1→Step2）
  → Wave0: Step0(RAG) ∥ Step1(分析) 并发
  → Wave1: Step2(报告)
  → Synthesize: 跳过（Step2 已是终结步骤）
  → MemoryStore: 写入模板
验证: 3个 StepResult.Success=true，checkpoint 写入3次
```

### T8：崩溃恢复 E2E

```
同T7，Step 1 完成后注入 context.Canceled
验证: checkpoint 保存 Step 0-1 结果
Resume 后: 仅执行 Step 2，Step 0-1 不重新执行
最终 Output 与 T7 一致
```

---

## 10. 文件变更总览

| 操作 | 文件 |
|------|------|
| 新建 | `internal/agent/application/graph/plan_execute.go` |
| 新建 | `internal/agent/application/graph/plan_execute_test.go` |
| 新建 | `internal/agent/domain/checkpoint.go` |
| 新建 | `internal/agent/infrastructure/persistence/checkpoint_store.go` |
| 新建 | `internal/agent/infrastructure/persistence/checkpoint_store_test.go` |
| 修改 | `internal/agent/application/graph/react.go`（ReActState 字段 + 条件边） |
| 修改 | `internal/agent/application/agent_service.go`（resume 分支） |
| 修改 | `internal/agent/domain/port/repository.go`（PlanCheckpointRepo 接口） |
| 修改 | `internal/agent/domain/agent.go`（AgentConfig 新字段） |
| 修改 | `api/http/handler/agent_crud_handler.go`（resume handler） |
| 修改 | `api/http/router.go`（POST /executions/resume） |
| 修改 | `api/http/dto/request.go`（ResumeExecutionRequest） |
| 修改 | `api/wiring/agent.go`（注入 CheckpointStore） |
| 修改 | `pkg/constants/agent.go`（新常量） |
| 修改 | `pkg/storage/postgres/tenant_schema.sql`（plan_checkpoints 表） |

---

## 11. LLM 调用开销（最坏情况）

| 节点 | 调用次数 |
|------|---------|
| ReAct 早期循环 | K 次 |
| Reflect | 1 次 |
| Plan | 1 次 |
| StepExecutor × N 步 | N × 3 次 |
| Synthesize（按需） | 0–1 次 |
| **合计** | K + 2 + 3N |

简单任务（未触发规划）：K 次，与现有 ReAct 完全相同。

---

## 12. 不在本次 Spec 范围内

- 阻塞式人工审批（需执行态持久化 + 前端审批页）→ Phase 2 独立 spec
- 计划步骤粒度自适应调整（动态 K）→ Phase 3
- 跨 agent 协作执行 → 独立 spec
