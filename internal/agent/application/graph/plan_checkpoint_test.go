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

func TestPlanCheckpointCodecRoundTripsActiveSkills(t *testing.T) {
	want := graph.PlanCheckpointPayload{
		ActiveSkills: []graph.CheckpointSkillRef{
			{SkillID: "skill-a", RevisionID: "rev-1"},
			{SkillID: "skill-b", RevisionID: "rev-2"},
		},
	}
	encoded, err := graph.EncodePlanCheckpoint(want)
	require.NoError(t, err)
	got, err := graph.DecodePlanCheckpoint(encoded)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Nil(t, got.Plan)
}

func TestPlanCheckpointCodecDecodesLegacyActiveSkillFallback(t *testing.T) {
	// 旧版 payload 只有 active_skill_id/active_skill_revision_id，无 active_skills 数组。
	encoded, err := graph.EncodePlanCheckpoint(graph.PlanCheckpointPayload{
		ActiveSkillID:         "skill-a",
		ActiveSkillRevisionID: "rev-1",
	})
	require.NoError(t, err)
	got, err := graph.DecodePlanCheckpoint(encoded)
	require.NoError(t, err)
	require.Nil(t, got.ActiveSkills, "legacy payload must leave ActiveSkills nil")
	require.Equal(t, "skill-a", got.ActiveSkillID)
	require.Equal(t, "rev-1", got.ActiveSkillRevisionID)
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

func TestPersistReActCheckpointEncodesActiveSkills(t *testing.T) {
	writer := &checkpointWriterForPlanTest{}
	state := &graph.ReActState{
		TenantID: "tenant-1", ExecutionID: "exec-1", TraceID: "trace-1", ConversationID: "conv-1",
		Actives: []port.SkillActivation{
			{SkillID: "skill-a", RevisionID: "rev-1"},
			{SkillID: "skill-b", RevisionID: "rev-2"},
		},
		Steps: 1,
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
	require.Equal(t, []graph.CheckpointSkillRef{
		{SkillID: "skill-a", RevisionID: "rev-1"},
		{SkillID: "skill-b", RevisionID: "rev-2"},
	}, decoded.ActiveSkills)
	// 旧字段回填最后一条激活，供旧二进制降级恢复。
	require.Equal(t, "skill-b", decoded.ActiveSkillID)
	require.Equal(t, "rev-2", decoded.ActiveSkillRevisionID)
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

func TestExtractToolMessagesFiltersToolDimension(t *testing.T) {
	toolMsg := port.LLMMessage{Role: "tool", ToolCallID: "tc-1", Content: `{"result":"ok"}`}
	assistantWithCalls := port.LLMMessage{Role: "assistant", Content: "searching", ToolCalls: []port.ToolCall{{ID: "tc-1", Name: "search"}}}
	input := []port.LLMMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
		assistantWithCalls,
		toolMsg,
		{Role: "assistant", Content: "final answer"},
	}
	got := graph.ExtractToolMessages(input)
	// 纯文本 user/assistant/system 被过滤,工具维度(role=tool / 带 tool_calls)保留。
	require.Equal(t, []port.LLMMessage{assistantWithCalls, toolMsg}, got)
}

func TestToolMessagesSnapshotRoundTripV2MergeAppendsToBase(t *testing.T) {
	toolMsg := port.LLMMessage{Role: "tool", ToolCallID: "tc-1", Content: `{"result":"ok"}`}
	assistantWithCalls := port.LLMMessage{Role: "assistant", Content: "searching", ToolCalls: []port.ToolCall{{ID: "tc-1", Name: "search"}}}
	// 快照只含工具维度;纯文本 user 不入快照(chat_messages 已有)。
	raw, err := graph.EncodeToolMessagesSnapshot([]port.LLMMessage{
		{Role: "user", Content: "hello"}, assistantWithCalls, toolMsg,
	})
	require.NoError(t, err)

	// base = chat_messages 历史 + 本轮 user(纯 user/assistant 维度)。
	base := []port.LLMMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
	}
	merged := graph.MergeToolMessagesSnapshot(raw, base)
	require.Equal(t, []port.LLMMessage{base[0], base[1], assistantWithCalls, toolMsg}, merged)
}

func TestMergeToolMessagesSnapshotEmptyToolsReturnsBase(t *testing.T) {
	raw, err := graph.EncodeToolMessagesSnapshot([]port.LLMMessage{{Role: "user", Content: "only text"}})
	require.NoError(t, err)
	base := []port.LLMMessage{{Role: "system", Content: "sys"}}
	// 空工具集快照:merge 返回 base 原样。
	require.Equal(t, base, graph.MergeToolMessagesSnapshot(raw, base))
}

func TestMergeToolMessagesSnapshotV1FullSnapshotReplacesBase(t *testing.T) {
	// 旧二进制写裸 []port.LLMMessage 全量快照(含 user/assistant):整体替换 base,
	// 防止新旧混跑 append 造成重复历史。
	v1 := []port.LLMMessage{
		{Role: "system", Content: "sys"}, {Role: "user", Content: "old"},
		{Role: "assistant", Content: "old answer", ToolCalls: []port.ToolCall{{ID: "tc-9", Name: "search"}}},
	}
	raw, err := json.Marshal(v1)
	require.NoError(t, err)
	base := []port.LLMMessage{{Role: "system", Content: "fresh base"}}
	got := graph.MergeToolMessagesSnapshot(raw, base)
	require.Equal(t, v1, got)
}

func TestMergeToolMessagesSnapshotInvalidJSONReturnsBase(t *testing.T) {
	base := []port.LLMMessage{{Role: "user", Content: "hi"}}
	require.Equal(t, base, graph.MergeToolMessagesSnapshot(json.RawMessage(`{not json`), base))
	// 未知信封版本(对象但 version != 2):无法解为裸数组,降级返回 base。
	require.Equal(t, base, graph.MergeToolMessagesSnapshot(json.RawMessage(`{"version":99,"tool_messages":[]}`), base))
}

func TestPersistReActCheckpointWritesToolDimensionSnapshot(t *testing.T) {
	writer := &checkpointWriterForPlanTest{}
	state := &graph.ReActState{
		TenantID: "tenant-1", ExecutionID: "exec-1", TraceID: "trace-1", ConversationID: "conv-1",
		Messages: []port.LLMMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "searching", ToolCalls: []port.ToolCall{{ID: "tc-1", Name: "search"}}},
			{Role: "tool", ToolCallID: "tc-1", Content: `{"result":"ok"}`},
		},
		Steps: 3,
	}
	err := graph.PersistReActCheckpoint(
		context.Background(), writer, "tenant-1",
		graph.PlanCheckpointIdentity{ExecutionID: "exec-1", TraceID: "trace-1", ConversationID: "conv-1", AgentID: "agent-1", UserID: "user-1"},
		state, "llm",
	)
	require.NoError(t, err)
	cp := writer.lastCheckpoint
	require.NotNil(t, cp)
	// 快照必须是工具维度增量(v2 信封),而非全量 user/assistant:恢复时 merge 回
	// chat_messages base,重复 user/assistant 会造成历史重复。
	var envelope struct {
		Version      int               `json:"version"`
		ToolMessages []port.LLMMessage `json:"tool_messages"`
	}
	require.NoError(t, json.Unmarshal(cp.MessagesSnapshotJSON, &envelope))
	require.Equal(t, 2, envelope.Version)
	require.Equal(t, []port.LLMMessage{
		{Role: "assistant", Content: "searching", ToolCalls: []port.ToolCall{{ID: "tc-1", Name: "search"}}},
		{Role: "tool", ToolCallID: "tc-1", Content: `{"result":"ok"}`},
	}, envelope.ToolMessages)
}
