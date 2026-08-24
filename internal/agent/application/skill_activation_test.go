package application_test

import (
	"context"
	"encoding/json"
	"testing"

	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestCatalogFromActivations(t *testing.T) {
	cases := []struct {
		name        string
		activations []port.SkillActivation
		want        map[string]string // skillID → revisionID
	}{
		{
			name: "all activations cataloged",
			activations: []port.SkillActivation{
				{SkillID: "skill-a", RevisionID: "rev-a"},
				{SkillID: "skill-b", RevisionID: "rev-b"},
			},
			want: map[string]string{"skill-a": "rev-a", "skill-b": "rev-b"},
		},
		{
			name: "duplicate skill id keeps the later activation",
			activations: []port.SkillActivation{
				{SkillID: "skill-a", RevisionID: "rev-1"},
				{SkillID: "skill-a", RevisionID: "rev-2"},
			},
			want: map[string]string{"skill-a": "rev-2"},
		},
		{
			name: "empty skill id skipped",
			activations: []port.SkillActivation{
				{SkillID: "", RevisionID: "rev-empty"},
				{SkillID: "skill-a", RevisionID: "rev-a"},
			},
			want: map[string]string{"skill-a": "rev-a"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			catalog := agent.CatalogFromActivationsForTest(tc.activations)
			require.Len(t, catalog, len(tc.want))
			for skillID, revision := range tc.want {
				activation, ok := catalog[skillID]
				require.True(t, ok, "skill %s missing", skillID)
				require.Equal(t, revision, activation.RevisionID)
			}
		})
	}
}

func TestRestorePlanCheckpointStatePrefersActiveSkillsArray(t *testing.T) {
	catalog := map[string]port.SkillActivation{
		"skill-a": {SkillID: "skill-a", RevisionID: "rev-1"},
		"skill-b": {SkillID: "skill-b", RevisionID: "rev-2"},
		"skill-c": {SkillID: "skill-c", RevisionID: "rev-3"},
	}
	encode := func(p graph.PlanCheckpointPayload) json.RawMessage {
		raw, err := graph.EncodePlanCheckpoint(p)
		require.NoError(t, err)
		return raw
	}
	cases := []struct {
		name    string
		raw     json.RawMessage
		wantIDs []string
	}{
		{
			name: "array preferred over legacy fields",
			raw: encode(graph.PlanCheckpointPayload{
				ActiveSkills: []graph.CheckpointSkillRef{
					{SkillID: "skill-a", RevisionID: "rev-1"},
					{SkillID: "skill-b", RevisionID: "rev-2"},
				},
				// 若错误回退旧字段会得到 skill-c。
				ActiveSkillID: "skill-c", ActiveSkillRevisionID: "rev-3",
			}),
			wantIDs: []string{"skill-a", "skill-b"},
		},
		{
			name: "revision mismatch entry skipped",
			raw: encode(graph.PlanCheckpointPayload{
				ActiveSkills: []graph.CheckpointSkillRef{
					{SkillID: "skill-a", RevisionID: "rev-stale"},
					{SkillID: "skill-b", RevisionID: "rev-2"},
				},
			}),
			wantIDs: []string{"skill-b"},
		},
		{
			name: "entry not in catalog skipped",
			raw: encode(graph.PlanCheckpointPayload{
				ActiveSkills: []graph.CheckpointSkillRef{
					{SkillID: "skill-ghost", RevisionID: "rev-x"},
					{SkillID: "skill-a", RevisionID: "rev-1"},
				},
			}),
			wantIDs: []string{"skill-a"},
		},
		{
			name: "duplicate skill ids deduplicated",
			raw: encode(graph.PlanCheckpointPayload{
				ActiveSkills: []graph.CheckpointSkillRef{
					{SkillID: "skill-a", RevisionID: "rev-1"},
					{SkillID: "skill-a", RevisionID: "rev-1"},
					{SkillID: "skill-b", RevisionID: "rev-2"},
				},
			}),
			wantIDs: []string{"skill-a", "skill-b"},
		},
		{
			name: "no array falls back to legacy fields",
			raw: encode(graph.PlanCheckpointPayload{
				ActiveSkillID: "skill-c", ActiveSkillRevisionID: "rev-3",
			}),
			wantIDs: []string{"skill-c"},
		},
		{
			name:    "empty payload restores nothing",
			raw:     nil,
			wantIDs: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, actives := agent.RestorePlanCheckpointStateForTest(tc.raw, catalog)
			var got []string
			for _, active := range actives {
				got = append(got, active.SkillID)
			}
			require.Equal(t, tc.wantIDs, got)
		})
	}
}

func TestAgentService_ExecuteSkillScenarioActivatesMultipleSkills(t *testing.T) {
	cfg := &domain.AgentConfig{
		ID: "agent-scenario", Name: "scenario-agent", Type: domain.ReActAgent,
		LLMModel: "qwen-turbo", SystemPrompt: "You are helpful.", MaxIterations: 5,
	}
	repo := new(mockAgentRepo)
	repo.On("Get", mock.Anything, "agent-scenario").Return(cfg, true, nil)
	gw := &mockCapGW{responses: []port.CapabilityResponse{{Content: "scenario done"}}}
	svc := agent.NewAgentService(agent.AgentServiceDeps{
		Registry:             agent.NewRegistry(repo, zap.NewNop()),
		TenantResolver:       countingRevisionTenantResolver{gateway: gw},
		TenantModelValidator: lenientModelValidator{},
		Logger:               zap.NewNop(),
	})

	result, _, err := svc.ExecuteSkillScenario(context.Background(), "agent-scenario",
		agent.ExecRequest{Query: "run scenario"},
		agent.ExecMeta{TenantID: "tenant-1"},
		[]port.SkillActivation{
			{SkillID: "skill-a", Name: "skill-a", RevisionID: "rev-a", Instructions: "USE INSTRUCTION A"},
			{SkillID: "skill-b", Name: "skill-b", RevisionID: "rev-b", Instructions: "USE INSTRUCTION B"},
		},
	)
	require.NoError(t, err)
	require.Equal(t, "scenario done", result.Output)
	require.Len(t, gw.requests, 1)
	req := gw.requests[0]
	require.NotNil(t, req.LLM)
	encoded, err := json.Marshal(req.LLM.Messages)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "USE INSTRUCTION A")
	require.Contains(t, string(encoded), "USE INSTRUCTION B")
	// buildBuiltinTools 无条件追加 stratum_continue_reasoning；无 RAG/无 memory 时 available 仅此一项。
	// qwen-turbo fallback 8000 窗口下 ToolsCap 有限：预算裁剪只裁 plan 工作流工具，
	// 统一 stratum_skill 工具与授权能力工具必须全量保留（技能激活 = 用户显式功能开关）。
	names := scenarioToolNames(req.LLM.Tools)
	for _, mustKeep := range []string{"stratum_skill", "stratum_continue_reasoning"} {
		require.Contains(t, names, mustKeep, "激活技能/能力工具被预算裁剪: %v", names)
	}
}

type resumableCheckpointStore struct {
	cp *domain.AgentExecutionCheckpoint
}

func (f *resumableCheckpointStore) GetLatest(context.Context, string, string) (*domain.AgentExecutionCheckpoint, error) {
	return f.cp, nil
}

func (*resumableCheckpointStore) Upsert(context.Context, string, domain.AgentExecutionCheckpoint) error {
	return nil
}

func (*resumableCheckpointStore) MarkCompleted(context.Context, string, string) error {
	return nil
}

func (*resumableCheckpointStore) UpdateStatus(context.Context, string, string, string) error {
	return nil
}

func (*resumableCheckpointStore) DeleteExpired(context.Context, string) (int64, error) {
	return 0, nil
}

func (*resumableCheckpointStore) GetLatestActiveByConversation(context.Context, string, string) (*domain.AgentExecutionCheckpoint, error) {
	return nil, nil
}
func (*resumableCheckpointStore) UpdateStatusFrom(context.Context, string, string, string, string) error {
	return nil
}
func (*resumableCheckpointStore) AdvanceRunGeneration(context.Context, string, string, int) error {
	return nil
}
func (*resumableCheckpointStore) Terminate(context.Context, string, string, string) error {
	return nil
}

func TestBaseAgent_CheckpointRestoredActivesOverrideSeededActives(t *testing.T) {
	a := newReActAgent()
	payload, err := graph.EncodePlanCheckpoint(graph.PlanCheckpointPayload{
		ActiveSkills: []graph.CheckpointSkillRef{{SkillID: "skill-restored", RevisionID: "rev-r"}},
	})
	require.NoError(t, err)
	a.SetCheckpointStore(&resumableCheckpointStore{cp: &domain.AgentExecutionCheckpoint{
		ID: "cp-1", ExecutionID: "exec-1", Status: "running",
		RuntimeStateJSON: payload,
	}})
	gw := &mockCapGW{responses: []port.CapabilityResponse{{Content: "done"}}}
	a.SetCapGateway(gw)

	_, err = a.Execute(context.Background(), "resume task",
		agent.WithTenantID("tenant-1"),
		agent.WithExecutionID("exec-1"),
		agent.WithSkillCatalog(map[string]port.SkillActivation{
			"skill-restored": {SkillID: "skill-restored", Name: "skill-restored", RevisionID: "rev-r", Instructions: "RESTORED INSTRUCTION"},
		}),
		agent.WithActiveSkills([]port.SkillActivation{
			{SkillID: "skill-seed", Name: "skill-seed", RevisionID: "rev-s", Instructions: "SEED INSTRUCTION"},
		}),
	)
	require.NoError(t, err)
	require.Len(t, gw.requests, 1)
	encoded, err := json.Marshal(gw.requests[0].LLM.Messages)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "RESTORED INSTRUCTION")
	require.NotContains(t, string(encoded), "SEED INSTRUCTION")
}

func TestValidateSkillCatalogNames_FailClosedOnCollision(t *testing.T) {
	cases := []struct {
		name    string
		catalog map[string]port.SkillActivation
		wantErr bool
		errHint string
	}{
		{
			name: "unique names accepted",
			catalog: map[string]port.SkillActivation{
				"skill-a": {SkillID: "skill-a", Name: "Alpha", RevisionID: "rev-a"},
				"skill-b": {SkillID: "skill-b", Name: "Beta", RevisionID: "rev-b"},
			},
			wantErr: false,
		},
		{
			name: "empty name falls back to skill id and stays unique",
			catalog: map[string]port.SkillActivation{
				"skill-a": {SkillID: "skill-a", RevisionID: "rev-a"},
				"skill-b": {SkillID: "skill-b", Name: "Beta", RevisionID: "rev-b"},
			},
			wantErr: false,
		},
		{
			name: "duplicate resolved name rejected",
			catalog: map[string]port.SkillActivation{
				"skill-a": {SkillID: "skill-a", Name: "Same", RevisionID: "rev-a"},
				"skill-b": {SkillID: "skill-b", Name: "Same", RevisionID: "rev-b"},
			},
			wantErr: true,
			errHint: "collides",
		},
		{
			name: "reserved unified trigger name rejected",
			catalog: map[string]port.SkillActivation{
				"skill-a": {SkillID: "skill-a", Name: "stratum_skill", RevisionID: "rev-a"},
			},
			wantErr: true,
			errHint: "reserved platform tool",
		},
		{
			name: "reserved builtin tool name rejected",
			catalog: map[string]port.SkillActivation{
				"skill-a": {SkillID: "skill-a", Name: "stratum_recall_memory", RevisionID: "rev-a"},
			},
			wantErr: true,
			errHint: "reserved platform tool",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := agent.ValidateSkillCatalogNamesForTest(tc.catalog)
			if tc.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errHint)
				return
			}
			require.NoError(t, err)
		})
	}
}

func scenarioToolNames(tools []port.ToolDefinition) []string {
	names := make([]string, len(tools))
	for i := range tools {
		names[i] = tools[i].Name
	}
	return names
}

// TestBaseAgent_ResumeMergesToolMessagesFromCheckpoint:断点续接恢复键 = executionID,
// checkpoint 只存工具维度快照(v2),恢复时 append 到 chat_messages 重建的 base 末尾。
// 纯文本 user/assistant 不入快照,恢复上下文不重复历史。
func TestBaseAgent_ResumeMergesToolMessagesFromCheckpoint(t *testing.T) {
	a := newReActAgent()
	assistantCall := port.LLMMessage{Role: "assistant", Content: "searching", ToolCalls: []port.ToolCall{{ID: "tc-1", Name: "search"}}}
	toolMsg := port.LLMMessage{Role: "tool", ToolCallID: "tc-1", Content: `{"result":"ok"}`}
	raw, err := graph.EncodeToolMessagesSnapshot([]port.LLMMessage{assistantCall, toolMsg})
	require.NoError(t, err)
	a.SetCheckpointStore(&resumableCheckpointStore{cp: &domain.AgentExecutionCheckpoint{
		ID: "cp-1", ExecutionID: "exec-1", Status: "running", MessagesSnapshotJSON: raw,
	}})
	gw := &mockCapGW{responses: []port.CapabilityResponse{{Content: "done"}}}
	a.SetCapGateway(gw)

	_, err = a.Execute(context.Background(), "continue the search",
		agent.WithTenantID("tenant-1"),
		agent.WithExecutionID("exec-1"),
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(gw.requests), 1)
	msgs := gw.requests[0].LLM.Messages

	// 工具维度消息 merge 到 base 末尾:assistant(tool_calls) 与 tool 结果都在。
	var sawAssistantCall, sawTool bool
	for _, m := range msgs {
		if len(m.ToolCalls) > 0 && m.ToolCalls[0].ID == "tc-1" {
			sawAssistantCall = true
		}
		if m.Role == "tool" && m.ToolCallID == "tc-1" {
			sawTool = true
		}
	}
	require.True(t, sawAssistantCall, "恢复上下文必须含 tool_calls 调用消息")
	require.True(t, sawTool, "恢复上下文必须含 tool 结果消息")
	// 恢复后从断点续跑:最后一条是 tool 结果。
	require.Equal(t, "tool", msgs[len(msgs)-1].Role)

	// 纯文本 user 只来自 chat_messages base,快照不含 user,不得重复。
	userCount := 0
	for _, m := range msgs {
		if m.Role == "user" {
			userCount++
		}
	}
	require.Equal(t, 1, userCount, "恢复不得重复 user 消息")
}
