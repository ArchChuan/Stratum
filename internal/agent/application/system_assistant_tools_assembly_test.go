package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	versioningdomain "github.com/byteBuilderX/stratum/internal/versioning/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type assistantDiagnosticStub struct {
	authorizeCalls int
	role           string
	authorizeErr   error
}

func (s *assistantDiagnosticStub) Authorize(
	_ context.Context,
	req domain.DiagnosticRequest,
) (domain.DiagnosticAuthorization, error) {
	s.authorizeCalls++
	if s.authorizeErr != nil {
		return domain.DiagnosticAuthorization{}, s.authorizeErr
	}
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

func (genericMCPTools) ToolsForServer(_ context.Context, _ string, serverID string) []port.ToolDefinition {
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
		Registry:           NewRegistry(nil, zap.NewNop()),
		DiagnosticProvider: diagnostics,
		OfficialDocsSearch: func(_ context.Context, query string) ([]domain.Citation, error) {
			return []domain.Citation{{Title: query}}, nil
		},
		TenantModelValidator: &strictModelValidatorStub{},
	})
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID:       domain.SystemAssistantID,
		LLMModel: "tenant-model", MaxIterations: 3,
	}}

	_, options, err := svc.assembleOptions(t.Context(), system, ExecRequest{UserID: "user-1"},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")

	require.NoError(t, err)
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	require.Len(t, cfg.ExtraTools, 8)
	for _, tool := range cfg.ExtraTools {
		require.Equal(t, domain.ProviderTypeInternal, tool.ProviderType)
		require.Equal(t, tool.Name, tool.CapabilityID)
		require.Empty(t, tool.ServerID)
	}
	require.Equal(t, "member", cfg.AssistantRoleClass)
	require.NotNil(t, cfg.OfficialDocsSearchFn)
	require.NotNil(t, cfg.DiagnosticFn)
	require.Nil(t, cfg.ProposalCreateFn)
	require.NotNil(t, cfg.InternalToolResultGuardFn)
	// Registry 注入时装配 list_agents；MCPServerLister 未注入时 list_mcp_servers
	// fail-closed（Fn nil）。
	require.NotNil(t, cfg.ListAgentsFn)
	require.Nil(t, cfg.ListMCPServersFn)
	require.Equal(t, 1, diagnostics.authorizeCalls)
}

func TestSystemAssistantProposalToolVisibleInProcessWithoutService(t *testing.T) {
	diagnostics := &assistantDiagnosticStub{role: "admin"}
	svc := NewAgentService(AgentServiceDeps{
		Registry:           NewRegistry(nil, zap.NewNop()),
		DiagnosticProvider: diagnostics,
		OfficialDocsSearch: func(_ context.Context, query string) ([]domain.Citation, error) {
			return []domain.Citation{{Title: query}}, nil
		},
		TenantModelValidator: &strictModelValidatorStub{},
	})
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID:       domain.SystemAssistantID,
		LLMModel: "tenant-model", MaxIterations: 3,
	}}
	_, options, err := svc.assembleOptions(t.Context(), system, ExecRequest{UserID: "user-1"},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")
	require.NoError(t, err)
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	require.Equal(t, "admin", cfg.AssistantRoleClass)
	// D6：工具可见性全角色一致（8 个），无需按角色注入；ProposalCreateFn
	// 仅在装配 ProposalService 时注入（见 TestAdminProposeAutoConfirmsAndApplies）。
	require.Len(t, cfg.ExtraTools, 8)
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
		Registry:             NewRegistry(repo, zap.NewNop()),
		DiagnosticProvider:   &assistantDiagnosticStub{role: "member"},
		OfficialDocsSearch:   func(_ context.Context, query string) ([]domain.Citation, error) { return nil, nil },
		TenantModelValidator: validator,
		TenantModelCatalog:   validator,
		ModelDetailsProvider: details,
		Logger:               zap.NewNop(),
	})
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID:       domain.SystemAssistantID,
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
	_, updateErr := cfg.UpdateSystemModelFn(context.Background(), "qwen-plus", "")
	require.ErrorContains(t, updateErr, "管理员权限")
}

func TestSystemAssistantAdminUpdateSystemModelExecutes(t *testing.T) {
	repo := new(mockAgentRepo)
	validator := &stubTenantModelValidator{}
	// SystemActor 注入：等同化后 update 走普通 ownership 链路，系统上下文放行。
	ctx := reqctx.WithSystemActor(reqctx.WithTenantID(context.Background(), "tenant-1"), "system")
	svc := NewAgentService(AgentServiceDeps{
		Registry:             NewRegistry(repo, zap.NewNop()),
		DiagnosticProvider:   &assistantDiagnosticStub{role: "admin"},
		OfficialDocsSearch:   func(_ context.Context, query string) ([]domain.Citation, error) { return nil, nil },
		TenantModelValidator: validator,
		TenantModelCatalog:   validator,
		Logger:               zap.NewNop(),
	})
	existing := &domain.AgentConfig{
		ID: domain.SystemAssistantID, LLMModel: "existing-model",
	}
	repo.On("Get", mock.Anything, domain.SystemAssistantID).Return(existing, true, nil)
	repo.On("Update", mock.Anything).Return(nil)
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID:       domain.SystemAssistantID,
		LLMModel: "tenant-model", MaxIterations: 3,
	}}
	_, options, err := svc.assembleOptions(t.Context(), system, ExecRequest{UserID: "user-1"},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")
	require.NoError(t, err)
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)

	result, updateErr := cfg.UpdateSystemModelFn(ctx, "qwen-plus", "")
	require.NoError(t, updateErr)
	require.Equal(t, "qwen-plus", result["model"])
	require.Equal(t, true, result["ready"])
	require.Equal(t, []string{"qwen-plus", "qwen-plus-latest", "qwen-max"}, result["availableModels"])
	repo.AssertExpectations(t)
}

func TestSystemAssistantWithoutModelFailsBeforeCapabilityResolution(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{
		Registry: NewRegistry(nil, zap.NewNop()),
	})
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: domain.SystemAssistantID, MaxIterations: 3,
	}}
	_, _, err := svc.assembleOptions(t.Context(), system, ExecRequest{},
		ExecMeta{TenantID: "tenant-1"}, "execution-1")
	require.ErrorIs(t, err, domain.ErrAssistantModelUnavailable)
}

func TestOrdinaryAgentResolvesMCPToolsFromProvider(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{MCPTools: genericMCPTools{},
		TenantModelValidator: &stubTenantModelValidator{}})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		LLMModel: "test-model", ID: "ordinary", MaxIterations: 3, MCPToolIDs: []string{"mcp:orders:get"},
	}}
	_, options, err := svc.assembleOptions(t.Context(), agent, ExecRequest{},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")
	require.NoError(t, err)
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	// D17/D19 等化后普通 agent 也注入 8 个内置运维工具：MCP 工具 + 8 = 9。
	require.Len(t, cfg.ExtraTools, 9)
	require.Equal(t, "mcp:orders:get", cfg.ExtraTools[0].Name)
	require.Equal(t, domain.ProviderTypeMCP, cfg.ExtraTools[0].ProviderType)
	for _, def := range cfg.ExtraTools[1:] {
		require.Equal(t, domain.ProviderTypeInternal, def.ProviderType)
	}
}

func TestSystemAssistantTreatsStaleModelAsUnavailable(t *testing.T) {
	validator := &strictModelValidatorStub{err: domain.ErrInvalidAgentModel}
	svc := NewAgentService(AgentServiceDeps{
		Registry:             NewRegistry(nil, zap.NewNop()),
		TenantModelValidator: validator,
	})
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID:       domain.SystemAssistantID,
		LLMModel: "retired-model", MaxIterations: 3,
	}}
	_, _, err := svc.assembleOptions(t.Context(), system, ExecRequest{},
		ExecMeta{TenantID: "tenant-1"}, "execution-1")
	require.ErrorIs(t, err, domain.ErrAssistantModelUnavailable)
	require.False(t, errors.Is(err, domain.ErrInvalidAgentModel))
}

func TestBuildExecutionArtifactsPreservesAssistantFailureAsEvidenceGap(t *testing.T) {
	artifacts := buildExecutionArtifacts([]domain.SystemAssistantToolArtifact{{
		Tool: domain.SystemAssistantToolSearchOfficialDocs, Outcome: "error", ErrorCode: "not_found",
	}}, domain.CurrentExecutionArtifactProfileVersion)
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

func (m *mockAgentRepo) GetAll(_ context.Context) ([]*domain.AgentConfig, error) {
	args := m.Called()
	return args.Get(0).([]*domain.AgentConfig), args.Error(1)
}

func (m *mockAgentRepo) Remove(_ context.Context, id string, _ *auditdomain.ResourceChangeAuditEvent) error {
	return m.Called(id).Error(0)
}

func (m *mockAgentRepo) Update(_ context.Context, cfg *domain.AgentConfig, _ *auditdomain.ResourceChangeAuditEvent, _ string, _ bool, _ *versioningdomain.Version) error {
	return m.Called(cfg).Error(0)
}

func (m *mockAgentRepo) Rollback(_ context.Context, cfg *domain.AgentConfig, _ *auditdomain.ResourceChangeAuditEvent, _, _ string) error {
	return m.Called(cfg).Error(0)
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

// TestProposalToolSchemaIsLooseProjection 守护提案工具的模型可见 schema 为
// 松散投影：payload 只声明 type:object、不再展开字段级约束（8 分支 oneOf
// 全量 schema 每轮透传给 provider 是 prompt 膨胀主因，旧结构 5.7KB ≈ 2.9k
// tokens，新结构 340B ≈ 170 tokens）。字段级校验由执行边界
// validateProposalPayloadSchema 兜底，此处禁止回退为详细 schema。
func TestProposalToolSchemaIsLooseProjection(t *testing.T) {
	schema := proposalToolSchema()
	require.NotContains(t, schema, "oneOf", "payload 字段约束不得以 oneOf 形式展开给模型")

	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok, "schema must be a flat object with properties")

	payload, ok := props["payload"].(map[string]any)
	require.True(t, ok, "payload property must exist")
	require.Equal(t, "object", payload["type"])
	require.NotContains(t, payload, "properties", "payload 字段级 schema 不下沉到模型可见层")
	require.NotContains(t, payload, "required")

	// 顶层枚举保留，供模型在合法值内选填。
	require.Equal(t, []any{"agent", "skill_draft", "mcp_config", "knowledge_workspace"},
		props["resourceKind"].(map[string]any)["enum"])
	require.Equal(t, []any{"create", "update"},
		props["operation"].(map[string]any)["enum"])

	// 必填顶层三键与运行时解析契约（ParseResourceChangeToolArguments）对齐。
	require.Equal(t, []any{"resourceKind", "operation", "payload"}, schema["required"])
}

func TestAdminProposeAutoConfirmsAndApplies(t *testing.T) {
	repo := newProposalRepoFake()
	applier := &proposalApplierFake{result: domain.ApplyResult{ResourceID: "created"}}
	proposalService := newProposalServiceForTest(repo, &proposalAuthorizerFake{}, &baselineFake{},
		map[domain.ResourceKind]port.ResourceChangeApplier{domain.ResourceAgent: applier})
	svc := NewAgentService(AgentServiceDeps{
		Registry:             NewRegistry(new(mockAgentRepo), zap.NewNop()),
		DiagnosticProvider:   &assistantDiagnosticStub{role: "admin"},
		ProposalService:      proposalService,
		TenantModelValidator: &stubTenantModelValidator{},
		Logger:               zap.NewNop(),
	})
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID:       domain.SystemAssistantID,
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
		Registry:             NewRegistry(new(mockAgentRepo), zap.NewNop()),
		DiagnosticProvider:   &assistantDiagnosticStub{role: "member"},
		ProposalService:      proposalService,
		TenantModelValidator: &stubTenantModelValidator{},
		Logger:               zap.NewNop(),
	})
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID:       domain.SystemAssistantID,
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
		Registry:             NewRegistry(new(mockAgentRepo), zap.NewNop()),
		DiagnosticProvider:   &assistantDiagnosticStub{role: "admin"},
		ProposalService:      proposalService,
		TenantModelValidator: &stubTenantModelValidator{},
		Logger:               zap.NewNop(),
	})
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID:       domain.SystemAssistantID,
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

func TestMemberApplyRejectedBeforeApplier(t *testing.T) {
	called := false
	svc := NewAgentService(AgentServiceDeps{
		Registry:           NewRegistry(new(mockAgentRepo), zap.NewNop()),
		DiagnosticProvider: &assistantDiagnosticStub{role: "member"},
		ResourceChangeApplier: func(_ context.Context, _ string, _ map[string]any) (domain.ApplyResult, error) {
			called = true
			return domain.ApplyResult{}, nil
		},
		TenantModelValidator: &stubTenantModelValidator{},
		Logger:               zap.NewNop(),
	})
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID:       domain.SystemAssistantID,
		LLMModel: "tenant-model", MaxIterations: 3,
	}}
	_, options, err := svc.assembleOptions(t.Context(), system, ExecRequest{UserID: "member-1"},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")
	require.NoError(t, err)
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	require.NotNil(t, cfg.ResourceChangeApplyFn)

	// 写路径 fail closed：member 直接拒绝，不触达 applier，与 update_system_model
	// 的 member 拒绝模式一致；错误同时满足 errors.Is 哨兵与可读文案。
	result, applyErr := cfg.ResourceChangeApplyFn(context.Background(), map[string]any{})
	require.ErrorIs(t, applyErr, domain.ErrProposalForbidden)
	require.ErrorContains(t, applyErr, "管理员权限")
	require.False(t, called, "member apply must not reach the applier")
	require.Empty(t, result)
}

func TestSystemAssistantListAgentsProjectionOmitsSensitiveFields(t *testing.T) {
	repo := new(mockAgentRepo)
	repo.On("GetAll").Return([]*domain.AgentConfig{{
		ID: "agent-1", Name: "sales", Type: domain.ReActAgent, Description: "d",
		LLMModel: "m", MaxIterations: 3, MaxContextTokens: 100,
		SystemPrompt: "secret-prompt",
	}}, nil)
	svc := NewAgentService(AgentServiceDeps{
		Registry:             NewRegistry(repo, zap.NewNop()),
		DiagnosticProvider:   &assistantDiagnosticStub{role: "member"},
		OfficialDocsSearch:   func(_ context.Context, query string) ([]domain.Citation, error) { return nil, nil },
		TenantModelValidator: &stubTenantModelValidator{},
		Logger:               zap.NewNop(),
	})
	system := &optionCaptureAgent{config: &domain.AgentConfig{
		ID:       domain.SystemAssistantID,
		LLMModel: "tenant-model", MaxIterations: 3,
	}}
	_, options, err := svc.assembleOptions(t.Context(), system, ExecRequest{UserID: "user-1"},
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"}, "execution-1")
	require.NoError(t, err)
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	require.NotNil(t, cfg.ListAgentsFn)

	raw, listErr := cfg.ListAgentsFn(context.Background())
	require.NoError(t, listErr)
	blob, marshalErr := json.Marshal(raw)
	require.NoError(t, marshalErr)
	s := string(blob)
	// 安全投影：保留 id/name 等元数据，绝不携带 systemPrompt（system_key 字段已随
	// 「所有 agent 一视同仁」删除，投影天然不含）。
	require.Contains(t, s, "agent-1")
	require.Contains(t, s, "sales")
	require.NotContains(t, s, "secret-prompt")
	require.NotContains(t, s, "secret-key")
	require.NotContains(t, s, "systemPrompt")
}

// D18：无 HTTP actor 的内部执行（revision/评估/工作流）没有用户上下文，
// Authorize 失败回退 member（self 范围），不阻断合法内部流程。
func TestResolveTooling_NoActor_AuthorizeFailure_FallsBackToMember(t *testing.T) {
	diagnostics := &assistantDiagnosticStub{authorizeErr: errors.New("authorize unavailable")}
	svc := NewAgentService(AgentServiceDeps{
		Registry:             NewRegistry(nil, zap.NewNop()),
		DiagnosticProvider:   diagnostics,
		TenantModelValidator: &strictModelValidatorStub{},
	})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: domain.SystemAssistantID, LLMModel: "tenant-model",
	}}
	authorization := &domain.DiagnosticAuthorization{}
	_, _, roleClass, err := svc.resolveTooling(t.Context(),
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"},
		ExecRequest{UserID: ""},
		agent, "subject-1", authorization)
	require.NoError(t, err)
	require.Equal(t, "member", roleClass)
	require.Equal(t, "member", authorization.RoleClass)
}

// D18：HTTP 执行带真实 UUID actor（JWT sub 派生），Authorize 失败维持 fail-closed。
func TestResolveTooling_HTTPActor_AuthorizeFailure_FailsClosed(t *testing.T) {
	diagnostics := &assistantDiagnosticStub{authorizeErr: errors.New("authorize unavailable")}
	svc := NewAgentService(AgentServiceDeps{
		Registry:             NewRegistry(nil, zap.NewNop()),
		DiagnosticProvider:   diagnostics,
		TenantModelValidator: &strictModelValidatorStub{},
	})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: domain.SystemAssistantID, LLMModel: "tenant-model",
	}}
	authorization := &domain.DiagnosticAuthorization{}
	_, _, _, err := svc.resolveTooling(t.Context(),
		ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"},
		ExecRequest{UserID: uuid.NewString()},
		agent, "subject-1", authorization)
	require.Error(t, err)
	require.ErrorContains(t, err, "authorize unavailable")
}

// D18：内部执行（collab/workflow）使用合成非 UUID 标识，Authorize 失败
// 同样回退 member（self 范围），不阻断合法内部流程。
func TestResolveTooling_SyntheticUser_AuthorizeFailure_FallsBackToMember(t *testing.T) {
	diagnostics := &assistantDiagnosticStub{authorizeErr: errors.New("authorize unavailable")}
	svc := NewAgentService(AgentServiceDeps{
		Registry:             NewRegistry(nil, zap.NewNop()),
		DiagnosticProvider:   diagnostics,
		TenantModelValidator: &strictModelValidatorStub{},
	})
	agent := &optionCaptureAgent{config: &domain.AgentConfig{
		ID: domain.SystemAssistantID, LLMModel: "tenant-model",
	}}
	for _, userID := range []string{"collab:plan-01h0", "workflow"} {
		authorization := &domain.DiagnosticAuthorization{}
		_, _, roleClass, err := svc.resolveTooling(t.Context(),
			ExecMeta{TenantID: "tenant-1", TraceID: "trace-1"},
			ExecRequest{UserID: userID},
			agent, "subject-1", authorization)
		require.NoError(t, err)
		require.Equal(t, "member", roleClass)
		require.Equal(t, "member", authorization.RoleClass)
	}
}
