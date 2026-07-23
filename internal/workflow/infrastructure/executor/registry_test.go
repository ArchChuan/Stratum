package executor

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/internal/workflow/domain/port"
	"github.com/stretchr/testify/require"
)

type agentFake struct{ agentID, input string }

func (f *agentFake) ExecuteAgent(
	_ context.Context,
	_, agentID, input string,
	_ func(string) error,
) (string, string, []port.NodeToolStep, error) {
	f.agentID, f.input = agentID, input
	return "agent-output", "trace-agent", nil, nil
}

type skillFake struct{ agentID, skillID, revisionID string }

func (f *skillFake) ExecuteSkill(_ context.Context, _, agentID, skillID, revisionID, _ string) (string, string, error) {
	f.agentID, f.skillID, f.revisionID = agentID, skillID, revisionID
	return "skill-output", "trace-skill", nil
}

type mcpFake struct {
	risk   ToolRisk
	called bool
	err    error
}

func (f *mcpFake) ToolRisk(context.Context, string, string, string) (ToolRisk, error) {
	return f.risk, nil
}
func (f *mcpFake) CallTool(context.Context, string, string, string, map[string]any) (any, error) {
	f.called = true
	return map[string]any{"ok": true}, f.err
}

func TestRegistryClassifiesMCPRetryByEffectClass(t *testing.T) {
	mcp := &mcpFake{risk: ToolRiskRead, err: errors.New("provider unavailable")}
	registry := NewRegistry(&agentFake{}, &skillFake{}, mcp)
	result, err := registry.Execute(context.Background(), port.NodeExecutionRequest{TenantID: "tenant", Node: domain.Node{ID: "read", Type: domain.NodeTypeMCPTool, MCPServerID: "crm", MCPToolName: "get", EffectClass: domain.EffectClassIdempotent}, Input: `{}`})
	require.Error(t, err)
	require.True(t, result.Retryable)
	require.Equal(t, "mcp_provider_error", result.ErrorCode)

	result, err = registry.Execute(context.Background(), port.NodeExecutionRequest{TenantID: "tenant", Node: domain.Node{ID: "write", Type: domain.NodeTypeMCPTool, MCPServerID: "crm", MCPToolName: "create", EffectClass: domain.EffectClassNonIdempotent}, Input: `{}`})
	require.Error(t, err)
	require.False(t, result.Retryable)
}

func TestRegistryPinsSkillRevision(t *testing.T) {
	skill := &skillFake{}
	registry := NewRegistry(&agentFake{}, skill, &mcpFake{risk: ToolRiskRead})
	result, err := registry.Execute(context.Background(), port.NodeExecutionRequest{TenantID: "tenant", Node: domain.Node{ID: "skill", Type: domain.NodeTypeSkill, AgentID: "agent-7", SkillID: "skill-2", SkillRevisionID: "revision-9"}, Input: "query"})
	require.NoError(t, err)
	require.Equal(t, "skill-output", result.Output)
	require.Equal(t, "agent-7", skill.agentID)
	require.Equal(t, "skill-2", skill.skillID)
	require.Equal(t, "revision-9", skill.revisionID)
}

func TestRegistryMCPRiskFailsClosedBeforeCall(t *testing.T) {
	mcp := &mcpFake{risk: ToolRiskUnclassified}
	registry := NewRegistry(&agentFake{}, &skillFake{}, mcp)
	result, err := registry.Execute(context.Background(), port.NodeExecutionRequest{TenantID: "tenant", Node: domain.Node{ID: "tool", Type: domain.NodeTypeMCPTool, MCPServerID: "crm", MCPToolName: "delete", EffectClass: domain.EffectClassNonIdempotent}, Input: `{"id":"1"}`})
	require.NoError(t, err)
	require.True(t, result.Paused)
	require.False(t, mcp.called)
}

func TestRegistryHighRiskMCPRequiresServerTrustedApproval(t *testing.T) {
	mcp := &mcpFake{risk: ToolRiskDestructive}
	registry := NewRegistry(&agentFake{}, &skillFake{}, mcp)
	node := domain.Node{ID: "tool", Type: domain.NodeTypeMCPTool, MCPServerID: "crm", MCPToolName: "delete", EffectClass: domain.EffectClassNonIdempotent}
	result, err := registry.Execute(context.Background(), port.NodeExecutionRequest{TenantID: "tenant", Node: node, Input: `{}`, Approved: true})
	require.NoError(t, err)
	require.True(t, result.Paused, "boolean alone must not bypass approval")
	require.False(t, mcp.called)

	result, err = registry.Execute(context.Background(), port.NodeExecutionRequest{TenantID: "tenant", Node: node, Input: `{}`, Approved: true, ApprovalID: "persisted-approval-1"})
	require.NoError(t, err)
	require.False(t, result.Paused)
	require.True(t, mcp.called)
}

func TestRegistryConditionUsesOnlyRunInput(t *testing.T) {
	registry := NewRegistry(&agentFake{}, &skillFake{}, &mcpFake{risk: ToolRiskRead})
	result, err := registry.Execute(context.Background(), port.NodeExecutionRequest{Node: domain.Node{ID: "route", Type: domain.NodeTypeCondition, Condition: "input.approved == true"}, RunInput: map[string]any{"approved": true}})
	require.NoError(t, err)
	require.True(t, result.ConditionValue)

	_, err = registry.Execute(context.Background(), port.NodeExecutionRequest{Node: domain.Node{ID: "route", Type: domain.NodeTypeCondition, Condition: "os.exec('rm')"}})
	require.Error(t, err)
}

func TestRegistryConditionMissingNodeOutputFailsClosed(t *testing.T) {
	registry := NewRegistry(&agentFake{}, &skillFake{}, &mcpFake{risk: ToolRiskRead})
	_, err := registry.Execute(context.Background(), port.NodeExecutionRequest{Node: domain.Node{ID: "route", Type: domain.NodeTypeCondition, Condition: "nodes.upstream.output == 'ok'"}, NodeOutputs: map[string]string{}})
	require.Error(t, err)
}

func TestRegistryRejectsPureClassificationForWriteTool(t *testing.T) {
	mcp := &mcpFake{risk: ToolRiskWriteReversible}
	registry := NewRegistry(&agentFake{}, &skillFake{}, mcp)
	_, err := registry.Execute(context.Background(), port.NodeExecutionRequest{TenantID: "tenant", Node: domain.Node{ID: "write", Type: domain.NodeTypeMCPTool, MCPServerID: "crm", MCPToolName: "update", EffectClass: domain.EffectClassPure}, Input: `{}`})
	require.Error(t, err)
	require.False(t, mcp.called)
}
