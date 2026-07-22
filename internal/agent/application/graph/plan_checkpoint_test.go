package graph_test

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/stretchr/testify/require"
)

func TestPlanCheckpointCodecRoundTripsRevisionAndAttempts(t *testing.T) {
	want := graph.PlanCheckpointPayload{
		Plan: &domain.Plan{
			ID: "plan-1", Revision: 7, Status: domain.PlanStatusActive,
			Nodes: []domain.PlanNode{{ID: "node-1", Status: domain.PlanNodeStatusRunning, Attempts: []domain.PlanAttempt{{ID: "attempt-2", Number: 2}}}},
		},
		RemainingNodeBudget:     4,
		RemainingRevisionBudget: 12,
		ActiveAttemptIDs:        []string{"attempt-2"},
	}

	encoded, err := graph.EncodePlanCheckpoint(want)
	require.NoError(t, err)
	got, err := graph.DecodePlanCheckpoint(encoded)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestPlanCheckpointCodecRejectsUnsupportedVersion(t *testing.T) {
	_, err := graph.DecodePlanCheckpoint([]byte(`{"version":99,"plan":{"id":"plan-1"}}`))
	require.ErrorIs(t, err, graph.ErrUnsupportedPlanCheckpoint)
}

func TestPersistPlanCheckpointPropagatesFailureBeforeSuccess(t *testing.T) {
	writer := &checkpointWriterForPlanTest{err: errors.New("database unavailable")}
	err := graph.PersistPlanCheckpoint(context.Background(), writer, "tenant-1", graph.PlanCheckpointIdentity{
		CheckpointID: "checkpoint-1", ExecutionID: "exec-1", TraceID: "trace-1", ConversationID: "conv-1", AgentID: "agent-1", UserID: "user-1",
	}, graph.PlanCheckpointPayload{Plan: &domain.Plan{ID: "plan-1", Revision: 1}})
	require.ErrorContains(t, err, "plan checkpoint")
	require.Equal(t, 1, writer.calls)
}

type checkpointWriterForPlanTest struct {
	err   error
	calls int
}

func (w *checkpointWriterForPlanTest) Upsert(_ context.Context, _ string, _ domain.AgentExecutionCheckpoint) error {
	w.calls++
	return w.err
}
