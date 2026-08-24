package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/zap"
)

// ---------- mocks ----------

type mockAgentRepo struct {
	mock.Mock
}

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
				_, _, _, _, err := svc.ExecuteStream(ctx, "agent-1", application.ExecRequest{
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
					repo, zap.NewNop(),
				),
				AgentRevisionResolver: preparationAgentRevisionResolver{},
				MCPTools:              preparationMCPTools{},
				MCPRevisionResolver: failingPreparationMCPRevisionResolver{
					err: dependencyErr,
				},
				TenantModelValidator: lenientModelValidator{},
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

// noAgentRevisionResolver 返回 found=false:执行不走 revision 分支,
// 避免 buildRevisionAgent 的额外依赖,聚焦 execution_id 恢复键语义。
type noAgentRevisionResolver struct{}

func (noAgentRevisionResolver) ResolveAgentRevision(context.Context, string, string, string) (port.AgentRevisionAssignment, bool, error) {
	return port.AgentRevisionAssignment{}, false, nil
}

// TestAgentService_ExecuteStream_ReusesProvidedExecutionID:断线续接协议要求
// 服务端沿用调用方恢复键。ExecuteStream 传 meta.ExecutionID 必须原样返回,
// 前端才能用同一 execution_id 重发续接;此前流式路径无条件新建 execution_id,
// 带 ID 重发也永不 resume(先决 bug B1)。
func TestAgentService_ExecuteStream_ReusesProvidedExecutionID(t *testing.T) {
	repo := new(mockAgentRepo)
	repo.On("Get", mock.Anything, "agent-1").Return(&domain.AgentConfig{
		ID: "agent-1", Name: "Resume Agent", Type: domain.ReActAgent,
		SystemPrompt: "resume prompt", LLMModel: "qwen-plus", MaxIterations: 3,
	}, true, nil).Once()
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:              application.NewRegistry(repo, zap.NewNop()),
		AgentRevisionResolver: noAgentRevisionResolver{},
		TenantModelValidator:  lenientModelValidator{},
	})

	execCtx, cancel, run, executionID, err := svc.ExecuteStream(
		context.Background(), "agent-1",
		application.ExecRequest{UserID: "user-1", Query: "continue the search"},
		application.ExecMeta{TenantID: "tenant-1", TraceID: "trace-1", ExecutionID: "provided-exec-1"},
		func(string) {},
	)
	require.NoError(t, err)
	require.NotNil(t, execCtx)
	require.NotNil(t, cancel)
	require.NotNil(t, run)
	require.Equal(t, "provided-exec-1", executionID)
	repo.AssertExpectations(t)
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

// lenientModelValidator accepts any non-empty model. D16 泛化后所有 agent 执行
// 都走模型校验，测试若不关注模型则注入此宽松 stub 绕过（空 model 仍 fail-closed）。
type lenientModelValidator struct{}

func (lenientModelValidator) ValidateTenantChatModel(context.Context, string, string) error {
	return nil
}

func newTestService(t *testing.T) (*application.AgentService, *mockAgentRepo) {
	t.Helper()
	return newTestServiceWithProvider(t, nil)
}

func newTestServiceWithProvider(t *testing.T, provider port.ParametersProvider) (*application.AgentService, *mockAgentRepo) {
	t.Helper()
	repo := new(mockAgentRepo)
	reg := application.NewRegistry(repo, zap.NewNop())
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:             reg,
		ParametersProvider:   provider,
		TenantModelValidator: lenientModelValidator{},
		Logger:               zap.NewNop(),
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
func (validatingParametersProvider) ValidateResourceKey(_ context.Context, _ string, _ any) error {
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
func (rejectingParametersProvider) ValidateResourceKey(_ context.Context, _ string, _ any) error {
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
	registry := application.NewRegistry(repo, zap.NewNop())
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
	// 全局系统提示词改由平台参数在 Execute 时解析，revision 不再携带后缀。
	assert.Equal(t, "", revision.GlobalSystemSuffix)
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

func TestAgentServiceManagedAssistantRevisionEntrypointsProceed(t *testing.T) {
	svc, repo := newTestService(t)
	repo.On("Get", mock.Anything, domain.SystemAssistantID).Return(&domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, LLMModel: "tenant-model",
		Name: "平台使用助手", Type: domain.ReActAgent, SystemPrompt: "你是平台助手",
		MaxIterations: 3,
	}, true, nil)

	revision, err := svc.SnapshotRevision(context.Background(), "tenant-1", domain.SystemAssistantID)
	assert.NoError(t, err)
	assert.Equal(t, domain.SystemAssistantID, revision.AgentID)
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

func TestAgentServiceCreateMaxIterationsValidation(t *testing.T) {
	tests := []struct {
		name          string
		maxIterations int
		wantErr       bool
	}{
		{name: "unset zero", maxIterations: 0, wantErr: false},
		{name: "lower boundary", maxIterations: 1, wantErr: false},
		{name: "upper boundary 90", maxIterations: 90, wantErr: false},
		{name: "exceeds 90", maxIterations: 91, wantErr: true},
		{name: "negative", maxIterations: -1, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := newTestService(t)
			repo.On("Register", mock.Anything, mock.Anything).Return(nil)
			_, err := svc.Create(context.Background(), application.CreateAgentInput{
				TenantID: "tenant-1", ActorID: "user-1", Name: "new",
				Type: string(domain.ReActAgent), LLMModel: "qwen-plus", MaxIterations: tc.maxIterations,
			})
			if tc.wantErr {
				require.ErrorIs(t, err, domain.ErrInvalidMaxIterations)
				repo.AssertNotCalled(t, "Register", mock.Anything, mock.Anything)
				return
			}
			require.NoError(t, err)
			repo.AssertExpectations(t)
		})
	}
}

// 普通 update（非系统助手）maxIterations 校验：91/-1 在 buildUpdateConfig
// 拒绝且不落库，90/0 通过并走 Registry.Update。

func TestAgentServiceUpdateMaxIterationsValidation(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name          string
		maxIterations int
		wantErr       bool
	}{
		{name: "unset zero keeps current", maxIterations: 0, wantErr: false},
		{name: "upper boundary 90", maxIterations: 90, wantErr: false},
		{name: "exceeds 90", maxIterations: 91, wantErr: true},
		{name: "negative", maxIterations: -1, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := newTestService(t)
			cfg := &domain.AgentConfig{ID: "agent-1", Name: "old", Type: domain.ReActAgent, CreatedBy: "user-1"}
			repo.On("Get", mock.Anything, "agent-1").Return(cfg, true, nil).Once()
			if !tc.wantErr {
				updated := &domain.AgentConfig{ID: "agent-1", Name: "old", Type: domain.ReActAgent,
					CreatedBy: "user-1", MaxIterations: tc.maxIterations}
				repo.On("Update", mock.Anything, mock.Anything).Return(nil).Once()
				repo.On("Get", mock.Anything, "agent-1").Return(updated, true, nil).Once()
			}
			_, err := svc.Update(ctx, "agent-1", application.UpdateAgentInput{
				ActorID: "user-1", MaxIterations: tc.maxIterations,
			})
			if tc.wantErr {
				require.ErrorIs(t, err, domain.ErrInvalidMaxIterations)
				repo.AssertNotCalled(t, "Update", mock.Anything, mock.Anything)
				return
			}
			require.NoError(t, err)
			repo.AssertExpectations(t)
		})
	}
}

// TestAgentServiceUpdateDelegateEnabledNilPreservesExisting 验证 delegate_enabled
// 的 *bool 缺省语义:Update 缺省(nil)必须继承已存值,显式 true/false 才覆盖。
// 存量默认关闭 + Update 全量列写,不在此合并会把未携带字段当显式 false 改写。

func TestAgentServiceUpdateDelegateEnabledNilPreservesExisting(t *testing.T) {
	ctx := context.Background()
	boolp := func(v bool) *bool { return &v }
	tests := []struct {
		name     string
		existing bool
		want     bool
		input    *bool
	}{
		{name: "nil preserves existing true", existing: true, want: true},
		{name: "nil preserves existing false", existing: false, want: false},
		{name: "explicit true overrides existing false", existing: false, want: true, input: boolp(true)},
		{name: "explicit false overrides existing true", existing: true, want: false, input: boolp(false)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := newTestService(t)
			existing := &domain.AgentConfig{ID: "agent-1", Name: "old", Type: domain.ReActAgent,
				CreatedBy: "user-1", DelegateEnabled: tc.existing}
			repo.On("Get", mock.Anything, "agent-1").Return(existing, true, nil).Once()
			repo.On("Update", mock.Anything, mock.Anything).Return(nil).Once().Run(func(args mock.Arguments) {
				cfg := args.Get(1).(*domain.AgentConfig)
				require.Equal(t, tc.want, cfg.DelegateEnabled,
					"持久化的 DelegateEnabled 必须为 %v", tc.want)
			})
			repo.On("Get", mock.Anything, "agent-1").Return(existing, true, nil).Once()
			_, err := svc.Update(ctx, "agent-1", application.UpdateAgentInput{
				ActorID: "user-1", DelegateEnabled: tc.input,
			})
			require.NoError(t, err)
			repo.AssertExpectations(t)
		})
	}
}

// updateSystemAssistant maxIterations 通道测试：只校验显式非零 in.MaxIterations
// （B2），0 = 保留原值，90 合法落库，91 越界 400 且不落库。

func TestAgentService_DeleteSystemAssistantGoesThroughOwnership(t *testing.T) {
	repo := new(mockAgentRepo)
	registry := application.NewRegistry(repo, zap.NewNop())
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
		ID: id, SystemKey: "stratum.platform_assistant",
	}, true, nil)
	repo.On("Remove", ctx, id).Return(nil).Maybe()

	// 等同化后平台助手删除走普通 ownership 路径：无角色解析器时与普通 agent
	// 一致 fail-closed 拒绝（ErrForbidden），绝不返回系统助手专属 sentinel。
	err := svc.Delete(ctx, "tenant-1", id, "user-1")

	assert.ErrorIs(t, err, domain.ErrForbidden)
	assert.Zero(t, memoryCalls)
	assert.Zero(t, chatCalls)
	repo.AssertNotCalled(t, "Remove", mock.Anything, mock.Anything)
}

func TestAgentService_DeletePropagatesIdentityLookupFailureBeforeCleanup(t *testing.T) {
	repo := new(mockAgentRepo)
	registry := application.NewRegistry(repo, zap.NewNop())
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
	registry := application.NewRegistry(repo, zap.NewNop())
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
		Registry:      application.NewRegistry(repo, zap.NewNop()),
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
		Registry:  application.NewRegistry(repo, zap.NewNop()),
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
		Registry: application.NewRegistry(new(mockAgentRepo), zap.NewNop()),
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
		Registry: application.NewRegistry(repo, zap.NewNop()),
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
		Registry: application.NewRegistry(repo, zap.NewNop()),
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
