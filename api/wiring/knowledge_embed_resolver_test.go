package wiring

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
)

type knowledgeModelRepo struct {
	models []domain.Model
}

func (r *knowledgeModelRepo) List(_ context.Context, _ string, filter port.ModelFilter) ([]domain.Model, error) {
	models := make([]domain.Model, 0, len(r.models))
	for _, model := range r.models {
		if filter.Enabled != nil && model.Enabled != *filter.Enabled {
			continue
		}
		if filter.Capability != "" && !modelHasCapabilityForKnowledge(model, filter.Capability) {
			continue
		}
		models = append(models, model)
	}
	return models, nil
}
func (r *knowledgeModelRepo) Create(context.Context, string, *domain.Model) error { return nil }
func (r *knowledgeModelRepo) Get(context.Context, string, string) (*domain.Model, error) {
	return nil, nil
}
func (r *knowledgeModelRepo) Update(context.Context, string, *domain.Model) error { return nil }
func (r *knowledgeModelRepo) UpsertDiscovered(
	context.Context, string, string, []domain.Model,
) ([]domain.Model, error) {
	return nil, nil
}
func (r *knowledgeModelRepo) Delete(context.Context, string, string) error       { return nil }
func (r *knowledgeModelRepo) Toggle(context.Context, string, string, bool) error { return nil }

func modelHasCapabilityForKnowledge(model domain.Model, capability domain.ModelCapability) bool {
	for _, candidate := range model.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

type knowledgeProviderRepo struct {
	provider domain.Provider
}

func (r *knowledgeProviderRepo) Create(context.Context, string, *domain.Provider) error { return nil }
func (r *knowledgeProviderRepo) Get(context.Context, string, string) (*domain.Provider, error) {
	provider := r.provider
	return &provider, nil
}
func (r *knowledgeProviderRepo) GetMeta(ctx context.Context, tenantID, id string) (*domain.Provider, error) {
	provider := r.provider
	provider.APIKey = ""
	return &provider, nil
}
func (r *knowledgeProviderRepo) Update(context.Context, string, *domain.Provider) error { return nil }
func (r *knowledgeProviderRepo) List(context.Context, string) ([]domain.Provider, error) {
	return []domain.Provider{r.provider}, nil
}
func (r *knowledgeProviderRepo) Delete(context.Context, string, string) error { return nil }

func newKnowledgeRegistry(models []domain.Model) *llmgateway.ModelRegistry {
	return llmgateway.NewModelRegistry(
		&knowledgeModelRepo{models: models},
		&knowledgeProviderRepo{provider: domain.Provider{
			ID: "provider-1", Kind: domain.ProviderOpenAICompat, Enabled: true,
			BaseURL: "https://example.test/v1", APIKey: "test-key",
		}},
		nil,
		map[domain.ProviderKind]llmgateway.EmbedProtocol{domain.ProviderOpenAICompat: nil},
		time.Minute,
	)
}

func TestKnowledgeEmbedResolversUseManagedModels(t *testing.T) {
	registry := newKnowledgeRegistry([]domain.Model{{
		ID: "embedding-1", ProviderID: "provider-1", Name: "managed-embedding", Enabled: true,
		Capabilities: []domain.ModelCapability{domain.CapEmbedding},
	}})

	pipelineResolver := buildEmbedResolver(registry, zap.NewNop())
	require.NotNil(t, pipelineResolver(context.Background(), "tenant-1"))

	workspaceResolver := buildKnowledgeEmbedResolver(registry, zap.NewNop())
	require.NotNil(t, workspaceResolver(context.Background(), "tenant-1", "managed-embedding"))
	require.NotNil(t, workspaceResolver(context.Background(), "tenant-1", ""))
	require.Nil(t, workspaceResolver(context.Background(), "tenant-1", "not-managed"))
}

func TestKnowledgeEmbedResolversReturnNilForEmptyCatalogue(t *testing.T) {
	registry := newKnowledgeRegistry(nil)

	require.Nil(t, buildEmbedResolver(registry, zap.NewNop())(context.Background(), "tenant-1"))
	require.Nil(t, buildKnowledgeEmbedResolver(registry, zap.NewNop())(context.Background(), "tenant-1", ""))
}
