package graph_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
)

func TestPlanToolDefinitionsReserveAllPlanActions(t *testing.T) {
	tools := graph.PlanToolDefinitions()
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, name := range []string{"stratum_create_plan", "stratum_revise_plan", "stratum_continue_plan", "stratum_cancel_plan"} {
		require.Truef(t, names[name], "missing built-in plan tool %s", name)
	}
}

func TestExecutePlanToolCreatesAndPersistsPlan(t *testing.T) {
	writer := &checkpointWriterForPlanTest{}
	state := graph.ReActState{
		TenantID: "tenant-1", ExecutionID: "exec-1", TraceID: "trace-1", ConversationID: "conv-1", PlanCheckpointWriter: writer,
		PlanCheckpointIdentity: graph.PlanCheckpointIdentity{CheckpointID: "cp-1", ExecutionID: "exec-1", TraceID: "trace-1", ConversationID: "conv-1"},
		PlanIDSource:           sequencePlanIDs([]string{"plan-1", "node-1"}), PlanLimits: domain.PlanLimits{MaxNodes: 5, MaxRevisions: 5},
	}
	content, err := graph.ExecutePlanTool(context.Background(), &state, port.ToolCall{Name: "stratum_create_plan", Arguments: map[string]any{
		"expected_revision": float64(0), "nodes": []any{map[string]any{"key": "one", "goal": "Do one thing"}},
	}})
	require.NoError(t, err)
	require.Contains(t, content, `"revision":1`)
	require.NotNil(t, state.ActivePlan)
	require.Equal(t, 1, writer.calls)
}

func TestExecutePlanToolReturnsCorrectionWithoutMutation(t *testing.T) {
	state := graph.ReActState{ActivePlan: &domain.Plan{ID: "plan-1", Revision: 2, Status: domain.PlanStatusActive}}
	before, _ := json.Marshal(state.ActivePlan)
	content, err := graph.ExecutePlanTool(context.Background(), &state, port.ToolCall{Name: "stratum_continue_plan", Arguments: map[string]any{"expected_revision": float64(1)}})
	require.NoError(t, err)
	require.Contains(t, content, "correction")
	after, _ := json.Marshal(state.ActivePlan)
	require.Equal(t, string(before), string(after))
}

func TestExecutePlanToolRequiresCheckpointWriter(t *testing.T) {
	state := graph.ReActState{PlanIDSource: sequencePlanIDs([]string{"plan-1", "node-1"}), PlanLimits: domain.PlanLimits{MaxNodes: 5}}
	_, err := graph.ExecutePlanTool(context.Background(), &state, port.ToolCall{Name: "stratum_create_plan", Arguments: map[string]any{
		"nodes": []any{map[string]any{"key": "one", "goal": "one"}},
	}})
	require.Error(t, err)
	require.True(t, errors.Is(err, graph.ErrPlanCheckpointRequired))
	require.Nil(t, state.ActivePlan)
}

func sequencePlanIDs(values []string) func() string {
	index := 0
	return func() string {
		if index >= len(values) {
			return "generated"
		}
		value := values[index]
		index++
		return value
	}
}
