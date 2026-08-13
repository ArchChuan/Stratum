package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/zap"
)

// ---------- mocks ----------

type mockAgentRepo struct{ mock.Mock }

type preparationAgentRevisionResolver struct{}

func (preparationAgentRevisionResolver) ResolveAgentRevision(
	context.Context, string, string, string,
) (port.AgentRevisionAssignment, bool, error) {
	return port.AgentRevisionAssignment{
		Revision: domain.AgentRevision{
			AgentID: "agent-1", Type: domain.ReActAgent,
			SystemPrompt: "canary prompt", Model: "qwen-plus", MaxIterations: 3,
			Bindings: []domain.AgentBinding{{
				Kind: domain.AgentBindingMCP, ID: "mcp:server-1:lookup", Enabled: true,
			}},
		},
		RevisionID: "agent-revision-canary", ExperimentID: "experiment-agent", Variant: "canary",
	}, true, nil
}

type preparationMCPTools struct{}

func (preparationMCPTools) ToolsForServer(context.Context, string, string) []port.ToolDefinition {
	return []port.ToolDefinition{{
		Name: "mcp:server-1:lookup", ProviderType: domain.ProviderTypeMCP,
		ServerID: "server-1", CapabilityID: "lookup",
	}}
}

type failingPreparationMCPRevisionResolver struct{ err error }

func (f failingPreparationMCPRevisionResolver) ResolveMCPRevision(
	context.Context, string, string, string,
) (port.MCPRevisionAssignment, bool, error) {
	return port.MCPRevisionAssignment{}, false, f.err
}

func (m *mockAgentRepo) Register(ctx context.Context, cfg *domain.AgentConfig, _ *auditdomain.ResourceChangeAuditEvent, _ []string) error {
	return m.Called(ctx, cfg).Error(0)
}
func (m *mockAgentRepo) Get(ctx context.Context, id string) (*domain.AgentConfig, bool, error) {
	args := m.Called(ctx, id)
	cfg, _ := args.Get(0).(*domain.AgentConfig)
	return cfg, args.Bool(1), args.Error(2)
}

func TestAgentServicePreparationFailureRemainsObservable(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(context.Context, *application.AgentService) error
	}{
		{
			name: "execute",
			run: func(ctx context.Context, svc *application.AgentService) error {
				_, _, err := svc.Execute(ctx, "agent-1", application.ExecRequest{
					UserID: "user-1", Query: "use the lookup tool",
				}, application.ExecMeta{TenantID: "tenant-1", TraceID: "business-trace-1"})
				return err
			},
		},
		{
			name: "execute stream",
			run: func(ctx context.Context, svc *application.AgentService) error {
				_, _, _, err := svc.ExecuteStream(ctx, "agent-1", application.ExecRequest{
					UserID: "user-1", Query: "use the lookup tool",
				}, application.ExecMeta{TenantID: "tenant-1", TraceID: "business-trace-1"}, nil)
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			previous := otel.GetTracerProvider()
			otel.SetTracerProvider(provider)
			t.Cleanup(func() {
				otel.SetTracerProvider(previous)
				_ = provider.Shutdown(context.Background())
			})

			repo := new(mockAgentRepo)
			repo.On("Get", mock.Anything, "agent-1").Return(&domain.AgentConfig{
				ID: "agent-1", Name: "Mutable Agent", Type: domain.ReActAgent,
				SystemPrompt: "mutable prompt", LLMModel: "qwen-plus", MaxIterations: 3,
			}, true, nil).Once()
			dependencyErr := errors.New("mcp revision backend leaked-secret-body")
			svc := application.NewAgentService(application.AgentServiceDeps{
				Registry: application.NewRegistry(
					repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop(),
				),
				AgentRevisionResolver: preparationAgentRevisionResolver{},
				MCPTools:              preparationMCPTools{},
				MCPRevisionResolver: failingPreparationMCPRevisionResolver{
					err: dependencyErr,
				},
			})

			ctx, requestSpan := otel.Tracer("test/http").Start(context.Background(), "/agents/:id/execute")
			err := tc.run(ctx, svc)
			requestSpan.End()
			require.ErrorIs(t, err, dependencyErr)

			var request sdktrace.ReadOnlySpan
			for _, span := range recorder.Ended() {
				if span.Name() == "/agents/:id/execute" {
					request = span
					break
				}
			}
			require.NotNil(t, request)
			attrs := spanAttributes(request)
			require.Equal(t, "tenant-1", attrs["opik.metadata.stratum.tenant_id"])
			require.Equal(t, "business-trace-1", attrs["opik.metadata.stratum.trace_id"])
			require.NotEmpty(t, attrs["opik.metadata.stratum.execution_id"])
			require.Equal(t, "agent-1", attrs["opik.metadata.stratum.agent_id"])
			require.Equal(t, "Mutable Agent", attrs["opik.metadata.stratum.agent_name"])
			require.Equal(t, "error", attrs["opik.metadata.stratum.status"])
			require.Equal(t, "resource_preparation_failed", attrs["opik.metadata.stratum.error_category"])
			require.Equal(t, "assemble_options", attrs["opik.metadata.stratum.failure_stage"])
			require.Contains(t, attrs, "opik.metadata.stratum.duration_ms")

			var manifest map[string]string
			require.NoError(t, json.Unmarshal(
				[]byte(attrs["opik.metadata.stratum.resource_manifest"].(string)), &manifest,
			))
			require.Equal(t, "agent-revision-canary", manifest["agent:agent-1"])
			var assignments map[string]application.ExperimentAssignment
			require.NoError(t, json.Unmarshal(
				[]byte(attrs["opik.metadata.stratum.experiment_assignments"].(string)), &assignments,
			))
			require.Equal(t, application.ExperimentAssignment{
				ExperimentID: "experiment-agent", Variant: "canary",
			}, assignments["agent:agent-1"])
			for key, value := range attrs {
				require.False(t, strings.Contains(fmt.Sprint(value), "leaked-secret-body"), key)
			}
			repo.AssertExpectations(t)
		})
	}
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
func (m *mockAgentRepo) Remove(ctx context.Context, id string, _ *auditdomain.ResourceChangeAuditEvent) error {
	return m.Called(ctx, id).Error(0)
}
func (m *mockAgentRepo) Update(ctx context.Context, cfg *domain.AgentConfig, _ *auditdomain.ResourceChangeAuditEvent, _ string, _ bool) error {
	return m.Called(ctx, cfg).Error(0)
}
func (m *mockAgentRepo) UpdateSystemAssistantModel(ctx context.Context, model string, memoryScope string, checkpointEnabled bool, maxIterations int, maxContextTokens int, _ *auditdomain.ResourceChangeAuditEvent) (*domain.AgentConfig, error) {
	args := m.Called(ctx, model, memoryScope, checkpointEnabled, maxIterations, maxContextTokens)
	cfg, _ := args.Get(0).(*domain.AgentConfig)
	return cfg, args.Error(1)
}
func (m *mockAgentRepo) UpdateSystemAssistantAll(ctx context.Context, model string, memoryScope string, checkpointEnabled bool, maxIterations int, maxContextTokens int, maxTokens int, _ *auditdomain.ResourceChangeAuditEvent) (*domain.AgentConfig, error) {
	args := m.Called(ctx, model, memoryScope, checkpointEnabled, maxIterations, maxContextTokens, maxTokens)
	cfg, _ := args.Get(0).(*domain.AgentConfig)
	return cfg, args.Error(1)
}

func (m *mockAgentRepo) UpdateSystemAssistantBindings(ctx context.Context, mcpToolIDs, knowledgeWorkspaceIDs, allowedSkills []string) (*domain.AgentConfig, error) {
	args := m.Called(ctx, mcpToolIDs, knowledgeWorkspaceIDs, allowedSkills)
	cfg, _ := args.Get(0).(*domain.AgentConfig)
	return cfg, args.Error(1)
}

type mockSkillLookup struct{ mock.Mock }

func (m *mockSkillLookup) LookupSkill(ctx context.Context, tenantID, skillID string) (string, string, error) {
	args := m.Called(ctx, tenantID, skillID)
	return args.String(0), args.String(1), args.Error(2)
}

type mockMCPTools struct{ mock.Mock }

func (m *mockMCPTools) ToolsForServer(ctx context.Context, tenantID, serverID string) []port.ToolDefinition {
	args := m.Called(ctx, tenantID, serverID)
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
	_ port.SkillLookup     = (*mockSkillLookup)(nil)
	_ port.MCPToolProvider = (*mockMCPTools)(nil)
)

// ---------- helpers ----------

func newTestService(t *testing.T) (*application.AgentService, *mockAgentRepo) {
	t.Helper()
	return newTestServiceWithProvider(t, nil)
}

func newTestServiceWithProvider(t *testing.T, provider port.ParametersProvider) (*application.AgentService, *mockAgentRepo) {
	t.Helper()
	repo := new(mockAgentRepo)
	reg := application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop())
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:           reg,
		ParametersProvider: provider,
		Logger:             zap.NewNop(),
	})
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	return svc, repo
}

// validatingParametersProvider accepts every declared sampling value (registry
// healthy); rejectingParametersProvider rejects any non-empty declared set
// (out-of-bounds), so 0=unset PUTs still pass pre-merge validation.
type validatingParametersProvider struct{}

func (validatingParametersProvider) ResolveForResource(context.Context, map[string]any) (map[string]any, error) {
	return nil, nil
}
func (validatingParametersProvider) Resolve(context.Context, string, map[string]any) (any, bool, error) {
	return nil, false, nil
}
func (validatingParametersProvider) ValidateResource(_ context.Context, _ map[string]any) error {
	return nil
}

type rejectingParametersProvider struct{}

func (rejectingParametersProvider) ResolveForResource(context.Context, map[string]any) (map[string]any, error) {
	return nil, nil
}
func (rejectingParametersProvider) Resolve(context.Context, string, map[string]any) (any, bool, error) {
	return nil, false, nil
}
func (rejectingParametersProvider) ValidateResource(_ context.Context, declared map[string]any) error {
	if len(declared) == 0 {
		return nil
	}
	return errors.New("agent.max_tokens: must be <= 8192, got out-of-bounds")
}

var _ port.ParametersProvider = validatingParametersProvider{}
var _ port.ParametersProvider = rejectingParametersProvider{}

// ---------- tests ----------

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

func TestAgentService_Get(t *testing.T) {
	svc, repo := newTestService(t)

	repo.On("Get", mock.Anything, "agent-1").Return(&domain.AgentConfig{
		ID: "agent-1", Name: "Foo", Type: domain.ReActAgent, LLMModel: "gpt-4",
	}, true, nil)

	dto, err := svc.Get(context.Background(), "agent-1")
	assert.NoError(t, err)
	assert.Equal(t, "agent-1", dto.ID)
	assert.Equal(t, "Foo", dto.Name)
}

func TestAgentService_Get_NotFound(t *testing.T) {
	svc, repo := newTestService(t)
	repo.On("Get", mock.Anything, "missing").Return((*domain.AgentConfig)(nil), false, nil)

	_, err := svc.Get(context.Background(), "missing")
	assert.ErrorIs(t, err, application.ErrNotFound)
}

func TestAgentService_SnapshotRevisionCapturesAuthorizedBindings(t *testing.T) {
	svc, repo := newTestService(t)
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
	svc, repo := newTestService(t)
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

func (r countingRevisionTenantResolver) Resolve(context.Context, string) (port.CapabilityGateway, bool) {
	return r.gateway, true
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
	svc, repo := newTestService(t)
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

func TestAgentService_ListIncludesSystemAssistant(t *testing.T) {
	svc, repo := newTestService(t)
	repo.On("GetAll", mock.Anything).Return([]*domain.AgentConfig{
		{ID: "ordinary-1", Name: "First", Type: domain.ReActAgent},
		{ID: domain.SystemAssistantID, Name: "Platform", Type: domain.ReActAgent,
			SystemKey: domain.SystemAssistantKey, IsSystem: true, ManagementMode: "platform"},
		{ID: "ordinary-2", Name: "Second", Type: domain.ReActAgent},
	}, nil)

	list, err := svc.List(context.Background())
	assert.NoError(t, err)
	assert.Len(t, list, 3)
	assert.Equal(t, []string{"ordinary-1", domain.SystemAssistantID, "ordinary-2"},
		[]string{list[0].ID, list[1].ID, list[2].ID})
}

type stubTenantModelValidator struct {
	mu         sync.Mutex
	err        error
	catalogErr error
	calls      []string
}

func (v *stubTenantModelValidator) ValidateTenantChatModel(_ context.Context, tenantID, model string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls = append(v.calls, tenantID+":"+model)
	return v.err
}

func (v *stubTenantModelValidator) ListTenantChatModels(context.Context, string) ([]string, error) {
	return []string{"qwen-plus", "qwen-plus-latest", "qwen-max"}, v.catalogErr
}

func TestAgentService_GetSystemAssistantSettings(t *testing.T) {
	_, repo := newTestService(t)
	validator := &stubTenantModelValidator{}
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:             application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantModelValidator: validator,
		TenantModelCatalog:   validator,
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
	_, repo := newTestService(t)
	validator := &stubTenantModelValidator{err: domain.ErrAssistantModelUnavailable}
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:             application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantModelValidator: validator,
		TenantModelCatalog:   validator,
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
	_, repo := newTestService(t)
	wantErr := errors.New("settings read failed")
	validator := &stubTenantModelValidator{err: wantErr}
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:             application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantModelValidator: validator,
		TenantModelCatalog:   validator,
		Logger:               zap.NewNop(),
	})
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	repo.On("GetSystemAssistant", ctx).Return(&domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, LLMModel: "qwen-plus",
	}, true, nil)

	_, err := svc.GetSystemAssistantSettings(ctx)
	assert.ErrorIs(t, err, wantErr)
}

func TestAgentService_UpdateSystemAssistantModelUsesAtomicReturnedConfig(t *testing.T) {
	_, repo := newTestService(t)
	validator := &stubTenantModelValidator{}
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:             application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantModelValidator: validator,
		TenantModelCatalog:   validator,
		Logger:               zap.NewNop(),
	})
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	repo.On("GetSystemAssistant", ctx).Return(&domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, LLMModel: "existing-model",
	}, true, nil)
	repo.On("UpdateSystemAssistantModel", ctx, "qwen-plus", "", false, 0, 0).Return(&domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, LLMModel: "qwen-plus",
	}, nil).Once()

	settings, err := svc.UpdateSystemAssistantModel(ctx, " qwen-plus ", "user-1")
	assert.NoError(t, err)
	assert.True(t, settings.Ready)
	assert.Equal(t, "qwen-plus", settings.Model)
	assert.Equal(t, []string{"tenant-1:qwen-plus"}, validator.calls)
	repo.AssertExpectations(t)
}

func TestAgentService_UpdateSystemAssistantModelDoesNotPersistWhenCatalogReadFails(t *testing.T) {
	_, repo := newTestService(t)
	wantErr := errors.New("catalog read failed")
	validator := &stubTenantModelValidator{catalogErr: wantErr}
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:             application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantModelValidator: validator, TenantModelCatalog: validator, Logger: zap.NewNop(),
	})
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")

	_, err := svc.UpdateSystemAssistantModel(ctx, "qwen-plus", "user-1")
	assert.ErrorIs(t, err, wantErr)
	repo.AssertNotCalled(t, "UpdateSystemAssistantModel", mock.Anything, mock.Anything)
}

func TestAgentService_UpdateSystemAssistantModelMarksUnexpectedReturnedModelNotReady(t *testing.T) {
	_, repo := newTestService(t)
	validator := &stubTenantModelValidator{}
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:             application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantModelValidator: validator,
		TenantModelCatalog:   validator,
		Logger:               zap.NewNop(),
	})
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	repo.On("GetSystemAssistant", ctx).Return(&domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, LLMModel: "existing-model",
	}, true, nil)
	repo.On("UpdateSystemAssistantModel", ctx, "qwen-plus", "", false, 0, 0).Return(&domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, LLMModel: "qwen-plus-latest",
	}, nil).Once()

	settings, err := svc.UpdateSystemAssistantModel(ctx, "qwen-plus", "user-1")
	assert.NoError(t, err)
	assert.Equal(t, "qwen-plus-latest", settings.Model)
	assert.False(t, settings.Ready)
	assert.Equal(t, []string{"tenant-1:qwen-plus"}, validator.calls)
}

func TestAgentService_UpdateSystemAssistantModelConcurrentCallsKeepAtomicResults(t *testing.T) {
	_, repo := newTestService(t)
	validator := &stubTenantModelValidator{}
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:             application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantModelValidator: validator,
		TenantModelCatalog:   validator,
		Logger:               zap.NewNop(),
	})
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	repo.On("GetSystemAssistant", ctx).Return(&domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, LLMModel: "existing-model",
	}, true, nil).Maybe()
	models := []string{"qwen-plus", "qwen-max"}
	for _, model := range models {
		repo.On("UpdateSystemAssistantModel", ctx, model, "", false, 0, 0).Return(&domain.AgentConfig{
			ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, LLMModel: model,
		}, nil).Once()
	}

	results := make(chan application.SystemAssistantSettings, len(models))
	errs := make(chan error, len(models))
	var wg sync.WaitGroup
	for _, model := range models {
		wg.Add(1)
		go func(model string) {
			defer wg.Done()
			settings, err := svc.UpdateSystemAssistantModel(ctx, model, "user-1")
			results <- settings
			errs <- err
		}(model)
	}
	wg.Wait()
	close(results)
	close(errs)

	seen := map[string]bool{}
	for settings := range results {
		seen[settings.Model] = settings.Ready
	}
	for err := range errs {
		assert.NoError(t, err)
	}
	assert.Equal(t, map[string]bool{"qwen-plus": true, "qwen-max": true}, seen)
	validator.mu.Lock()
	assert.Len(t, validator.calls, len(models))
	validator.mu.Unlock()
	repo.AssertExpectations(t)
}

func TestAgentService_UpdateSystemAssistantModelRejectsEmptyAndInvalid(t *testing.T) {
	_, repo := newTestService(t)
	validator := &stubTenantModelValidator{err: domain.ErrInvalidSystemAssistantModel}
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:             application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantModelValidator: validator,
		TenantModelCatalog:   validator,
		Logger:               zap.NewNop(),
	})
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")

	_, err := svc.UpdateSystemAssistantModel(ctx, " ", "user-1")
	assert.ErrorIs(t, err, domain.ErrInvalidSystemAssistantModel)
	_, err = svc.UpdateSystemAssistantModel(ctx, "unknown", "user-1")
	assert.ErrorIs(t, err, domain.ErrInvalidSystemAssistantModel)
	repo.AssertNotCalled(t, "UpdateSystemAssistantModel", mock.Anything, mock.Anything)
}

func TestAgentService_UpdateSystemAssistantModelPropagatesPersistenceFailure(t *testing.T) {
	_, repo := newTestService(t)
	validator := &stubTenantModelValidator{}
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:             application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantModelValidator: validator,
		TenantModelCatalog:   validator,
		Logger:               zap.NewNop(),
	})
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	wantErr := errors.New("write failed")
	repo.On("GetSystemAssistant", ctx).Return(&domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, LLMModel: "existing-model",
	}, true, nil)
	repo.On("UpdateSystemAssistantModel", ctx, "qwen-plus", "", false, 0, 0).Return((*domain.AgentConfig)(nil), wantErr)

	_, err := svc.UpdateSystemAssistantModel(ctx, "qwen-plus", "user-1")
	assert.ErrorIs(t, err, wantErr)
}

func TestAgentService_UpdateSystemAssistant_IgnoresName(t *testing.T) {
	svc, repo := newTestService(t)
	cfg := &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey,
	}
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	repo.On("Get", ctx, domain.SystemAssistantID).Return(cfg, true, nil)
	repo.On("UpdateSystemAssistantAll", ctx, "", "", false, 0, 0, 0).Return(cfg, nil)

	dto, err := svc.Update(ctx, domain.SystemAssistantID, application.UpdateAgentInput{
		Name: "ignored",
	})
	assert.NoError(t, err)
	assert.Equal(t, domain.SystemAssistantID, dto.ID)
	repo.AssertExpectations(t)
}

func TestAgentServicePlatformAssistantModelOnlyUpdatePreservesSystemBindings(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	cfg := &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, LLMModel: "old-model",
		MCPToolIDs: []string{"mcp:orders:get"},
	}
	repo.On("Get", ctx, domain.SystemAssistantID).Return(cfg, true, nil)
	repo.On("UpdateSystemAssistantAll", ctx, "new-model", "", false, 0, 0, 0).Return(&domain.AgentConfig{
		ID: cfg.ID, SystemKey: cfg.SystemKey, LLMModel: "new-model", MCPToolIDs: cfg.MCPToolIDs,
	}, nil)

	got, err := svc.Update(ctx, domain.SystemAssistantID, application.UpdateAgentInput{LLMModel: "new-model"})

	assert.NoError(t, err)
	assert.Equal(t, cfg.MCPToolIDs, got.MCPToolIDs)
	repo.AssertExpectations(t)
}

func TestAgentServicePlatformAssistantRejectsBindingRemovalByPreservingManagedTools(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	managedTools := []string{"mcp:orders:get"}
	cfg := &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, MCPToolIDs: managedTools,
	}
	repo.On("Get", ctx, domain.SystemAssistantID).Return(cfg, true, nil)
	repo.On("UpdateSystemAssistantAll", ctx, "", "", false, 0, 0, 0).Return(cfg, nil)

	got, err := svc.Update(ctx, domain.SystemAssistantID, application.UpdateAgentInput{MCPToolIDs: []string{}})

	assert.NoError(t, err)
	assert.Equal(t, managedTools, got.MCPToolIDs)
	repo.AssertExpectations(t)
}

// updateSystemAssistant max_tokens 通道测试:merge 前校验 → merge(0=保留现值)
// → merge 后复验,越界与历史非法值均 400 且不落库。
func TestAgentServiceUpdateSystemAssistantPersistsMaxTokens(t *testing.T) {
	svc, repo := newTestService(t)
	cfg := &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey,
	}
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	repo.On("Get", ctx, domain.SystemAssistantID).Return(cfg, true, nil)
	repo.On("UpdateSystemAssistantAll", ctx, "", "", false, 0, 0, 2048).Return(&domain.AgentConfig{
		ID: cfg.ID, SystemKey: cfg.SystemKey, MaxTokens: 2048,
	}, nil)

	dto, err := svc.Update(ctx, domain.SystemAssistantID, application.UpdateAgentInput{MaxTokens: 2048})

	assert.NoError(t, err)
	assert.Equal(t, 2048, dto.MaxTokens)
	repo.AssertExpectations(t)
}

func TestAgentServiceUpdateSystemAssistantZeroKeepsCurrentMaxTokens(t *testing.T) {
	svc, repo := newTestService(t)
	cfg := &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, MaxTokens: 2048,
	}
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	repo.On("Get", ctx, domain.SystemAssistantID).Return(cfg, true, nil)
	// 0 = 保留现值:merge 后传 cfg.MaxTokens(2048) 落库。
	repo.On("UpdateSystemAssistantAll", ctx, "", "", false, 0, 0, 2048).Return(cfg, nil)

	dto, err := svc.Update(ctx, domain.SystemAssistantID, application.UpdateAgentInput{})

	assert.NoError(t, err)
	assert.Equal(t, 2048, dto.MaxTokens)
	repo.AssertExpectations(t)
}

func TestAgentServiceUpdateSystemAssistantRejectsOutOfBoundsMaxTokens(t *testing.T) {
	svc, repo := newTestServiceWithProvider(t, rejectingParametersProvider{})
	cfg := &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey,
	}
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	repo.On("Get", ctx, domain.SystemAssistantID).Return(cfg, true, nil)

	_, err := svc.Update(ctx, domain.SystemAssistantID, application.UpdateAgentInput{MaxTokens: 999999})

	assert.ErrorIs(t, err, domain.ErrInvalidSamplingParameters)
	repo.AssertNotCalled(t, "UpdateSystemAssistantAll", mock.Anything, mock.Anything)
}

func TestAgentServiceUpdateSystemAssistantRevalidatesLegacyOutOfBoundsOnZero(t *testing.T) {
	// merge 后复验:存量 cfg.MaxTokens 越界时 PUT 0 不得静默回写历史非法值。
	svc, repo := newTestServiceWithProvider(t, rejectingParametersProvider{})
	cfg := &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, MaxTokens: 999999,
	}
	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	repo.On("Get", ctx, domain.SystemAssistantID).Return(cfg, true, nil)

	_, err := svc.Update(ctx, domain.SystemAssistantID, application.UpdateAgentInput{})

	assert.ErrorIs(t, err, domain.ErrInvalidSamplingParameters)
	repo.AssertNotCalled(t, "UpdateSystemAssistantAll", mock.Anything, mock.Anything)
}

func TestAgentService_Delete(t *testing.T) {
	svc, repo := newTestService(t)
	repo.On("Get", mock.Anything, "agent-1").Return(&domain.AgentConfig{ID: "agent-1"}, true, nil)
	repo.On("Remove", mock.Anything, "agent-1").Return(nil)

	err := svc.Delete(context.Background(), "tenant-1", "agent-1", "user-1")
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

	err := svc.Delete(ctx, "tenant-1", id, "user-1")

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

	err := svc.Delete(ctx, "tenant-1", "agent-1", "user-1")

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

	err := svc.Delete(ctx, "tenant-1", "missing", "user-1")

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
		MemoryCleaner: stubMemoryCleaner{err: wantErr}, TenantRoleResolver: stubTenantRole{role: "owner"},
		Logger: zap.NewNop(),
	})
	repo.On("Get", mock.Anything, "agent-1").Return(&domain.AgentConfig{ID: "agent-1"}, true, nil)

	err := svc.Delete(context.Background(), "tenant-1", "agent-1", "user-1")
	assert.ErrorIs(t, err, wantErr)
	repo.AssertNotCalled(t, "Remove", mock.Anything, mock.Anything)
}

func TestAgentService_DeleteReturnsChatCleanupErrorBeforeRemovingRegistry(t *testing.T) {
	repo := new(mockAgentRepo)
	wantErr := errors.New("chat cleanup failed")
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:  application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		ChatStore: stubChatRepo{err: wantErr}, TenantRoleResolver: stubTenantRole{role: "owner"},
		Logger: zap.NewNop(),
	})
	repo.On("Get", mock.Anything, "agent-1").Return(&domain.AgentConfig{ID: "agent-1"}, true, nil)

	err := svc.Delete(context.Background(), "tenant-1", "agent-1", "user-1")
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
	mcpProv.On("ToolsForServer", mock.Anything, "tenant-1", "srv1").Return([]port.ToolDefinition{
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
	mcpProv.On("ToolsForServer", mock.Anything, "tenant-1", "orders").Return([]port.ToolDefinition{{Name: "mcp:orders:get", CapabilityID: "get"}, {Name: "mcp:orders:delete", CapabilityID: "delete"}})
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
	mcpProv.On("ToolsForServer", mock.Anything, "tenant-1", "orders").Return([]port.ToolDefinition{{Name: "mcp:orders:mystery", CapabilityID: "mystery"}})
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

// stubTenantRole resolves every actor as a fixed role so ownership tests
// control authorization via the fake, not tenant membership.
type stubTenantRole struct{ role string }

func (s stubTenantRole) ResolveTenantRole(_ context.Context, _, _ string) (string, error) {
	return s.role, nil
}
