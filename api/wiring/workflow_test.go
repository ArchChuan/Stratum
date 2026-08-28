package wiring

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	workflowport "github.com/byteBuilderX/stratum/internal/workflow/domain/port"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/stretchr/testify/require"
)

type workflowAgentServiceFake struct {
	agentID       string
	req           agentapp.ExecRequest
	meta          agentapp.ExecMeta
	tokens        []string
	result        *agentapp.AgentResult
	scenarioCalls int
	allowedSkills []string
}

func (f *workflowAgentServiceFake) Get(_ context.Context, _ string) (agentapp.AgentDTO, error) {
	return agentapp.AgentDTO{AllowedSkills: f.allowedSkills}, nil
}

func (f *workflowAgentServiceFake) Execute(_ context.Context, agentID string, req agentapp.ExecRequest, meta agentapp.ExecMeta) (*agentapp.AgentResult, int, error) {
	f.agentID, f.req, f.meta = agentID, req, meta
	return &agentapp.AgentResult{Output: "done"}, 10, nil
}

func (f *workflowAgentServiceFake) ExecuteSkillScenario(_ context.Context, agentID string, req agentapp.ExecRequest, meta agentapp.ExecMeta, activations []agentport.SkillActivation) (*agentapp.AgentResult, int, error) {
	f.agentID, f.req, f.meta = agentID, req, meta
	f.scenarioCalls++
	revision := ""
	if len(activations) > 0 {
		revision = activations[0].RevisionID
	}
	return &agentapp.AgentResult{Output: revision}, 10, nil
}

func (f *workflowAgentServiceFake) ExecuteStream(
	ctx context.Context,
	agentID string,
	req agentapp.ExecRequest,
	meta agentapp.ExecMeta,
	tokenCb func(string),
) (context.Context, context.CancelFunc, func() (*agentapp.AgentResult, int, error), string, error) {
	f.agentID, f.req, f.meta = agentID, req, meta
	execCtx, cancel := context.WithCancel(ctx)
	return execCtx, cancel, func() (*agentapp.AgentResult, int, error) {
		for _, token := range f.tokens {
			tokenCb(token)
			if execCtx.Err() != nil {
				return nil, 0, execCtx.Err()
			}
		}
		if f.result != nil {
			return f.result, 10, nil
		}
		return &agentapp.AgentResult{Output: "done"}, 10, nil
	}, "exec-fake", nil
}

func TestWorkflowAgentExecutorDelegatesToAgentService(t *testing.T) {
	fake := &workflowAgentServiceFake{}
	executor := workflowAgentExecutor{agents: fake}
	output, traceID, _, err := executor.ExecuteAgent(context.Background(), "tenant-1", "agent-1", "user-1", "exec-1", "analyse", nil)
	require.NoError(t, err)
	require.Equal(t, "done", output)
	require.NotEmpty(t, traceID)
	require.Equal(t, "agent-1", fake.agentID)
	require.Equal(t, "analyse", fake.req.Query)
	// 身份全量透传：审批/会话/记忆/轨迹归执行人；自动会话带 source 标记。
	require.Equal(t, "user-1", fake.req.UserID)
	require.Equal(t, "workflow", fake.req.ConversationSource)
	require.Equal(t, "exec-1", fake.meta.ExecutionID)
	require.Equal(t, "tenant-1", fake.meta.TenantID)
}

func TestWorkflowAgentExecutorStreamsOutputDeltaAndReturnsSafeToolSteps(t *testing.T) {
	fake := &workflowAgentServiceFake{
		tokens: []string{"答", "案"},
		result: &agentapp.AgentResult{Output: "答案", ToolCalls: []agentapp.ToolCall{{
			ToolName: "search", Input: map[string]interface{}{"api_key": "secret-value"},
			Output: map[string]any{"raw": "secret-result"}, Duration: 25 * time.Millisecond,
		}}},
	}
	executor := workflowAgentExecutor{agents: fake}
	var streamed string
	output, _, steps, err := executor.ExecuteAgent(
		context.Background(), "tenant-1", "agent-1", "user-1", "exec-1", "analyse",
		func(delta string) error { streamed += delta; return nil },
	)
	require.NoError(t, err)
	require.Equal(t, "答案", output)
	require.Equal(t, "答案", streamed)
	require.Equal(t, []workflowport.NodeToolStep{{
		ToolName: "search", DurationMS: 25, Summary: "工具执行成功",
	}}, steps)
	encoded, err := json.Marshal(steps)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "secret-value")
	require.NotContains(t, string(encoded), "secret-result")
}

type workflowSkillVersionsFake struct{}

func (workflowSkillVersionsFake) ResolveActivation(_ context.Context, skillID, revisionID string) (skillapp.SkillActivationView, bool, error) {
	return skillapp.SkillActivationView{SkillID: skillID, RevisionID: revisionID, Instructions: "pinned"}, true, nil
}

func TestWorkflowSkillExecutorUsesPinnedRevision(t *testing.T) {
	agents := &workflowAgentServiceFake{}
	executor := workflowSkillExecutor{agents: agents, versions: workflowSkillVersionsFake{}}
	output, _, err := executor.ExecuteSkill(context.Background(), "tenant-1", "agent-1", "user-1", "skill-1", "revision-9", "query")
	require.NoError(t, err)
	require.Equal(t, "revision-9", output)
	require.Equal(t, "query", agents.req.Query)
	require.Equal(t, "user-1", agents.req.UserID)
	require.Equal(t, "workflow", agents.req.ConversationSource)
}

// TestWorkflowSkillExecutorExecutesBuiltinSkill:等化后 builtin skill 可被
// workflow 执行,入口不再拒绝,ExecuteSkillScenario 正常被调用。
func TestWorkflowSkillExecutorExecutesBuiltinSkill(t *testing.T) {
	agents := &workflowAgentServiceFake{}
	executor := workflowSkillExecutor{agents: agents, versions: workflowSkillVersionsFake{}}
	output, _, err := executor.ExecuteSkill(context.Background(), "tenant-1", "agent-1", "user-1", "builtin:platform-guide", "revision-9", "query")
	require.NoError(t, err)
	require.Equal(t, "revision-9", output)
	require.Equal(t, "query", agents.req.Query)
	require.Equal(t, 1, agents.scenarioCalls, "builtin skill must reach ExecuteSkillScenario")
}

func TestWorkflowSkillBindingResolverReturnsAgentAllowedSkills(t *testing.T) {
	agents := &workflowAgentServiceFake{allowedSkills: []string{"skill-1", "skill-2"}}
	resolver := workflowSkillBindingResolver{agents: agents}
	allowed, err := resolver.AgentAllowedSkills(context.Background(), "tenant-1", "agent-1")
	require.NoError(t, err)
	require.Equal(t, []string{"skill-1", "skill-2"}, allowed)
}

type workflowMCPPolicyFake struct{ risk mcpdomain.ToolRiskLevel }

func (f workflowMCPPolicyFake) GetToolRisk(context.Context, string, string) (mcpdomain.ToolRiskLevel, error) {
	return f.risk, nil
}

type workflowMCPManagerFake struct {
	called   bool
	tenantID string
}

func (f *workflowMCPManagerFake) CallTool(ctx context.Context, _ string, _ string, _ interface{}) (interface{}, error) {
	f.called = true
	if tc, ok := tenantdb.FromContext(ctx); ok {
		f.tenantID = tc.TenantID
	}
	return map[string]any{"ok": true}, nil
}

func TestWorkflowMCPExecutorMapsTenantPolicyAndCall(t *testing.T) {
	manager := &workflowMCPManagerFake{}
	executor := workflowMCPExecutor{policies: workflowMCPPolicyFake{risk: mcpdomain.ToolRiskRead}, manager: manager}
	risk, err := executor.ToolRisk(context.Background(), "tenant-1", "server", "tool")
	require.NoError(t, err)
	require.Equal(t, "read", string(risk))
	_, err = executor.CallTool(context.Background(), "tenant-1", "server", "tool", map[string]any{"id": "1"})
	require.NoError(t, err)
	require.True(t, manager.called)
	require.Equal(t, "tenant-1", manager.tenantID)
	_, err = executor.CallTool(context.Background(), "", "server", "tool", map[string]any{})
	require.Error(t, err)
}
