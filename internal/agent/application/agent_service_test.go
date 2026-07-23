package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

// ---------- mocks ----------

type mockAgentRepo struct{ mock.Mock }

func (m *mockAgentRepo) Register(ctx context.Context, cfg *domain.AgentConfig) error {
	return m.Called(ctx, cfg).Error(0)
}
func (m *mockAgentRepo) Get(ctx context.Context, id string) (*domain.AgentConfig, bool, error) {
	args := m.Called(ctx, id)
	cfg, _ := args.Get(0).(*domain.AgentConfig)
	return cfg, args.Bool(1), args.Error(2)
}
func (m *mockAgentRepo) GetSystemAssistant(ctx context.Context) (*domain.AgentConfig, bool, error) {
	args := m.Called(ctx)
	cfg, _ := args.Get(0).(*domain.AgentConfig)
	return cfg, args.Bool(1), args.Error(2)
}
func (m *mockAgentRepo) GetAll(ctx context.Context) ([]*domain.AgentConfig, error) {
	args := m.Called(ctx)
	cfgs, _ := args.Get(0).([]*domain.AgentConfig)
	return cfgs, args.Error(1)
}
func (m *mockAgentRepo) Remove(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}
func (m *mockAgentRepo) Update(ctx context.Context, cfg *domain.AgentConfig) error {
	return m.Called(ctx, cfg).Error(0)
}
func (m *mockAgentRepo) UpdateSystemAssistantModel(ctx context.Context, model string) error {
	return m.Called(ctx, model).Error(0)
}

type mockTenantSettings struct{ mock.Mock }

func (m *mockTenantSettings) GetEmbedModel(ctx context.Context, tenantID string) (string, error) {
	args := m.Called(ctx, tenantID)
	return args.String(0), args.Error(1)
}

type mockSkillLookup struct{ mock.Mock }

func (m *mockSkillLookup) LookupSkill(ctx context.Context, tenantID, skillID string) (string, string, error) {
	args := m.Called(ctx, tenantID, skillID)
	return args.String(0), args.String(1), args.Error(2)
}

type mockMCPTools struct{ mock.Mock }

func (m *mockMCPTools) ToolsForServer(ctx context.Context, serverID string) []port.ToolDefinition {
	args := m.Called(ctx, serverID)
	out, _ := args.Get(0).([]port.ToolDefinition)
	return out
}

type fakeMCPToolPolicyResolver struct{ levels map[string]port.ToolRiskLevel }

func (f fakeMCPToolPolicyResolver) ResolveMCPToolRisk(_ context.Context, _, serverID, toolName string) (port.ToolRiskLevel, error) {
	return f.levels[serverID+":"+toolName], nil
}

type fakeSkillActivationResolver struct{}

type fakeSkillRevisionResolver struct{}

func (fakeSkillRevisionResolver) ResolveSkillRevision(
	_ context.Context, _, _, subjectID string,
) (port.SkillRevisionAssignment, bool, error) {
	if subjectID != "test-subject" {
		return port.SkillRevisionAssignment{}, false, nil
	}
	return port.SkillRevisionAssignment{
		RevisionID: "candidate-1", ExperimentID: "experiment-1", Variant: "canary",
	}, true, nil
}

func (fakeSkillActivationResolver) ResolveSkills(_ context.Context, _ string, refs []port.SkillRevisionRef) (map[string]port.SkillActivation, error) {
	out := make(map[string]port.SkillActivation, len(refs))
	for _, ref := range refs {
		out[ref.SkillID] = port.SkillActivation{SkillID: ref.SkillID, RevisionID: ref.RevisionID, Instructions: "follow instructions"}
	}
	return out, nil
}

type stubMemoryCleaner struct {
	err   error
	calls *int
}

func (s stubMemoryCleaner) ClearAgentMemories(context.Context, string, string) error {
	if s.calls != nil {
		(*s.calls)++
	}
	return s.err
}

type stubChatRepo struct {
	err   error
	calls *int
}

func (s stubChatRepo) CreateConversation(context.Context, string, string, string, string) (*domain.ChatConversation, error) {
	return nil, nil
}
func (s stubChatRepo) GetConversation(context.Context, string, string) (*domain.ChatConversation, error) {
	return nil, nil
}
func (s stubChatRepo) ListConversations(context.Context, string, string, string) ([]*domain.ChatConversation, error) {
	return nil, nil
}
func (s stubChatRepo) RenameConversation(context.Context, string, string, string, string) error {
	return nil
}
func (s stubChatRepo) DeleteConversation(context.Context, string, string, string) error { return nil }
func (s stubChatRepo) AddMessage(context.Context, string, *domain.ChatMessage) error    { return nil }
func (s stubChatRepo) ListMessages(context.Context, string, string, string) ([]*domain.ChatMessage, error) {
	return nil, nil
}
func (s stubChatRepo) CleanupExpired(context.Context, string) error { return nil }
func (s stubChatRepo) DeleteByAgent(context.Context, string, string) error {
	if s.calls != nil {
		(*s.calls)++
	}
	return s.err
}

// satisfy interfaces at compile time
var (
	_ port.AgentRepo       = (*mockAgentRepo)(nil)
	_ port.TenantSettings  = (*mockTenantSettings)(nil)
	_ port.SkillLookup     = (*mockSkillLookup)(nil)
	_ port.MCPToolProvider = (*mockMCPTools)(nil)
)

// ---------- helpers ----------

func newTestService(t *testing.T) (*application.AgentService, *mockAgentRepo, *mockTenantSettings) {
	t.Helper()
	repo := new(mockAgentRepo)
	ts := new(mockTenantSettings)
	reg := application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop())
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:       reg,
		TenantSettings: ts,
		Logger:         zap.NewNop(),
	})
	return svc, repo, ts
}

// ---------- tests ----------

func TestAgentService_Create_InheritsEmbedModel(t *testing.T) {
	svc, repo, ts := newTestService(t)

	ts.On("GetEmbedModel", mock.Anything, "tenant-1").Return("text-embedding-ada-002", nil)
	repo.On("Register", mock.Anything, mock.MatchedBy(func(cfg *domain.AgentConfig) bool {
		return cfg.Name == "TestAgent" && cfg.EmbedModel == "text-embedding-ada-002" && cfg.Type == domain.ReActAgent
	})).Return(nil)

	dto, err := svc.Create(context.Background(), application.CreateAgentInput{
		TenantID:      "tenant-1",
		Name:          "TestAgent",
		Type:          "react",
		LLMModel:      "gpt-4",
		MaxIterations: 10,
	})
	assert.NoError(t, err)
	assert.Equal(t, "TestAgent", dto.Name)
	assert.Equal(t, "text-embedding-ada-002", dto.EmbedModel)
	assert.Equal(t, "react", dto.Type)
	assert.NotEmpty(t, dto.ID)
	repo.AssertExpectations(t)
	ts.AssertExpectations(t)
}

func TestBuildExtraToolsBuildsInstructionSkillCatalogWithoutExecutableTool(t *testing.T) {
	svc := application.NewAgentService(application.AgentServiceDeps{
		SkillActivationResolver: fakeSkillActivationResolver{},
		Logger:                  zap.NewNop(),
	})

	tools, catalog := svc.BuildExtraToolsForTest(context.Background(), "t1", nil, []string{"skill-1"})
	assert.Empty(t, tools)
	assert.Equal(t, "skill-1", catalog["skill-1"].SkillID)
}

func TestBuildExtraToolsUsesExperimentRevisionResolver(t *testing.T) {
	svc := application.NewAgentService(application.AgentServiceDeps{
		SkillActivationResolver: fakeSkillActivationResolver{},
		SkillRevisionResolver:   fakeSkillRevisionResolver{},
		Logger:                  zap.NewNop(),
	})
	tools, catalog := svc.BuildExtraToolsForTest(context.Background(), "tenant-1", nil, []string{"skill-1"})
	assert.Empty(t, tools)
	assert.Equal(t, "candidate-1", catalog["skill-1"].RevisionID)
	assert.Equal(t, "experiment-1", catalog["skill-1"].ExperimentID)
	assert.Equal(t, "canary", catalog["skill-1"].Variant)
}

func TestAgentService_Create_KeepsExplicitEmbedModel(t *testing.T) {
	svc, repo, ts := newTestService(t)

	repo.On("Register", mock.Anything, mock.MatchedBy(func(cfg *domain.AgentConfig) bool {
		return cfg.EmbedModel == "explicit-model"
	})).Return(nil)

	dto, err := svc.Create(context.Background(), application.CreateAgentInput{
		TenantID:      "tenant-1",
		Name:          "TestAgent",
		LLMModel:      "gpt-4",
		EmbedModel:    "explicit-model",
		MaxIterations: 10,
	})
	assert.NoError(t, err)
	assert.Equal(t, "explicit-model", dto.EmbedModel)
	ts.AssertNotCalled(t, "GetEmbedModel")
	repo.AssertExpectations(t)
}

func TestAgentService_Create_PropagatesEmbedLookupError(t *testing.T) {
	svc, _, ts := newTestService(t)
	ts.On("GetEmbedModel", mock.Anything, "tenant-1").
		Return("", errors.New("db down"))

	_, err := svc.Create(context.Background(), application.CreateAgentInput{
		TenantID:      "tenant-1",
		Name:          "TestAgent",
		LLMModel:      "gpt-4",
		MaxIterations: 10,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "embed_model")
}

func TestAgentService_Get(t *testing.T) {
	svc, repo, _ := newTestService(t)

	repo.On("Get", mock.Anything, "agent-1").Return(&domain.AgentConfig{
		ID: "agent-1", Name: "Foo", Type: domain.ReActAgent, LLMModel: "gpt-4",
	}, true, nil)

	dto, err := svc.Get(context.Background(), "agent-1")
	assert.NoError(t, err)
	assert.Equal(t, "agent-1", dto.ID)
	assert.Equal(t, "Foo", dto.Name)
}

func TestAgentService_Get_NotFound(t *testing.T) {
	svc, repo, _ := newTestService(t)
	repo.On("Get", mock.Anything, "missing").Return((*domain.AgentConfig)(nil), false, nil)

	_, err := svc.Get(context.Background(), "missing")
	assert.ErrorIs(t, err, application.ErrNotFound)
}

func TestAgentService_SnapshotRevisionCapturesAuthorizedBindings(t *testing.T) {
	svc, repo, _ := newTestService(t)
	repo.On("Get", mock.Anything, "agent-1").Return(&domain.AgentConfig{
		ID: "agent-1", Type: domain.ReActAgent, SystemPrompt: "be precise", LLMModel: "qwen-plus",
		MaxIterations: 8, MaxContextTokens: 4096,
		AllowedSkills: []string{"skill-1"}, MCPToolIDs: []string{"mcp:server:tool"},
		KnowledgeWorkspaceIDs: []string{"workspace-1"},
	}, true, nil)

	revision, err := svc.SnapshotRevision(context.Background(), "tenant-1", "agent-1")
	assert.NoError(t, err)
	assert.Len(t, revision.Bindings, 3)
	assert.Equal(t, 4096, revision.ModelParameters.MaxContextTokens)
	firstHash, err := revision.ContentHash()
	assert.NoError(t, err)
	secondHash, err := revision.ContentHash()
	assert.NoError(t, err)
	assert.Equal(t, firstHash, secondHash)

	_, err = svc.SnapshotRevision(context.Background(), "", "agent-1")
	assert.ErrorContains(t, err, "tenant id required")
}

func TestAgentService_SnapshotRevisionPreservesExecutionParity(t *testing.T) {
	repo := new(mockAgentRepo)
	registry := application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop())
	registry.SetGlobalSystemSuffix("platform rules")
	registry.SetMemoryInjector(stubMemoryInjector{})
	registry.SetRecallMemoryFn(func(context.Context, string, string, string, string, map[string]any) (string, error) {
		return "", nil
	})
	repo.On("Get", mock.Anything, "agent-1").Return(&domain.AgentConfig{
		ID: "agent-1", Type: domain.ReActAgent, SystemPrompt: "prompt", LLMModel: "model", MaxIterations: 4,
		StuckThreshold: 2, KnowledgeWorkspaceIDs: []string{"workspace-1"},
		KnowledgeWorkspaceNames: []string{"Workspace"}, KnowledgeWorkspaceDescriptions: []string{"Description"},
	}, true, nil)
	svc := application.NewAgentService(application.AgentServiceDeps{Registry: registry, Logger: zap.NewNop()})

	revision, err := svc.SnapshotRevision(context.Background(), "tenant-1", "agent-1")
	assert.NoError(t, err)
	assert.Equal(t, "platform rules", revision.GlobalSystemSuffix)
	assert.Equal(t, 2, revision.StuckThreshold)
	var knowledge domain.AgentBinding
	for _, binding := range revision.Bindings {
		if binding.Kind == domain.AgentBindingKnowledge {
			knowledge = binding
		}
	}
	assert.Equal(t, "Workspace", knowledge.Name)
	assert.Equal(t, "Description", knowledge.Description)
	assert.True(t, revision.MemoryInjectorRequired)
	assert.True(t, revision.RecallMemoryRequired)
}

func TestAgentServiceManagedAssistantRevisionEntrypointsFailClosed(t *testing.T) {
	svc, repo, _ := newTestService(t)
	repo.On("Get", mock.Anything, domain.SystemAssistantID).Return(&domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, LLMModel: "tenant-model",
	}, true, nil)

	_, err := svc.SnapshotRevision(context.Background(), "tenant-1", domain.SystemAssistantID)
	assert.ErrorIs(t, err, domain.ErrSystemAssistantRevisionUnsupported)

	toolCalls := 0
	memoryCalls := 0
	gatewayCalls := 0
	blocked := application.NewAgentService(application.AgentServiceDeps{
		Logger:             zap.NewNop(),
		TenantResolver:     countingRevisionTenantResolver{gateway: countingRevisionGateway{calls: &gatewayCalls}},
		OfficialDocsSearch: func(context.Context, string) ([]domain.Citation, error) { toolCalls++; return nil, nil },
		MemoryInjector:     countingRevisionMemoryInjector{calls: &memoryCalls},
	})
	_, _, err = blocked.ExecuteRevision(context.Background(), domain.AgentRevision{AgentID: domain.SystemAssistantID},
		application.ExecRequest{Query: "crafted"}, application.ExecMeta{TenantID: "tenant-1"})
	assert.ErrorIs(t, err, domain.ErrSystemAssistantRevisionUnsupported)
	assert.Zero(t, toolCalls)
	assert.Zero(t, memoryCalls)
	assert.Zero(t, gatewayCalls)
}

type countingRevisionMemoryInjector struct{ calls *int }

func (m countingRevisionMemoryInjector) BuildContext(context.Context, port.InjectionContext) (string, error) {
	(*m.calls)++
	return "", nil
}

type countingRevisionGateway struct{ calls *int }

func (g countingRevisionGateway) Route(context.Context, port.CapabilityRequest) (port.CapabilityResponse, error) {
	(*g.calls)++
	return port.CapabilityResponse{}, nil
}

type countingRevisionTenantResolver struct{ gateway port.CapabilityGateway }

func (r countingRevisionTenantResolver) Resolve(context.Context, string) (port.CapabilityGateway, map[string]string, bool) {
	return r.gateway, nil, true
}

func (countingRevisionTenantResolver) InjectCompleter(ctx context.Context, _ string) context.Context {
	return ctx
}

func TestAgentService_ExecuteRevisionFailsClosedWhenMemoryHookIsUnavailable(t *testing.T) {
	svc := application.NewAgentService(application.AgentServiceDeps{Logger: zap.NewNop()})
	revision := domain.AgentRevision{AgentID: "agent-1", Type: domain.ReActAgent,
		SystemPrompt: "prompt", Model: "model", MaxIterations: 4, MemoryInjectorRequired: true}
	_, _, err := svc.ExecuteRevision(context.Background(), revision, application.ExecRequest{Query: "hello"},
		application.ExecMeta{TenantID: "tenant-1"})
	assert.ErrorContains(t, err, "requires memory injector")
}

type stubMemoryInjector struct{}

func (stubMemoryInjector) BuildContext(context.Context, port.InjectionContext) (string, error) {
	return "", nil
}

func TestAgentService_List(t *testing.T) {
	svc, repo, _ := newTestService(t)
	repo.On("GetAll", mock.Anything).Return([]*domain.AgentConfig{
		{ID: "a", Name: "A", Type: domain.ReActAgent},
		{ID: "b", Name: "B", Type: domain.CoTAgent},
	}, nil)

	list, err := svc.List(context.Background())
	assert.NoError(t, err)
	assert.Len(t, list, 2)
	assert.Equal(t, "A", list[0].Name)
	assert.Equal(t, "react", list[1].Type)
}

func TestAgentService_ListManagedAssistantFirstPreservesOrdinaryOrder(t *testing.T) {
	svc, repo, _ := newTestService(t)
	repo.On("GetAll", mock.Anything).Return([]*domain.AgentConfig{
		{ID: "ordinary-1", Name: "First", Type: domain.ReActAgent},
		{ID: domain.SystemAssistantID, Name: "Platform", Type: domain.ReActAgent,
			SystemKey: domain.SystemAssistantKey, IsSystem: true, ManagementMode: "platform"},
		{ID: "ordinary-2", Name: "Second", Type: domain.ReActAgent},
	}, nil)

	list, err := svc.List(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, []string{domain.SystemAssistantID, "ordinary-1", "ordinary-2"},
		[]string{list[0].ID, list[1].ID, list[2].ID})
	assert.True(t, list[0].IsSystem)
	assert.Equal(t, "platform", list[0].ManagementMode)
}

type stubTenantModelValidator struct {
	err   error
	calls []string
}

func (v *stubTenantModelValidator) ValidateTenantChatModel(_ context.Context, tenantID, model string) error {
	v.calls = append(v.calls, tenantID+":"+model)
	return v.err
}

func TestAgentService_GetSystemAssistantSettings(t *testing.T) {
	_, repo, _ := newTestService(t)
	validator := &stubTenantModelValidator{}
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:             application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantModelValidator: validator,
		Logger:               zap.NewNop(),
	})
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	repo.On("GetSystemAssistant", ctx).Return(&domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, LLMModel: "qwen-plus",
	}, true, nil)

	settings, err := svc.GetSystemAssistantSettings(ctx)
	assert.NoError(t, err)
	assert.Equal(t, domain.SystemAssistantID, settings.AgentID)
	assert.Equal(t, "qwen-plus", settings.Model)
	assert.True(t, settings.Ready)
	assert.Equal(t, []string{"tenant-1:qwen-plus"}, validator.calls)
}

func TestAgentService_GetSystemAssistantSettingsUnavailableIsNotReady(t *testing.T) {
	_, repo, _ := newTestService(t)
	validator := &stubTenantModelValidator{err: domain.ErrAssistantModelUnavailable}
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:             application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantModelValidator: validator,
		Logger:               zap.NewNop(),
	})
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	repo.On("GetSystemAssistant", ctx).Return(&domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, LLMModel: "qwen-plus",
	}, true, nil)

	settings, err := svc.GetSystemAssistantSettings(ctx)
	assert.NoError(t, err)
	assert.False(t, settings.Ready)
}

func TestAgentService_GetSystemAssistantSettingsFailsClosedOnConfigurationReadFailure(t *testing.T) {
	_, repo, _ := newTestService(t)
	wantErr := errors.New("settings read failed")
	validator := &stubTenantModelValidator{err: wantErr}
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:             application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantModelValidator: validator,
		Logger:               zap.NewNop(),
	})
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	repo.On("GetSystemAssistant", ctx).Return(&domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, LLMModel: "qwen-plus",
	}, true, nil)

	_, err := svc.GetSystemAssistantSettings(ctx)
	assert.ErrorIs(t, err, wantErr)
}

func TestAgentService_UpdateSystemAssistantModelValidatesThenReloads(t *testing.T) {
	_, repo, _ := newTestService(t)
	validator := &stubTenantModelValidator{}
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:             application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantModelValidator: validator,
		Logger:               zap.NewNop(),
	})
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	repo.On("UpdateSystemAssistantModel", ctx, "qwen-plus").Return(nil).Once()
	repo.On("GetSystemAssistant", ctx).Return(&domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, LLMModel: "qwen-plus",
	}, true, nil).Once()

	settings, err := svc.UpdateSystemAssistantModel(ctx, " qwen-plus ")
	assert.NoError(t, err)
	assert.True(t, settings.Ready)
	assert.Equal(t, []string{"tenant-1:qwen-plus"}, validator.calls)
	repo.AssertExpectations(t)
}

func TestAgentService_UpdateSystemAssistantModelRejectsEmptyAndInvalid(t *testing.T) {
	_, repo, _ := newTestService(t)
	validator := &stubTenantModelValidator{err: domain.ErrInvalidSystemAssistantModel}
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:             application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantModelValidator: validator,
		Logger:               zap.NewNop(),
	})
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")

	_, err := svc.UpdateSystemAssistantModel(ctx, " ")
	assert.ErrorIs(t, err, domain.ErrInvalidSystemAssistantModel)
	_, err = svc.UpdateSystemAssistantModel(ctx, "unknown")
	assert.ErrorIs(t, err, domain.ErrInvalidSystemAssistantModel)
	repo.AssertNotCalled(t, "UpdateSystemAssistantModel", mock.Anything, mock.Anything)
}

func TestAgentService_UpdateSystemAssistantModelPropagatesPersistenceFailure(t *testing.T) {
	_, repo, _ := newTestService(t)
	validator := &stubTenantModelValidator{}
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:             application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantModelValidator: validator,
		Logger:               zap.NewNop(),
	})
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	wantErr := errors.New("write failed")
	repo.On("UpdateSystemAssistantModel", ctx, "qwen-plus").Return(wantErr)

	_, err := svc.UpdateSystemAssistantModel(ctx, "qwen-plus")
	assert.ErrorIs(t, err, wantErr)
	repo.AssertNotCalled(t, "GetSystemAssistant", mock.Anything)
}

func TestAgentService_Update_PreservesEmbedModel(t *testing.T) {
	svc, repo, _ := newTestService(t)

	repo.On("Get", mock.Anything, "agent-1").Return(&domain.AgentConfig{
		ID: "agent-1", EmbedModel: "frozen-embed", Type: domain.ReActAgent,
	}, true, nil)
	repo.On("Update", mock.Anything, mock.MatchedBy(func(cfg *domain.AgentConfig) bool {
		return cfg.ID == "agent-1" && cfg.EmbedModel == "frozen-embed" && cfg.Name == "Renamed"
	})).Return(nil)

	dto, err := svc.Update(context.Background(), "agent-1", application.UpdateAgentInput{
		Name: "Renamed", LLMModel: "gpt-4", MaxIterations: 5,
	})
	assert.NoError(t, err)
	assert.Equal(t, "frozen-embed", dto.EmbedModel)
	assert.Equal(t, "Renamed", dto.Name)
}

func TestAgentService_Delete(t *testing.T) {
	svc, repo, _ := newTestService(t)
	repo.On("Get", mock.Anything, "agent-1").Return(&domain.AgentConfig{ID: "agent-1"}, true, nil)
	repo.On("Remove", mock.Anything, "agent-1").Return(nil)

	err := svc.Delete(context.Background(), "tenant-1", "agent-1")
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestAgentService_DeleteSystemAssistantRejectsBeforeCleanup(t *testing.T) {
	repo := new(mockAgentRepo)
	registry := application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop())
	memoryCalls := 0
	chatCalls := 0
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:      registry,
		MemoryCleaner: stubMemoryCleaner{calls: &memoryCalls},
		ChatStore:     stubChatRepo{calls: &chatCalls},
		Logger:        zap.NewNop(),
	})
	ctx := context.Background()
	const id = "stratum-platform-assistant"
	repo.On("Get", ctx, id).Return(&domain.AgentConfig{
		ID: id, SystemKey: "stratum.platform_assistant", IsSystem: true, ManagementMode: "platform",
	}, true, nil)
	repo.On("Remove", ctx, id).Return(domain.ErrSystemAssistantManaged).Maybe()

	err := svc.Delete(ctx, "tenant-1", id)

	assert.ErrorIs(t, err, domain.ErrSystemAssistantManaged)
	assert.Zero(t, memoryCalls)
	assert.Zero(t, chatCalls)
	repo.AssertNotCalled(t, "Remove", mock.Anything, mock.Anything)
}

func TestAgentService_DeletePropagatesIdentityLookupFailureBeforeCleanup(t *testing.T) {
	repo := new(mockAgentRepo)
	registry := application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop())
	memoryCalls := 0
	chatCalls := 0
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:      registry,
		MemoryCleaner: stubMemoryCleaner{calls: &memoryCalls},
		ChatStore:     stubChatRepo{calls: &chatCalls},
		Logger:        zap.NewNop(),
	})
	ctx := context.Background()
	wantErr := errors.New("identity lookup failed")
	repo.On("Get", ctx, "agent-1").Return((*domain.AgentConfig)(nil), false, wantErr)

	err := svc.Delete(ctx, "tenant-1", "agent-1")

	assert.ErrorIs(t, err, wantErr)
	assert.Zero(t, memoryCalls)
	assert.Zero(t, chatCalls)
	repo.AssertNotCalled(t, "Remove", mock.Anything, mock.Anything)
}

func TestAgentService_DeleteNotFoundRejectsBeforeCleanup(t *testing.T) {
	repo := new(mockAgentRepo)
	registry := application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop())
	memoryCalls := 0
	chatCalls := 0
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:      registry,
		MemoryCleaner: stubMemoryCleaner{calls: &memoryCalls},
		ChatStore:     stubChatRepo{calls: &chatCalls},
		Logger:        zap.NewNop(),
	})
	ctx := context.Background()
	repo.On("Get", ctx, "missing").Return((*domain.AgentConfig)(nil), false, nil)

	err := svc.Delete(ctx, "tenant-1", "missing")

	assert.ErrorIs(t, err, application.ErrNotFound)
	assert.Zero(t, memoryCalls)
	assert.Zero(t, chatCalls)
	repo.AssertNotCalled(t, "Remove", mock.Anything, mock.Anything)
}

func TestAgentService_DeleteReturnsCleanupErrorBeforeRemovingRegistry(t *testing.T) {
	repo := new(mockAgentRepo)
	wantErr := errors.New("memory cleanup failed")
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:      application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		MemoryCleaner: stubMemoryCleaner{err: wantErr}, Logger: zap.NewNop(),
	})
	repo.On("Get", mock.Anything, "agent-1").Return(&domain.AgentConfig{ID: "agent-1"}, true, nil)

	err := svc.Delete(context.Background(), "tenant-1", "agent-1")
	assert.ErrorIs(t, err, wantErr)
	repo.AssertNotCalled(t, "Remove", mock.Anything, mock.Anything)
}

func TestAgentService_DeleteReturnsChatCleanupErrorBeforeRemovingRegistry(t *testing.T) {
	repo := new(mockAgentRepo)
	wantErr := errors.New("chat cleanup failed")
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry: application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()), ChatStore: stubChatRepo{err: wantErr}, Logger: zap.NewNop(),
	})
	repo.On("Get", mock.Anything, "agent-1").Return(&domain.AgentConfig{ID: "agent-1"}, true, nil)

	err := svc.Delete(context.Background(), "tenant-1", "agent-1")
	assert.ErrorIs(t, err, wantErr)
	repo.AssertNotCalled(t, "Remove", mock.Anything, mock.Anything)
}

// ---------- Task 3: execute/extra-tools/record-execution ----------

func TestAgentService_BuildExtraTools_Empty(t *testing.T) {
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry: application.NewRegistry(new(mockAgentRepo), application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		Logger:   zap.NewNop(),
	})
	tools, _ := svc.BuildExtraToolsForTest(context.Background(), "tenant-1", nil, nil)
	assert.Empty(t, tools)
}

func TestAgentService_BuildExtraTools_MCPDelegates(t *testing.T) {
	repo := new(mockAgentRepo)
	mcpProv := new(mockMCPTools)
	mcpProv.On("ToolsForServer", mock.Anything, "srv1").Return([]port.ToolDefinition{
		{Name: "mcp:srv1:search", Description: "web search"},
	})
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry: application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		MCPTools: mcpProv,
		Logger:   zap.NewNop(),
	})
	tools, _ := svc.BuildExtraToolsForTest(context.Background(), "tenant-1", []string{"mcp:srv1:search"}, nil)
	assert.Len(t, tools, 1)
	assert.Equal(t, "mcp:srv1:search", tools[0].Name)
	mcpProv.AssertExpectations(t)
}

func TestAgentService_BuildExtraToolsAppliesTenantOwnedRiskPolicy(t *testing.T) {
	mcpProv := new(mockMCPTools)
	mcpProv.On("ToolsForServer", mock.Anything, "orders").Return([]port.ToolDefinition{{Name: "mcp:orders:get", CapabilityID: "get"}, {Name: "mcp:orders:delete", CapabilityID: "delete"}})
	svc := application.NewAgentService(application.AgentServiceDeps{
		MCPTools:      mcpProv,
		MCPToolPolicy: fakeMCPToolPolicyResolver{levels: map[string]port.ToolRiskLevel{"orders:get": port.ToolRiskRead, "orders:delete": port.ToolRiskDestructive}},
		Logger:        zap.NewNop(),
	})
	tools, _ := svc.BuildExtraToolsForTest(context.Background(), "tenant-1", []string{"mcp:orders:get", "mcp:orders:delete"}, nil)
	assert.Equal(t, "read", tools[0].Metadata["risk_level"])
	assert.Equal(t, "destructive", tools[1].Metadata["risk_level"])
}

func TestAgentService_BuildExtraToolsDefaultsMissingRiskToUnclassified(t *testing.T) {
	mcpProv := new(mockMCPTools)
	mcpProv.On("ToolsForServer", mock.Anything, "orders").Return([]port.ToolDefinition{{Name: "mcp:orders:mystery", CapabilityID: "mystery"}})
	svc := application.NewAgentService(application.AgentServiceDeps{MCPTools: mcpProv, Logger: zap.NewNop()})
	tools, _ := svc.BuildExtraToolsForTest(context.Background(), "tenant-1", []string{"mcp:orders:mystery"}, nil)
	assert.Equal(t, "unclassified", tools[0].Metadata["risk_level"])
}

func TestAgentService_Execute_NotFound(t *testing.T) {
	repo := new(mockAgentRepo)
	repo.On("Get", mock.Anything, "missing").Return((*domain.AgentConfig)(nil), false, nil)

	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry: application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		Logger:   zap.NewNop(),
	})
	_, _, err := svc.Execute(context.Background(), "missing", application.ExecRequest{Query: "hi"}, application.ExecMeta{TenantID: "t1"})
	assert.ErrorIs(t, err, application.ErrNotFound)
}
