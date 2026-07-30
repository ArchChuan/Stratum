package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/platformmcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type assistantDiagnosticStub struct {
	authorizeCalls int
}

func (s *assistantDiagnosticStub) Authorize(
	_ context.Context,
	req domain.DiagnosticRequest,
) (domain.DiagnosticAuthorization, error) {
	s.authorizeCalls++
	req.Scope = domain.DiagnosticScopeSelf
	return domain.DiagnosticAuthorization{Request: req, RoleClass: "member"}, nil
}

func (*assistantDiagnosticStub) CollectAuthorized(
	context.Context,
	domain.DiagnosticRequest,
) (domain.DiagnosticEvidence, error) {
	return domain.DiagnosticEvidence{}, nil
}

type strictModelValidatorStub struct {
	err   error
	calls []string
}

func (v *strictModelValidatorStub) ValidateTenantChatModel(_ context.Context, tenantID, model string) error {
	v.calls = append(v.calls, tenantID+":"+model)
	return v.err
}

type platformAssistantMCPTools struct{}

func (platformAssistantMCPTools) ToolsForServer(_ context.Context, serverID string) []port.ToolDefinition {
	if serverID != platformmcp.SystemServerID {
		return nil
	}
	tools := make([]port.ToolDefinition, 0, len(platformmcp.Phase1ToolNames))
	for _, name := range platformmcp.Phase1ToolNames {
		tools = append(tools, port.ToolDefinition{
			Name: "mcp:" + serverID + ":" + name, ProviderType: domain.ProviderTypeMCP,
			ServerID: serverID, CapabilityID: name,
		})
	}
	return tools
}

func TestSystemAssistantResolvesPlatformToolsThroughSharedMCP(t *testing.T) {
	diagnostics := &assistantDiagnosticStub{}
	svc := NewAgentService(AgentServiceDeps{
		Registry: NewRegistry(nil, BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		MCPTools: platformAssistantMCPTools{}, DiagnosticProvider: diagnostics,
		TenantModelValidator: &strictModelValidatorStub{},
	})
	toolIDs := platformAssistantToolIDs()
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey,
		LLMModel: "tenant-model", MaxIterations: 3, MCPToolIDs: toolIDs,
	}}

	_, options, err := svc.assembleOptions(t.Context(), system, ExecRequest{UserID: "user-1"},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")

	require.NoError(t, err)
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	require.Len(t, cfg.ExtraTools, len(platformmcp.Phase1ToolNames))
	for _, tool := range cfg.ExtraTools {
		require.Equal(t, domain.ProviderTypeMCP, tool.ProviderType)
		require.Equal(t, platformmcp.SystemServerID, tool.ServerID)
		wantRisk := string(port.ToolRiskRead)
		if tool.CapabilityID == platformmcp.ToolProposeResourceChange {
			wantRisk = string(port.ToolRiskWriteReversible)
		}
		require.Equal(t, wantRisk, tool.Metadata["risk_level"])
		require.Equal(t, true, tool.Metadata["policy_resolved"])
	}
	require.Equal(t, "member", cfg.SystemAssistantRoleClass)
	require.Equal(t, 1, diagnostics.authorizeCalls)
}

func TestSystemAssistantWithoutModelFailsBeforeCapabilityResolution(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{
		Registry: NewRegistry(nil, BuiltinSystemAssistantProfileSource(), zap.NewNop()),
	})
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, MaxIterations: 3,
	}}
	_, _, err := svc.assembleOptions(t.Context(), system, ExecRequest{},
		ExecMeta{TenantID: "tenant-1"}, "execution-1")
	require.ErrorIs(t, err, domain.ErrAssistantModelUnavailable)
}

func TestOrdinaryAgentCannotResolveCopiedPlatformMCPBindings(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{MCPTools: platformAssistantMCPTools{}})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: "ordinary", MaxIterations: 3, MCPToolIDs: platformAssistantToolIDs(),
	}}
	_, options, err := svc.assembleOptions(t.Context(), agent, ExecRequest{},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")
	require.NoError(t, err)
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	require.Empty(t, cfg.ExtraTools)
}

func TestSystemAssistantTreatsStaleModelAsUnavailable(t *testing.T) {
	validator := &strictModelValidatorStub{err: domain.ErrInvalidSystemAssistantModel}
	svc := NewAgentService(AgentServiceDeps{
		Registry:             NewRegistry(nil, BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantModelValidator: validator,
	})
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey,
		LLMModel: "retired-model", MaxIterations: 3,
	}}
	_, _, err := svc.assembleOptions(t.Context(), system, ExecRequest{},
		ExecMeta{TenantID: "tenant-1"}, "execution-1")
	require.ErrorIs(t, err, domain.ErrAssistantModelUnavailable)
	require.False(t, errors.Is(err, domain.ErrInvalidSystemAssistantModel))
}

func TestBuildExecutionArtifactsPreservesPlatformMCPFailureAsEvidenceGap(t *testing.T) {
	artifacts := buildExecutionArtifacts([]domain.SystemAssistantToolArtifact{{
		Tool: platformmcp.ToolSearchOfficialDocs, Outcome: "error", ErrorCode: "not_found",
	}}, domain.CurrentSystemAssistantProfileVersion)
	require.Len(t, artifacts, 1)
	require.NotNil(t, artifacts[0].DiagnosticReport)
	require.Equal(t, "not_found", artifacts[0].DiagnosticReport.EvidenceGaps[0].Code)
}

func platformAssistantToolIDs() []string {
	ids := make([]string, 0, len(platformmcp.Phase1ToolNames))
	for _, name := range platformmcp.Phase1ToolNames {
		ids = append(ids, "mcp:"+platformmcp.SystemServerID+":"+name)
	}
	return ids
}
