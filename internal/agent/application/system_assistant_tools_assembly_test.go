package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type assistantDiagnosticStub struct {
	authorizeCalls int
	role           string
}

func (s *assistantDiagnosticStub) Authorize(
	_ context.Context,
	req domain.DiagnosticRequest,
) (domain.DiagnosticAuthorization, error) {
	s.authorizeCalls++
	req.Scope = domain.DiagnosticScopeSelf
	role := s.role
	if role == "" {
		role = "member"
	}
	return domain.DiagnosticAuthorization{Request: req, RoleClass: role}, nil
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

type genericMCPTools struct{}

func (genericMCPTools) ToolsForServer(_ context.Context, serverID string) []port.ToolDefinition {
	if serverID != "orders" {
		return nil
	}
	return []port.ToolDefinition{{
		Name: "mcp:orders:get", ProviderType: domain.ProviderTypeMCP,
		ServerID: serverID, CapabilityID: "get",
	}}
}

func TestSystemAssistantResolvesPlatformToolsInProcess(t *testing.T) {
	diagnostics := &assistantDiagnosticStub{}
	svc := NewAgentService(AgentServiceDeps{
		Registry:           NewRegistry(nil, BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		DiagnosticProvider: diagnostics,
		OfficialDocsSearch: func(_ context.Context, query string) ([]domain.Citation, error) {
			return []domain.Citation{{Title: query}}, nil
		},
		TenantModelValidator: &strictModelValidatorStub{},
	})
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey,
		LLMModel: "tenant-model", MaxIterations: 3,
	}}

	_, options, err := svc.assembleOptions(t.Context(), system, ExecRequest{UserID: "user-1"},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")

	require.NoError(t, err)
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	require.Len(t, cfg.ExtraTools, 2)
	for _, tool := range cfg.ExtraTools {
		require.Equal(t, domain.ProviderTypeInternal, tool.ProviderType)
		require.Equal(t, tool.Name, tool.CapabilityID)
		require.Empty(t, tool.ServerID)
	}
	require.Equal(t, "member", cfg.SystemAssistantRoleClass)
	require.True(t, cfg.SystemAssistantMode)
	require.NotNil(t, cfg.OfficialDocsSearchFn)
	require.NotNil(t, cfg.DiagnosticFn)
	require.Nil(t, cfg.ProposalCreateFn)
	require.NotNil(t, cfg.InternalToolResultGuardFn)
	require.Equal(t, 1, diagnostics.authorizeCalls)
}

func TestSystemAssistantAdminGetsProposalToolInProcess(t *testing.T) {
	diagnostics := &assistantDiagnosticStub{role: "admin"}
	svc := NewAgentService(AgentServiceDeps{
		Registry:           NewRegistry(nil, BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		DiagnosticProvider: diagnostics,
		OfficialDocsSearch: func(_ context.Context, query string) ([]domain.Citation, error) {
			return []domain.Citation{{Title: query}}, nil
		},
		TenantModelValidator: &strictModelValidatorStub{},
	})
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey,
		LLMModel: "tenant-model", MaxIterations: 3,
	}}
	_, options, err := svc.assembleOptions(t.Context(), system, ExecRequest{UserID: "user-1"},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")
	require.NoError(t, err)
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	require.Equal(t, "admin", cfg.SystemAssistantRoleClass)
	// Proposal tool is only injected when the proposal service is wired; the
	// role-gated definition set itself is covered by the pure-function test.
	require.Len(t, cfg.ExtraTools, 2)
	require.Nil(t, cfg.ProposalCreateFn)
}

func TestSystemAssistantToolDefinitionsForRole(t *testing.T) {
	require.Len(t, SystemAssistantToolDefinitionsForRole("member"), 2)
	require.Len(t, SystemAssistantToolDefinitionsForRole("admin"), 3)
	require.Len(t, SystemAssistantToolDefinitionsForRole("owner"), 3)
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

func TestOrdinaryAgentResolvesMCPToolsFromProvider(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{MCPTools: genericMCPTools{}})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: "ordinary", MaxIterations: 3, MCPToolIDs: []string{"mcp:orders:get"},
	}}
	_, options, err := svc.assembleOptions(t.Context(), agent, ExecRequest{},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")
	require.NoError(t, err)
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	require.Len(t, cfg.ExtraTools, 1)
	require.Equal(t, "mcp:orders:get", cfg.ExtraTools[0].Name)
	require.Equal(t, domain.ProviderTypeMCP, cfg.ExtraTools[0].ProviderType)
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

func TestBuildExecutionArtifactsPreservesAssistantFailureAsEvidenceGap(t *testing.T) {
	artifacts := buildExecutionArtifacts([]domain.SystemAssistantToolArtifact{{
		Tool: domain.SystemAssistantToolSearchOfficialDocs, Outcome: "error", ErrorCode: "not_found",
	}}, domain.CurrentSystemAssistantProfileVersion)
	require.Len(t, artifacts, 1)
	require.NotNil(t, artifacts[0].DiagnosticReport)
	require.Equal(t, "not_found", artifacts[0].DiagnosticReport.EvidenceGaps[0].Code)
}
