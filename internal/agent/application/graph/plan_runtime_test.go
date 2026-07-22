package graph_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/stretchr/testify/require"
)

func TestExecuteReadyPlanNodesBoundsConcurrencyAndReturnsObservation(t *testing.T) {
	state := runtimeStateWithPlan([]domain.PlanNode{
		{ID: "a", Goal: "a", Status: domain.PlanNodeStatusPending},
		{ID: "b", Goal: "b", Status: domain.PlanNodeStatusPending},
		{ID: "c", Goal: "c", DependsOn: []string{"a", "b"}, Status: domain.PlanNodeStatusPending},
	})
	state.PlanLimits.MaxConcurrentNodes = 2
	var current, maximum atomic.Int32

	observation, err := graph.ExecuteReadyPlanNodes(context.Background(), &state, func(ctx context.Context, nodeState graph.ReActState, node domain.PlanNode, _ map[string]string) (graph.PlanNodeExecutionResult, error) {
		require.True(t, nodeState.PlanToolsDisabled)
		n := current.Add(1)
		for {
			old := maximum.Load()
			if n <= old || maximum.CompareAndSwap(old, n) {
				break
			}
		}
		defer current.Add(-1)
		time.Sleep(15 * time.Millisecond)
		return graph.PlanNodeExecutionResult{Summary: node.ID + " done"}, nil
	})

	require.NoError(t, err)
	require.Contains(t, observation, "a")
	require.Contains(t, observation, "b")
	require.Equal(t, int32(2), maximum.Load())
	require.Equal(t, domain.PlanNodeStatusSucceeded, state.ActivePlan.Nodes[0].Status)
	require.Equal(t, domain.PlanNodeStatusSucceeded, state.ActivePlan.Nodes[1].Status)
	require.Equal(t, domain.PlanNodeStatusPending, state.ActivePlan.Nodes[2].Status)
	require.NotEmpty(t, state.ActivePlan.Nodes[0].Attempts[0].ID)
	require.NotEmpty(t, state.ActivePlan.Nodes[1].Attempts[0].ID)
}

func TestExecuteReadyPlanNodesRejectsWaveBeyondRevisionBudget(t *testing.T) {
	state := runtimeStateWithPlan([]domain.PlanNode{{ID: "a", Goal: "a", Status: domain.PlanNodeStatusPending}})
	state.PlanLimits.MaxRevisions = state.ActivePlan.Revision
	called := false

	_, err := graph.ExecuteReadyPlanNodes(context.Background(), &state, func(context.Context, graph.ReActState, domain.PlanNode, map[string]string) (graph.PlanNodeExecutionResult, error) {
		called = true
		return graph.PlanNodeExecutionResult{Summary: "done"}, nil
	})

	require.ErrorIs(t, err, domain.ErrPlanBudgetExceeded)
	require.False(t, called)
}

func TestExecuteReadyPlanNodesRecoversPanicAndWaitsForWorkers(t *testing.T) {
	state := runtimeStateWithPlan([]domain.PlanNode{{ID: "panic", Goal: "panic", Status: domain.PlanNodeStatusPending}, {ID: "slow", Goal: "slow", Status: domain.PlanNodeStatusPending}})
	var finished atomic.Int32
	observation, err := graph.ExecuteReadyPlanNodes(context.Background(), &state, func(ctx context.Context, _ graph.ReActState, node domain.PlanNode, _ map[string]string) (graph.PlanNodeExecutionResult, error) {
		if node.ID == "panic" {
			panic("node panic")
		}
		select {
		case <-time.After(20 * time.Millisecond):
			finished.Add(1)
			return graph.PlanNodeExecutionResult{Summary: "done"}, nil
		case <-ctx.Done():
			return graph.PlanNodeExecutionResult{}, ctx.Err()
		}
	})
	require.NoError(t, err)
	require.Contains(t, observation, "failed")
	require.Equal(t, int32(1), finished.Load())
	require.Equal(t, domain.PlanNodeStatusFailed, state.ActivePlan.Nodes[0].Status)
}

func TestExecuteReadyPlanNodesCancelsAndWaitsOnCheckpointFailure(t *testing.T) {
	state := runtimeStateWithPlan([]domain.PlanNode{{ID: "a", Goal: "a", Status: domain.PlanNodeStatusPending}, {ID: "b", Goal: "b", Status: domain.PlanNodeStatusPending}})
	state.PlanCheckpointWriter = &checkpointWriterForPlanTest{err: errors.New("store down")}
	started := make(chan struct{}, 2)
	var stopped atomic.Int32
	_, err := graph.ExecuteReadyPlanNodes(context.Background(), &state, func(ctx context.Context, _ graph.ReActState, _ domain.PlanNode, _ map[string]string) (graph.PlanNodeExecutionResult, error) {
		started <- struct{}{}
		select {
		case <-time.After(20 * time.Millisecond):
			stopped.Add(1)
			return graph.PlanNodeExecutionResult{Summary: "done"}, nil
		case <-ctx.Done():
			stopped.Add(1)
			return graph.PlanNodeExecutionResult{}, ctx.Err()
		}
	})
	require.ErrorContains(t, err, "plan checkpoint")
	require.Eventually(t, func() bool { return stopped.Load() == 2 }, time.Second, 5*time.Millisecond)
}

func runtimeStateWithPlan(nodes []domain.PlanNode) graph.ReActState {
	return graph.ReActState{
		TenantID: "tenant-1", ExecutionID: "exec-1", TraceID: "trace-1", ConversationID: "conv-1",
		ActivePlan:   &domain.Plan{ID: "plan-1", Revision: 1, Status: domain.PlanStatusActive, Nodes: nodes},
		PlanIDSource: func() string { return "generated" }, PlanLimits: domain.PlanLimits{MaxNodes: 10, MaxRevisions: 10, MaxConcurrentNodes: 2},
		PlanCheckpointWriter: &checkpointWriterForPlanTest{}, PlanCheckpointIdentity: graph.PlanCheckpointIdentity{CheckpointID: "cp-1"},
	}
}
