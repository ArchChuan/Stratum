package wiring

import (
	"context"
	"testing"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/stretchr/testify/require"
)

type workflowAgentServiceFake struct {
	agentID string
	req     agentapp.ExecRequest
	meta    agentapp.ExecMeta
}

func (f *workflowAgentServiceFake) Execute(_ context.Context, agentID string, req agentapp.ExecRequest, meta agentapp.ExecMeta) (*agentapp.AgentResult, int, error) {
	f.agentID, f.req, f.meta = agentID, req, meta
	return &agentapp.AgentResult{Output: "done"}, 10, nil
}

func (f *workflowAgentServiceFake) ExecuteSkillScenario(_ context.Context, agentID string, req agentapp.ExecRequest, meta agentapp.ExecMeta, activation agentport.SkillActivation) (*agentapp.AgentResult, int, error) {
	f.agentID, f.req, f.meta = agentID, req, meta
	return &agentapp.AgentResult{Output: activation.RevisionID}, 10, nil
}

func TestWorkflowAgentExecutorDelegatesToAgentService(t *testing.T) {
	fake := &workflowAgentServiceFake{}
	executor := workflowAgentExecutor{agents: fake}
	output, traceID, err := executor.ExecuteAgent(context.Background(), "tenant-1", "agent-1", "analyse")
	require.NoError(t, err)
	require.Equal(t, "done", output)
	require.NotEmpty(t, traceID)
	require.Equal(t, "agent-1", fake.agentID)
	require.Equal(t, "analyse", fake.req.Query)
	require.Equal(t, "tenant-1", fake.meta.TenantID)
}

type workflowSkillVersionsFake struct{}

func (workflowSkillVersionsFake) ResolveActivation(_ context.Context, skillID, revisionID string) (skillapp.SkillActivationView, bool, error) {
	return skillapp.SkillActivationView{SkillID: skillID, RevisionID: revisionID, Instructions: "pinned"}, true, nil
}

func TestWorkflowSkillExecutorUsesPinnedRevision(t *testing.T) {
	agents := &workflowAgentServiceFake{}
	executor := workflowSkillExecutor{agents: agents, versions: workflowSkillVersionsFake{}}
	output, _, err := executor.ExecuteSkill(context.Background(), "tenant-1", "agent-1", "skill-1", "revision-9", "query")
	require.NoError(t, err)
	require.Equal(t, "revision-9", output)
	require.Equal(t, "query", agents.req.Query)
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
