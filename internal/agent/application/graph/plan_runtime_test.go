package graph_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestPlanWaveFoldsBackSlotTokensIntoParentTotal 验证 plan 槽位子循环的 token
// 用量折回父图预算账本（Finding 1 修复）：子循环 delta 经
// PlanNodeExecutionResult.TokensUsed 叠加进父状态 TotalTokens；并行波次的多个
// 槽位各折回自身 delta，父图基线只计一次（与 MergeReActWave 的 base-relative
// 语义一致，无重复计数）。
func TestPlanWaveFoldsBackSlotTokensIntoParentTotal(t *testing.T) {
	cg, stub := planWaveTestGraph(t)
	// 父图首轮 LLM 调用消耗 500：折回必须叠加在基线上，基线不得重复计入。
	stub.responses[0].Usage = port.TokenUsage{Total: 500}
	state := runtimeStateWithPlan([]domain.PlanNode{
		{ID: "a", Goal: "a", Status: domain.PlanNodeStatusPending},
		{ID: "b", Goal: "b", Status: domain.PlanNodeStatusPending},
	})
	state.Model = "qwen-turbo"
	state.Messages = []port.LLMMessage{{Role: "user", Content: "run the plan"}}
	state.PlanNodeExecutor = func(_ context.Context, _ graph.ReActState, node domain.PlanNode, _ map[string]string) (graph.PlanNodeExecutionResult, error) {
		return graph.PlanNodeExecutionResult{Summary: node.ID + " done", TokensUsed: 1500}, nil
	}

	out, err := cg.Invoke(context.Background(), state, graph.RunConfig[graph.ReActState]{
		MaxSteps: 20, MaxParallel: 2, MergeWave: graph.MergeReActWave,
	})
	require.NoError(t, err)
	// 基线 500 只计一次 + 两个槽位各折回 1500 = 3500。
	require.Equal(t, 3500, out.TotalTokens)
	require.Equal(t, domain.PlanNodeStatusSucceeded, out.ActivePlan.Nodes[0].Status)
}

// TestPlanWaveBudgetTerminatesAfterSlotFoldBack 验证父图成本预算检查点在槽位
// 折回后生效：子循环用量 + 基线超预算时，finalize 后父图下一次 LLM 检查点
// 业务终止整次执行。修复前子循环用量在返回时丢失，父图预算形同虚设（每个
// 槽位独立放宽 ≈ 一个 cap）。
func TestPlanWaveBudgetTerminatesAfterSlotFoldBack(t *testing.T) {
	cg, stub := planWaveTestGraph(t)
	state := runtimeStateWithPlan([]domain.PlanNode{
		{ID: "a", Goal: "a", Status: domain.PlanNodeStatusPending},
	})
	state.Model = "qwen-turbo"
	state.Messages = []port.LLMMessage{{Role: "user", Content: "run"}}
	state.PlanNodeExecutor = func(_ context.Context, _ graph.ReActState, node domain.PlanNode, _ map[string]string) (graph.PlanNodeExecutionResult, error) {
		return graph.PlanNodeExecutionResult{Summary: node.ID + " done", TokensUsed: 1500}, nil
	}
	// 预算低于折回后的累计：finalize 后父图 LLM 检查点必须终止，不再发起后续调用。
	state.MaxTokensPerExecution = 1200

	out, err := cg.Invoke(context.Background(), state, graph.RunConfig[graph.ReActState]{
		MaxSteps: 20, MaxParallel: 2, MergeWave: graph.MergeReActWave,
	})
	require.NoError(t, err)
	require.Equal(t, graph.CostBudgetTerminated, out.TerminatedBy)
	require.Equal(t, 1500, out.TotalTokens)
	require.Equal(t, domain.PlanNodeStatusSucceeded, out.ActivePlan.Nodes[0].Status)
	// 折回终止发生在波次汇合后的那次 LLM 调用上：共 2 次（排程 + 终止检查），
	// 终止后不再多调。
	require.Len(t, stub.llmReqs, 2)
}

// planWaveTestGraph builds the real ReAct graph whose first LLM response
// schedules a stratum_continue_plan wave and whose second response ends the run.
func planWaveTestGraph(t *testing.T) (*graph.CompiledGraph[graph.ReActState], *capGWSequence) {
	t.Helper()
	stub := &capGWSequence{responses: []port.CapabilityResponse{
		{ToolCalls: []port.ToolCall{{ID: "call-1", Name: "stratum_continue_plan", Arguments: map[string]any{"expected_revision": float64(1)}}}},
		{Content: "all nodes finished"},
	}}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)
	return cg, stub
}

// TestPlanWaveEngineRunsScheduledWave walks the full engine path: LLM schedules
// a wave, the tool node registers it, the slots run concurrently up to
// MaxParallel, the finalize join applies status/attempts/revision and restores
// the wave observation, then the LLM resumes and terminates.
func TestPlanWaveEngineRunsScheduledWave(t *testing.T) {
	cg, _ := planWaveTestGraph(t)
	state := runtimeStateWithPlan([]domain.PlanNode{
		{ID: "a", Goal: "a", Status: domain.PlanNodeStatusPending},
		{ID: "b", Goal: "b", Status: domain.PlanNodeStatusPending},
		{ID: "c", Goal: "c", DependsOn: []string{"a", "b"}, Status: domain.PlanNodeStatusPending},
	})
	state.Model = "qwen-turbo"
	state.Messages = []port.LLMMessage{{Role: "user", Content: "run the plan"}}
	var current, maximum atomic.Int32
	var executed atomic.Int32
	state.PlanNodeExecutor = func(_ context.Context, nodeState graph.ReActState, node domain.PlanNode, _ map[string]string) (graph.PlanNodeExecutionResult, error) {
		require.True(t, nodeState.PlanToolsDisabled)
		require.Nil(t, nodeState.ActivePlan)
		require.Empty(t, nodeState.PlanWavePending)
		n := current.Add(1)
		for {
			old := maximum.Load()
			if n <= old || maximum.CompareAndSwap(old, n) {
				break
			}
		}
		defer current.Add(-1)
		time.Sleep(15 * time.Millisecond)
		executed.Add(1)
		return graph.PlanNodeExecutionResult{Summary: node.ID + " done"}, nil
	}
	writer := &checkpointWriterForPlanTest{}
	state.PlanCheckpointWriter = writer

	out, err := cg.Invoke(context.Background(), state, graph.RunConfig[graph.ReActState]{
		MaxSteps: 20, MaxParallel: 2, MergeWave: graph.MergeReActWave,
	})
	require.NoError(t, err)
	require.Equal(t, int32(2), maximum.Load())
	require.Equal(t, int32(2), executed.Load())
	require.Equal(t, domain.PlanNodeStatusSucceeded, out.ActivePlan.Nodes[0].Status)
	require.Equal(t, domain.PlanNodeStatusSucceeded, out.ActivePlan.Nodes[1].Status)
	require.Equal(t, domain.PlanNodeStatusPending, out.ActivePlan.Nodes[2].Status)
	// ApplyPlanCommand(1→2) + 2 节点波次（2→3, 3→4）
	require.Equal(t, int64(4), out.ActivePlan.Revision)
	require.Equal(t, "all nodes finished", out.Output)
	// continue 的 rev checkpoint + 每节点一个 wave checkpoint
	require.Equal(t, 3, writer.calls)
	// finalize 用 wave 观察恢复 LLM 上下文，替换被跳过的工具消息
	require.Contains(t, waveObservationMessage(out.Messages, "call-1"), `"a":"succeeded"`)
	require.Equal(t, "all nodes finished", out.Messages[len(out.Messages)-1].Content)
	require.NotEmpty(t, out.ActivePlan.Nodes[0].Attempts[0].ID)
	require.Equal(t, 1, out.ActivePlan.Nodes[0].Attempts[0].Number)
}

// TestPlanWaveEngineRecoversPanicAsFailedOutcome asserts a panicking plan node
// collapses into a failed outcome and the remaining nodes of the wave still
// run (old per-node recovery semantics, now owned by the slot node).
func TestPlanWaveEngineRecoversPanicAsFailedOutcome(t *testing.T) {
	cg, _ := planWaveTestGraph(t)
	state := runtimeStateWithPlan([]domain.PlanNode{
		{ID: "panic", Goal: "p", Status: domain.PlanNodeStatusPending},
		{ID: "slow", Goal: "s", Status: domain.PlanNodeStatusPending},
	})
	state.Model = "qwen-turbo"
	state.Messages = []port.LLMMessage{{Role: "user", Content: "run"}}
	var finished atomic.Int32
	state.PlanNodeExecutor = func(_ context.Context, _ graph.ReActState, node domain.PlanNode, _ map[string]string) (graph.PlanNodeExecutionResult, error) {
		if node.ID == "panic" {
			panic("node panic")
		}
		time.Sleep(20 * time.Millisecond)
		finished.Add(1)
		return graph.PlanNodeExecutionResult{Summary: "done"}, nil
	}

	out, err := cg.Invoke(context.Background(), state, graph.RunConfig[graph.ReActState]{
		MaxSteps: 20, MaxParallel: 2, MergeWave: graph.MergeReActWave,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), finished.Load())
	require.Equal(t, domain.PlanNodeStatusFailed, out.ActivePlan.Nodes[0].Status)
	require.Equal(t, domain.PlanNodeStatusSucceeded, out.ActivePlan.Nodes[1].Status)
	require.Contains(t, out.ActivePlan.Nodes[0].Attempts[0].Error, "panic")
	require.Contains(t, waveObservationMessage(out.Messages, "call-1"), `"panic":"failed"`)
}

// waveObservationMessage returns the content of the last tool message matching
// callID, i.e. the wave observation appended by the finalize node.
func waveObservationMessage(messages []port.LLMMessage, callID string) string {
	content := ""
	for _, m := range messages {
		if m.Role == "tool" && m.ToolCallID == callID {
			content = m.Content
		}
	}
	return content
}

// TestPlanWaveFinalizePropagatesCheckpointFailure asserts a failed wave
// checkpoint aborts the run and surfaces the persistence error instead of
// pretending the wave succeeded.
func TestPlanWaveFinalizePropagatesCheckpointFailure(t *testing.T) {
	cg, _ := planWaveTestGraph(t)
	state := runtimeStateWithPlan([]domain.PlanNode{
		{ID: "a", Goal: "a", Status: domain.PlanNodeStatusPending},
		{ID: "b", Goal: "b", Status: domain.PlanNodeStatusPending},
	})
	state.Model = "qwen-turbo"
	state.Messages = []port.LLMMessage{{Role: "user", Content: "run"}}
	state.PlanNodeExecutor = func(_ context.Context, _ graph.ReActState, _ domain.PlanNode, _ map[string]string) (graph.PlanNodeExecutionResult, error) {
		return graph.PlanNodeExecutionResult{Summary: "done"}, nil
	}
	// 第一次写入（rev checkpoint）成功，波次 checkpoint 失败
	state.PlanCheckpointWriter = &flakyCheckpointWriter{err: errors.New("store down")}

	_, err := cg.Invoke(context.Background(), state, graph.RunConfig[graph.ReActState]{
		MaxSteps: 20, MaxParallel: 2, MergeWave: graph.MergeReActWave,
	})
	require.ErrorContains(t, err, "plan checkpoint")
	require.ErrorContains(t, err, "store down")
}

// TestSchedulePlanWaveRejectsWaveBeyondRevisionBudget asserts the ready-wave
// revision budget check happens at scheduling time and nothing is registered.
func TestSchedulePlanWaveRejectsWaveBeyondRevisionBudget(t *testing.T) {
	state := runtimeStateWithPlan([]domain.PlanNode{{ID: "a", Goal: "a", Status: domain.PlanNodeStatusPending}})
	// ApplyPlanCommand 先拦截 Revision >= MaxRevisions；这里让命令通过而
	// 波次预算超限：Revision(1)+1 个就绪节点 > MaxRevisions(2)。
	state.PlanLimits.MaxRevisions = state.ActivePlan.Revision + 1
	called := false
	state.PlanNodeExecutor = func(context.Context, graph.ReActState, domain.PlanNode, map[string]string) (graph.PlanNodeExecutionResult, error) {
		called = true
		return graph.PlanNodeExecutionResult{Summary: "done"}, nil
	}

	_, err := graph.ExecutePlanTool(context.Background(), &state, port.ToolCall{
		ID: "call-1", Name: "stratum_continue_plan", Arguments: map[string]any{"expected_revision": float64(1)},
	})
	require.ErrorIs(t, err, domain.ErrPlanBudgetExceeded)
	require.False(t, called)
	require.Empty(t, state.PlanWavePending)
	require.Empty(t, state.PlanContinueCallID)
}

func runtimeStateWithPlan(nodes []domain.PlanNode) graph.ReActState {
	return graph.ReActState{
		TenantID: "tenant-1", ExecutionID: "exec-1", TraceID: "trace-1", ConversationID: "conv-1",
		ActivePlan:   &domain.Plan{ID: "plan-1", Revision: 1, Status: domain.PlanStatusActive, Nodes: nodes},
		PlanIDSource: func() string { return "generated" }, PlanLimits: domain.PlanLimits{MaxNodes: 10, MaxRevisions: 10, MaxConcurrentNodes: 2},
		CheckpointEnabled: true, PlanCheckpointWriter: &checkpointWriterForPlanTest{}, PlanCheckpointIdentity: graph.PlanCheckpointIdentity{CheckpointID: "cp-1"},
	}
}

// flakyCheckpointWriter succeeds once (the rev checkpoint) then fails every
// subsequent write, isolating wave-checkpoint failure paths.
type flakyCheckpointWriter struct {
	calls int
	err   error
}

func (w *flakyCheckpointWriter) Upsert(_ context.Context, _ string, _ domain.AgentExecutionCheckpoint) error {
	w.calls++
	if w.calls > 1 {
		return w.err
	}
	return nil
}
