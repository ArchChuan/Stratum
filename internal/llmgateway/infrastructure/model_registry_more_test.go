package infrastructure_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/stretchr/testify/require"
)

func registryFixture(modelRepo *mockModelRepo) *infrastructure.ModelRegistry {
	return infrastructure.NewModelRegistry(modelRepo, &mockProviderRepo{providers: map[string]*domain.Provider{}},
		map[domain.ProviderKind]infrastructure.ChatProtocol{},
		map[domain.ProviderKind]infrastructure.EmbedProtocol{},
		5*time.Minute)
}

func TestModelRegistry_GetChatModelContextWindow(t *testing.T) {
	repo := &mockModelRepo{models: []domain.Model{
		{Name: "qwen-turbo", ContextWindow: 8192, Enabled: true, Capabilities: []domain.ModelCapability{domain.CapChat}},
		{Name: "text-embed", ContextWindow: 2048, Enabled: true, Capabilities: []domain.ModelCapability{domain.CapEmbedding}},
	}}
	reg := registryFixture(repo)

	got, err := reg.GetChatModelContextWindow(context.Background(), "qwen-turbo")
	require.NoError(t, err)
	require.Equal(t, 8192, got)
}

func TestModelRegistry_GetChatModelContextWindow_notFound(t *testing.T) {
	reg := registryFixture(&mockModelRepo{models: []domain.Model{
		{Name: "text-embed", ContextWindow: 2048, Capabilities: []domain.ModelCapability{domain.CapEmbedding}},
	}})

	got, err := reg.GetChatModelContextWindow(context.Background(), "unknown")
	require.NoError(t, err)
	require.Equal(t, 0, got)
}

func TestModelRegistry_GetChatModelContextWindow_repoError(t *testing.T) {
	reg := registryFixture(&mockModelRepo{err: errors.New("db down")})

	_, err := reg.GetChatModelContextWindow(context.Background(), "qwen-turbo")
	require.ErrorContains(t, err, "get context window")
}

func TestModelRegistry_ListChatModelsAndEmbeddingEmpty(t *testing.T) {
	reg := registryFixture(&mockModelRepo{})

	require.Empty(t, reg.ListChatModels())
	require.Empty(t, reg.ListEmbeddingModels())
	require.NotNil(t, reg.ListChatModels())      // never nil
	require.NotNil(t, reg.ListEmbeddingModels()) // never nil
}
