package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/stretchr/testify/mock"
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
	require.Len(t, cfg.ExtraTools, 6)
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

func TestSystemAssistantProposalToolVisibleInProcessWithoutService(t *testing.T) {
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
	// D6：工具可见性全角色一致（6 个），无需按角色注入；ProposalCreateFn
	// 仅在装配 ProposalService 时注入（见 TestAdminProposeAutoConfirmsAndApplies）。
	require.Len(t, cfg.ExtraTools, 6)
	require.Nil(t, cfg.ProposalCreateFn)
}

func TestSystemAssistantToolDefinitionsIncludeModelTools(t *testing.T) {
	names := map[string]bool{}
	for _, d := range SystemAssistantToolDefinitions() {
		names[d.Name] = true
	}
	if !names[domain.SystemAssistantToolListModels] {
		t.Fatalf("expected %s in definitions, got %v", domain.SystemAssistantToolListModels, names)
	}
	if !names[domain.SystemAssistantToolUpdateSystemModel] {
		t.Fatalf("expected %s in definitions, got %v", domain.SystemAssistantToolUpdateSystemModel, names)
	}
	require.Equal(t, domain.ProviderTypeInternal, defByName(SystemAssistantToolDefinitions(), domain.SystemAssistantToolListModels).ProviderType)
	require.Equal(t, domain.ProviderTypeInternal, defByName(SystemAssistantToolDefinitions(), domain.SystemAssistantToolUpdateSystemModel).ProviderType)
}

func defByName(defs []port.ToolDefinition, name string) port.ToolDefinition {
	for _, d := range defs {
		if d.Name == name {
			return d
		}
	}
	return port.ToolDefinition{}
}

type assistantModelDetailsStub struct {
	details []domain.TenantModelDetail
	err     error
}

func (s *assistantModelDetailsStub) ListTenantModelDetails(
	_ context.Context, _ string,
) ([]domain.TenantModelDetail, error) {
	return s.details, s.err
}

func TestSystemAssistantModelToolsAssemblyAndRoleGate(t *testing.T) {
	details := &assistantModelDetailsStub{details: []domain.TenantModelDetail{
		{Model: "qwen-plus", Provider: "provider-1", Capabilities: []string{"chat"}, Enabled: true},
		{Model: "embed-model", Capabilities: []string{"embedding"}, Enabled: false, ProviderManaged: true},
	}}
	repo := new(mockAgentRepo)
	validator := &stubTenantModelValidator{}
	svc := NewAgentService(AgentServiceDeps{
		Registry:             NewRegistry(repo, BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		DiagnosticProvider:   &assistantDiagnosticStub{role: "member"},
		OfficialDocsSearch:   func(_ context.Context, query string) ([]domain.Citation, error) { return nil, nil },
		TenantModelValidator: validator,
		TenantModelCatalog:   validator,
		ModelDetailsProvider: details,
		Logger:               zap.NewNop(),
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

	// list_models 全角色可见：member 直接返回完整清单。
	require.NotNil(t, cfg.ListModelsFn)
	listResult, listErr := cfg.ListModelsFn(context.Background())
	require.NoError(t, listErr)
	require.Equal(t, details.details, listResult["models"])

	// update_system_model 写路径 member 明确拒绝（fail closed），不触达 Registry。
	require.NotNil(t, cfg.UpdateSystemModelFn)
	_, updateErr := cfg.UpdateSystemModelFn(context.Background(), "qwen-plus")
	require.ErrorContains(t, updateErr, "管理员权限")
	repo.AssertNotCalled(t, "UpdateSystemAssistantModel", mock.Anything, mock.Anything)
}

func TestSystemAssistantAdminUpdateSystemModelExecutes(t *testing.T) {
	repo := new(mockAgentRepo)
	validator := &stubTenantModelValidator{}
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	svc := NewAgentService(AgentServiceDeps{
		Registry:             NewRegistry(repo, BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		DiagnosticProvider:   &assistantDiagnosticStub{role: "admin"},
		OfficialDocsSearch:   func(_ context.Context, query string) ([]domain.Citation, error) { return nil, nil },
		TenantModelValidator: validator,
		TenantModelCatalog:   validator,
		Logger:               zap.NewNop(),
	})
	repo.On("GetSystemAssistant", ctx).Return(&domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, LLMModel: "existing-model",
	}, true, nil)
	repo.On("UpdateSystemAssistantModel", ctx, "qwen-plus", "", false, 0, 0,
		mock.MatchedBy(func(a *auditdomain.ResourceChangeAuditEvent) bool {
			return a != nil && a.ActorID == "user-1" && a.Operation == auditdomain.ChangeOpUpdate &&
				a.ResourceKind == auditdomain.ResourceKindAgent
		})).Return(&domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, LLMModel: "qwen-plus",
	}, nil).Once()
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey,
		LLMModel: "tenant-model", MaxIterations: 3,
	}}
	_, options, err := svc.assembleOptions(t.Context(), system, ExecRequest{UserID: "user-1"},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")
	require.NoError(t, err)
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)

	result, updateErr := cfg.UpdateSystemModelFn(ctx, "qwen-plus")
	require.NoError(t, updateErr)
	require.Equal(t, "qwen-plus", result["model"])
	require.Equal(t, true, result["ready"])
	require.Equal(t, []string{"qwen-plus", "qwen-plus-latest", "qwen-max"}, result["availableModels"])
	repo.AssertExpectations(t)
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

// mockAgentRepo 是内部测试包的 port.AgentRepo stub（外部测试包 agent_service_test.go
// 中已有同名类型，但内部测试包无法引用外部测试包符号，故此处独立定义）。
type mockAgentRepo struct{ mock.Mock }

func (m *mockAgentRepo) Register(_ context.Context, cfg *domain.AgentConfig, _ *auditdomain.ResourceChangeAuditEvent, _ []string) error {
	return m.Called(cfg).Error(0)
}

func (m *mockAgentRepo) Get(ctx context.Context, id string) (*domain.AgentConfig, bool, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(*domain.AgentConfig), args.Bool(1), args.Error(2)
}

func (m *mockAgentRepo) GetSystemAssistant(ctx context.Context) (*domain.AgentConfig, bool, error) {
	args := m.Called(ctx)
	return args.Get(0).(*domain.AgentConfig), args.Bool(1), args.Error(2)
}

func (m *mockAgentRepo) GetAll(_ context.Context) ([]*domain.AgentConfig, error) {
	args := m.Called()
	return args.Get(0).([]*domain.AgentConfig), args.Error(1)
}

func (m *mockAgentRepo) Remove(_ context.Context, id string, _ *auditdomain.ResourceChangeAuditEvent) error {
	return m.Called(id).Error(0)
}

func (m *mockAgentRepo) Update(_ context.Context, cfg *domain.AgentConfig, _ *auditdomain.ResourceChangeAuditEvent, _ string, _ bool) error {
	return m.Called(cfg).Error(0)
}

func (m *mockAgentRepo) UpdateSystemAssistantModel(
	ctx context.Context, model, memoryScope string, checkpointEnabled bool, maxIterations, maxContextTokens int,
	audit *auditdomain.ResourceChangeAuditEvent,
) (*domain.AgentConfig, error) {
	args := m.Called(ctx, model, memoryScope, checkpointEnabled, maxIterations, maxContextTokens, audit)
	cfg, _ := args.Get(0).(*domain.AgentConfig)
	return cfg, args.Error(1)
}

func (m *mockAgentRepo) UpdateSystemAssistantAll(
	ctx context.Context, model, memoryScope string, checkpointEnabled bool, maxIterations, maxContextTokens, maxTokens int,
	_ *auditdomain.ResourceChangeAuditEvent,
) (*domain.AgentConfig, error) {
	args := m.Called(ctx, model, memoryScope, checkpointEnabled, maxIterations, maxContextTokens, maxTokens)
	cfg, _ := args.Get(0).(*domain.AgentConfig)
	return cfg, args.Error(1)
}

// stubTenantModelValidator 同时实现 port.TenantChatModelValidator 与
// port.TenantChatModelCatalog，目录与外部测试包版本一致。
type stubTenantModelValidator struct{}

func (s *stubTenantModelValidator) ValidateTenantChatModel(context.Context, string, string) error {
	return nil
}

func (s *stubTenantModelValidator) ListTenantChatModels(context.Context, string) ([]string, error) {
	return []string{"qwen-plus", "qwen-plus-latest", "qwen-max"}, nil
}

func TestProposeToolVisibleToAllRoles(t *testing.T) {
	defs := SystemAssistantToolDefinitions()
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
	}
	require.True(t, names[domain.SystemAssistantToolProposeResourceChange], "propose must be visible to all roles")
	require.True(t, names[domain.SystemAssistantToolApplyResourceChange], "apply must be visible to all roles")
}

func TestAdminProposeAutoConfirmsAndApplies(t *testing.T) {
	repo := newProposalRepoFake()
	applier := &proposalApplierFake{result: domain.ApplyResult{ResourceID: "created"}}
	proposalService := newProposalServiceForTest(repo, &proposalAuthorizerFake{}, &baselineFake{},
		map[domain.ResourceKind]port.ResourceChangeApplier{domain.ResourceAgent: applier})
	svc := NewAgentService(AgentServiceDeps{
		Registry:             NewRegistry(new(mockAgentRepo), BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		DiagnosticProvider:   &assistantDiagnosticStub{role: "admin"},
		ProposalService:      proposalService,
		TenantModelValidator: &stubTenantModelValidator{},
		Logger:               zap.NewNop(),
	})
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey,
		LLMModel: "tenant-model", MaxIterations: 3,
	}}
	_, options, err := svc.assembleOptions(t.Context(), system, ExecRequest{UserID: "admin-1"},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")
	require.NoError(t, err)
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	require.NotNil(t, cfg.ProposalCreateFn)

	artifact, err := cfg.ProposalCreateFn(context.Background(), map[string]any{
		"resourceKind": "agent", "operation": "create",
		"payload": map[string]any{"name": "a", "description": "d", "model": "qwen-plus", "maxIterations": 5, "maxContextTokens": 4096},
	})
	require.NoError(t, err)
	require.Equal(t, domain.StatusApplied, artifact.Status)
	require.Equal(t, 1, applier.calls)
}

func TestMemberProposeStaysInReviewFlow(t *testing.T) {
	repo := newProposalRepoFake()
	applier := &proposalApplierFake{result: domain.ApplyResult{ResourceID: "created"}}
	proposalService := newProposalServiceForTest(repo, &proposalAuthorizerFake{}, &baselineFake{},
		map[domain.ResourceKind]port.ResourceChangeApplier{domain.ResourceAgent: applier})
	svc := NewAgentService(AgentServiceDeps{
		Registry:             NewRegistry(new(mockAgentRepo), BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		DiagnosticProvider:   &assistantDiagnosticStub{role: "member"},
		ProposalService:      proposalService,
		TenantModelValidator: &stubTenantModelValidator{},
		Logger:               zap.NewNop(),
	})
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey,
		LLMModel: "tenant-model", MaxIterations: 3,
	}}
	_, options, err := svc.assembleOptions(t.Context(), system, ExecRequest{UserID: "member-1"},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")
	require.NoError(t, err)
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)

	artifact, err := cfg.ProposalCreateFn(context.Background(), map[string]any{
		"resourceKind": "agent", "operation": "create",
		"payload": map[string]any{"name": "a", "description": "d", "model": "qwen-plus", "maxIterations": 5, "maxContextTokens": 4096},
	})
	require.NoError(t, err)
	require.Equal(t, domain.StatusReadyForReview, artifact.Status)
	require.Equal(t, 0, applier.calls)
}

func TestAdminProposeAutoApplyFailureKeepsCreatedProposal(t *testing.T) {
	repo := newProposalRepoFake()
	applier := &proposalApplierFake{result: domain.ApplyResult{ResourceID: "created"}}
	// failAt=2：CreateProposal 的 authorize 放行，ConfirmAndApply 的预检拒绝，
	// 已创建提案必须保留（artifact.ID 非空、状态 ready_for_review）。
	proposalService := newProposalServiceForTest(repo, &proposalAuthorizerFake{failAt: 2}, &baselineFake{},
		map[domain.ResourceKind]port.ResourceChangeApplier{domain.ResourceAgent: applier})
	svc := NewAgentService(AgentServiceDeps{
		Registry:             NewRegistry(new(mockAgentRepo), BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		DiagnosticProvider:   &assistantDiagnosticStub{role: "admin"},
		ProposalService:      proposalService,
		TenantModelValidator: &stubTenantModelValidator{},
		Logger:               zap.NewNop(),
	})
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey,
		LLMModel: "tenant-model", MaxIterations: 3,
	}}
	_, options, err := svc.assembleOptions(t.Context(), system, ExecRequest{UserID: "admin-1"},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")
	require.NoError(t, err)
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)

	artifact, err := cfg.ProposalCreateFn(context.Background(), map[string]any{
		"resourceKind": "agent", "operation": "create",
		"payload": map[string]any{"name": "a", "description": "d", "model": "qwen-plus", "maxIterations": 5, "maxContextTokens": 4096},
	})
	require.ErrorIs(t, err, domain.ErrProposalForbidden)
	require.NotEmpty(t, artifact.ID)
	require.Equal(t, domain.StatusReadyForReview, artifact.Status)
	require.Zero(t, applier.calls)
}
