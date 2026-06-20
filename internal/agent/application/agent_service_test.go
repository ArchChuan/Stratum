package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
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

type mockExecStore struct{ mock.Mock }

func (m *mockExecStore) Insert(ctx context.Context, r application.ExecutionRecord) error {
	return m.Called(ctx, r).Error(0)
}

func (m *mockExecStore) List(ctx context.Context, opts application.ListOptions) ([]application.ExecutionRecord, int64, error) {
	args := m.Called(ctx, opts)
	rows, _ := args.Get(0).([]application.ExecutionRecord)
	return rows, args.Get(1).(int64), args.Error(2)
}

// satisfy interfaces at compile time
var (
	_ port.AgentRepo             = (*mockAgentRepo)(nil)
	_ port.TenantSettings        = (*mockTenantSettings)(nil)
	_ port.SkillLookup           = (*mockSkillLookup)(nil)
	_ port.MCPToolProvider       = (*mockMCPTools)(nil)
	_ application.ExecutionStore = (*mockExecStore)(nil)
)

// ---------- helpers ----------

func newTestService(t *testing.T) (*application.AgentService, *mockAgentRepo, *mockTenantSettings) {
	t.Helper()
	repo := new(mockAgentRepo)
	ts := new(mockTenantSettings)
	reg := application.NewRegistry(repo, zap.NewNop())
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
	assert.Equal(t, "cot", list[1].Type)
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
	repo.On("Remove", mock.Anything, "agent-1").Return(nil)

	err := svc.Delete(context.Background(), "agent-1")
	assert.NoError(t, err)
	repo.AssertExpectations(t)
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
	mcpProv.On("ToolsForServer", mock.Anything, "srv1").Return([]port.ToolDefinition{
		{Name: "mcp:srv1:search", Description: "web search"},
	})
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry: application.NewRegistry(repo, zap.NewNop()),
		MCPTools: mcpProv,
		Logger:   zap.NewNop(),
	})
	tools, _ := svc.BuildExtraToolsForTest(context.Background(), "tenant-1", []string{"srv1"}, nil)
	assert.Len(t, tools, 1)
	assert.Equal(t, "mcp:srv1:search", tools[0].Name)
	mcpProv.AssertExpectations(t)
}

func TestAgentService_BuildExtraTools_SkillFallback_NoLookup(t *testing.T) {
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry: application.NewRegistry(new(mockAgentRepo), zap.NewNop()),
		Logger:   zap.NewNop(),
	})
	tools, _ := svc.BuildExtraToolsForTest(context.Background(), "tenant-1", nil, []string{"my-skill"})
	assert.Len(t, tools, 1)
	assert.Equal(t, "tenant_tenant-1_my-skill", tools[0].Name)
	assert.Equal(t, "my-skill: my-skill", tools[0].Description)
	assert.NotNil(t, tools[0].InputSchema)
}

func TestAgentService_BuildExtraTools_SkillLookupOverridesDescription(t *testing.T) {
	skillLookup := new(mockSkillLookup)
	skillLookup.On("LookupSkill", mock.Anything, "tenant-1", "skill-id").
		Return("PrettyName", "tool description", nil)

	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:    application.NewRegistry(new(mockAgentRepo), zap.NewNop()),
		SkillLookup: skillLookup,
		Logger:      zap.NewNop(),
	})
	tools, _ := svc.BuildExtraToolsForTest(context.Background(), "tenant-1", nil, []string{"skill-id"})
	assert.Len(t, tools, 1)
	assert.Equal(t, "tenant_tenant-1_PrettyName", tools[0].Name)
	assert.Equal(t, "PrettyName: tool description", tools[0].Description)
}

func TestAgentService_BuildExtraTools_SkillLookupErrorFallsBack(t *testing.T) {
	skillLookup := new(mockSkillLookup)
	skillLookup.On("LookupSkill", mock.Anything, "tenant-1", "skill-id").
		Return("", "", errors.New("db down"))

	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:    application.NewRegistry(new(mockAgentRepo), zap.NewNop()),
		SkillLookup: skillLookup,
		Logger:      zap.NewNop(),
	})
	tools, _ := svc.BuildExtraToolsForTest(context.Background(), "tenant-1", nil, []string{"skill-id"})
	assert.Len(t, tools, 1)
	assert.Equal(t, "skill-id: skill-id", tools[0].Description)
}

func TestAgentService_RecordExecution_Success(t *testing.T) {
	store := new(mockExecStore)
	done := make(chan struct{})
	store.On("Insert", mock.Anything, mock.MatchedBy(func(r application.ExecutionRecord) bool {
		return r.AgentID == "a1" && r.Status == "success" && r.OutputPreview == "hello" && r.TotalTokens == 42
	})).Return(nil).Run(func(_ mock.Arguments) { close(done) })

	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:  application.NewRegistry(new(mockAgentRepo), zap.NewNop()),
		ExecStore: store,
		Logger:    zap.NewNop(),
	})
	res := &application.AgentResult{Output: "hello", TokensUsed: 42}
	svc.RecordExecutionForTest(context.Background(), "a1", "u1", "Agent", "query", res, nil, 100)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Insert not called within 2s")
	}
	store.AssertExpectations(t)
}

func TestAgentService_RecordExecution_Error(t *testing.T) {
	store := new(mockExecStore)
	done := make(chan struct{})
	store.On("Insert", mock.Anything, mock.MatchedBy(func(r application.ExecutionRecord) bool {
		return r.Status == "error" && r.ErrorMessage == "boom"
	})).Return(nil).Run(func(_ mock.Arguments) { close(done) })

	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:  application.NewRegistry(new(mockAgentRepo), zap.NewNop()),
		ExecStore: store,
		Logger:    zap.NewNop(),
	})
	svc.RecordExecutionForTest(context.Background(), "a1", "u1", "Agent", "query", nil, errors.New("boom"), 50)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Insert not called within 2s")
	}
}

func TestAgentService_RecordExecution_NilStore_NoOp(t *testing.T) {
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry: application.NewRegistry(new(mockAgentRepo), zap.NewNop()),
		Logger:   zap.NewNop(),
	})
	svc.RecordExecutionForTest(context.Background(), "a1", "u1", "Agent", "q", nil, nil, 0)
	// no panic, no goroutine — pass
}

func TestAgentService_ListExecutions_NilStore_ReturnsEmpty(t *testing.T) {
	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry: application.NewRegistry(new(mockAgentRepo), zap.NewNop()),
		Logger:   zap.NewNop(),
	})
	rows, total, err := svc.ListExecutions(context.Background(), 1, 20)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, rows)
}

func TestAgentService_ListExecutions_Maps(t *testing.T) {
	store := new(mockExecStore)
	now := time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC)
	store.On("List", mock.Anything, application.ListOptions{Page: 1, PageSize: 20}).
		Return([]application.ExecutionRecord{
			{ID: "r1", AgentID: "a1", Status: "success", CreatedAt: now},
		}, int64(1), nil)

	svc := application.NewAgentService(application.AgentServiceDeps{
		Registry:  application.NewRegistry(new(mockAgentRepo), zap.NewNop()),
		ExecStore: store,
		Logger:    zap.NewNop(),
	})
	rows, total, err := svc.ListExecutions(context.Background(), 1, 20)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, rows, 1)
	assert.Equal(t, "r1", rows[0].ID)
	assert.Equal(t, now.Format(time.RFC3339), rows[0].CreatedAt)
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
