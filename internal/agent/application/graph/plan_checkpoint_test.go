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

func TestPlanCheckpointCodecRoundTripsActiveSkill(t *testing.T) {
	want := graph.PlanCheckpointPayload{
		ActiveSkillID:         "skill-a",
		ActiveSkillRevisionID: "rev-1",
	}
	encoded, err := graph.EncodePlanCheckpoint(want)
	require.NoError(t, err)
	got, err := graph.DecodePlanCheckpoint(encoded)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Nil(t, got.Plan, "Plan should be nil when only ActiveSkill is persisted")
}

func TestPlanCheckpointCodecRejectsUnsupportedVersion(t *testing.T) {
	_, err := graph.DecodePlanCheckpoint([]byte(`{"version":99,"plan":{"id":"plan-1"}}`))
	require.ErrorIs(t, err, graph.ErrUnsupportedPlanCheckpoint)
}

func TestPersistPlanCheckpointPropagatesFailureBeforeSuccess(t *testing.T) {
	writer := &checkpointWriterForPlanTest{err: errors.New("database unavailable")}
	err := graph.PersistPlanCheckpoint(context.Background(), writer, "tenant-1", graph.PlanCheckpointIdentity{
		CheckpointID: "checkpoint-1", ExecutionID: "exec-1", TraceID: "trace-1", ConversationID: "conv-1", AgentID: "agent-1", UserID: "user-1",
	}, graph.PlanCheckpointPayload{Plan: &domain.Plan{ID: "plan-1", Revision: 1}}, nil)
	require.ErrorContains(t, err, "plan checkpoint")
	require.Equal(t, 1, writer.calls)
}

type checkpointWriterForPlanTest struct {
	err            error
	calls          int
	lastCheckpoint *domain.AgentExecutionCheckpoint
}

func (w *checkpointWriterForPlanTest) Upsert(_ context.Context, _ string, cp domain.AgentExecutionCheckpoint) error {
	w.calls++
	w.lastCheckpoint = &cp
	return w.err
}

func TestPersistReActCheckpointSnapshotsMessagesAndToolCalls(t *testing.T) {
	writer := &checkpointWriterForPlanTest{}
	state := &graph.ReActState{
		TenantID: "tenant-1", ExecutionID: "exec-1", TraceID: "trace-1", ConversationID: "conv-1",
		Messages: []port.LLMMessage{{Role: "user", Content: "hello"}},
		Steps:    3,
	}
	err := graph.PersistReActCheckpoint(
		context.Background(), writer, "tenant-1",
		graph.PlanCheckpointIdentity{
			CheckpointID: "cp-react", ExecutionID: "exec-1", TraceID: "trace-1",
			ConversationID: "conv-1", AgentID: "agent-1", UserID: "user-1",
		},
		state, "llm",
	)
	require.NoError(t, err)
	require.Equal(t, 1, writer.calls)
}

func TestPersistReActCheckpointNilWriterNoops(t *testing.T) {
	state := &graph.ReActState{TenantID: "t1"}
	err := graph.PersistReActCheckpoint(
		context.Background(), nil, "tenant-1",
		graph.PlanCheckpointIdentity{}, state, "llm",
	)
	require.NoError(t, err)
}

func TestPersistReActCheckpointAutoGeneratesIDWhenEmpty(t *testing.T) {
	writer := &checkpointWriterForPlanTest{}
	state := &graph.ReActState{
		TenantID: "tenant-1", ExecutionID: "exec-1", TraceID: "trace-1", ConversationID: "conv-1",
		Steps: 7,
	}
	err := graph.PersistReActCheckpoint(
		context.Background(), writer, "tenant-1",
		graph.PlanCheckpointIdentity{
			ExecutionID: "exec-1", TraceID: "trace-1",
			ConversationID: "conv-1", AgentID: "agent-1", UserID: "user-1",
		},
		state, "tool",
	)
	require.NoError(t, err)
	require.Equal(t, 1, writer.calls)
}

func TestPersistReActCheckpointEncodesActiveSkill(t *testing.T) {
	writer := &checkpointWriterForPlanTest{}
	state := &graph.ReActState{
		TenantID: "tenant-1", ExecutionID: "exec-1", TraceID: "trace-1", ConversationID: "conv-1",
		ActiveSkill: &port.SkillActivation{SkillID: "skill-a", RevisionID: "rev-1"},
		Steps:       1,
	}
	err := graph.PersistReActCheckpoint(
		context.Background(), writer, "tenant-1",
		graph.PlanCheckpointIdentity{
			CheckpointID: "cp-active-skill", ExecutionID: "exec-1", TraceID: "trace-1",
			ConversationID: "conv-1", AgentID: "agent-1", UserID: "user-1",
		},
		state, "tool",
	)
	require.NoError(t, err)
	require.Equal(t, 1, writer.calls)
	cp := writer.lastCheckpoint
	require.NotNil(t, cp)
	require.NotEqual(t, json.RawMessage("{}"), cp.RuntimeStateJSON)

	decoded, decErr := graph.DecodePlanCheckpoint(cp.RuntimeStateJSON)
	require.NoError(t, decErr)
	require.Equal(t, "skill-a", decoded.ActiveSkillID)
	require.Equal(t, "rev-1", decoded.ActiveSkillRevisionID)
	require.Nil(t, decoded.Plan)
}

func TestPersistReActCheckpointEmptyActiveSkillWritesEmptyJSON(t *testing.T) {
	writer := &checkpointWriterForPlanTest{}
	state := &graph.ReActState{
		TenantID: "tenant-1", ExecutionID: "exec-1", TraceID: "trace-1", ConversationID: "conv-1",
		Steps: 1,
	}
	err := graph.PersistReActCheckpoint(
		context.Background(), writer, "tenant-1",
		graph.PlanCheckpointIdentity{
			CheckpointID: "cp-empty", ExecutionID: "exec-1", TraceID: "trace-1",
			ConversationID: "conv-1", AgentID: "agent-1", UserID: "user-1",
		},
		state, "tool",
	)
	require.NoError(t, err)
	require.Equal(t, 1, writer.calls)
	cp := writer.lastCheckpoint
	require.NotNil(t, cp)
	require.Equal(t, json.RawMessage("{}"), cp.RuntimeStateJSON)
}
