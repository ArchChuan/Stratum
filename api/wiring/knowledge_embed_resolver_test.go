package wiring

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockModelRepo implements port.ModelRepository for tests.
type mockModelRepo struct{}

func (m *mockModelRepo) List(_ context.Context, _ string, _ port.ModelFilter) ([]domain.Model, error) {
	return nil, nil
}
func (m *mockModelRepo) Create(_ context.Context, _ string, _ *domain.Model) error { return nil }
func (m *mockModelRepo) Get(_ context.Context, _, _ string) (*domain.Model, error) { return nil, nil }
func (m *mockModelRepo) Update(_ context.Context, _ string, _ *domain.Model) error { return nil }
func (m *mockModelRepo) UpsertDiscovered(_ context.Context, _, _ string, _ []domain.Model) ([]domain.Model, error) {
	return nil, nil
}
func (m *mockModelRepo) Delete(_ context.Context, _, _ string) error         { return nil }
func (m *mockModelRepo) Toggle(_ context.Context, _, _ string, _ bool) error { return nil }

// mockProviderRepo implements port.ProviderRepository for tests.
type mockProviderRepo struct{}

func (m *mockProviderRepo) Create(_ context.Context, _ string, _ *domain.Provider) error { return nil }
func (m *mockProviderRepo) Get(_ context.Context, _, _ string) (*domain.Provider, error) {
	return &domain.Provider{
		Kind: domain.ProviderOpenAICompat,
	}, nil
}
func (m *mockProviderRepo) Update(_ context.Context, _ string, _ *domain.Provider) error { return nil }
func (m *mockProviderRepo) List(_ context.Context, _ string) ([]domain.Provider, error) {
	return nil, nil
}
func (m *mockProviderRepo) Delete(_ context.Context, _, _ string) error { return nil }

func newTestRegistry() *llmgateway.ModelRegistry {
	chatProtos := map[domain.ProviderKind]llmgateway.ChatProtocol{}
	embedProtos := map[domain.ProviderKind]llmgateway.EmbedProtocol{}
	return llmgateway.NewModelRegistry(
		&mockModelRepo{},
		&mockProviderRepo{},
		chatProtos,
		embedProtos,
		time.Minute,
	)
}

func TestKnowledgeEmbedResolverBuilds(t *testing.T) {
	t.Skip("TODO: adapt for ModelRegistry-based resolver with mock provider config")

	db := tenantSettingsQueryFunc(func(_ context.Context, _ string, _ ...any) pgx.Row {
		return tenantSettingsRow{settings: []byte(`{"embed_model":"text-embedding-v3"}`)}
	})
	registry := newTestRegistry()
	resolver := buildKnowledgeEmbedResolver(db, registry, zap.NewNop())
	embedder := resolver(context.Background(), "tenant-1", "text-embedding-v3")
	require.Nil(t, embedder) // empty registry returns nil
}

func TestKnowledgeEmbedResolverUsesConfiguredQwenBaseURL(t *testing.T) {
	t.Skip("TODO: adapt for ModelRegistry-based resolver")
}

func TestKnowledgeEmbedResolverKeepsConfiguredBaseURLAfterPipelineCacheLoad(t *testing.T) {
	t.Skip("TODO: adapt for ModelRegistry-based resolver")
}
