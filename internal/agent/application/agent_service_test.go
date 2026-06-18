package application_test

import (
	"context"
	"errors"
	"testing"

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

// satisfy interfaces at compile time
var (
	_ port.AgentRepo      = (*mockAgentRepo)(nil)
	_ port.TenantSettings = (*mockTenantSettings)(nil)
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
