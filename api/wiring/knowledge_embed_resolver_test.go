package wiring

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
)

type knowledgeModelRepo struct {
	models  []domain.Model
	listErr error
}

func (r *knowledgeModelRepo) List(_ context.Context, filter port.ModelFilter) ([]domain.Model, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
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
func (r *knowledgeModelRepo) Create(context.Context, *domain.Model) error { return nil }
func (r *knowledgeModelRepo) Get(context.Context, string) (*domain.Model, error) {
	return nil, nil
}
func (r *knowledgeModelRepo) Update(context.Context, *domain.Model, string, *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
func (r *knowledgeModelRepo) UpsertDiscovered(
	context.Context, string, []domain.Model,
) ([]domain.Model, error) {
	return nil, nil
}
func (r *knowledgeModelRepo) Delete(context.Context, string) error       { return nil }
func (r *knowledgeModelRepo) Toggle(context.Context, string, bool) error { return nil }
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

func (r *knowledgeProviderRepo) Create(context.Context, *domain.Provider) error { return nil }
func (r *knowledgeProviderRepo) Get(context.Context, string) (*domain.Provider, error) {
	provider := r.provider
	return &provider, nil
}
func (r *knowledgeProviderRepo) GetMeta(ctx context.Context, id string) (*domain.Provider, error) {
	provider := r.provider
	provider.APIKey = ""
	return &provider, nil
}
func (r *knowledgeProviderRepo) Update(context.Context, *domain.Provider, string, *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
func (r *knowledgeProviderRepo) List(context.Context) ([]domain.Provider, error) {
	return []domain.Provider{r.provider}, nil
}
func (r *knowledgeProviderRepo) Delete(context.Context, string) error { return nil }

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

	// pipeline resolver 只读租户显式配置：配置 managed-embedding → 解析出 client。
	configured := newTestTenantEmbeddingResolver(
		map[string]any{"memory_embedding_model": "managed-embedding"}, registry)
	require.NotNil(t, buildEmbedResolver(configured, zap.NewNop())(context.Background(), "tenant-1"))
	// 未配置租户 fail-closed → nil。
	unconfigured := newTestTenantEmbeddingResolver(nil, registry)
	require.Nil(t, buildEmbedResolver(unconfigured, zap.NewNop())(context.Background(), "tenant-1"))

	workspaceResolver := buildKnowledgeEmbedResolver(registry, zap.NewNop())
	require.NotNil(t, workspaceResolver(context.Background(), "tenant-1", "managed-embedding"))
	// 空模型/目录缺失均 fail-closed 返回 nil（无默认兜底）。
	require.Nil(t, workspaceResolver(context.Background(), "tenant-1", ""))
	require.Nil(t, workspaceResolver(context.Background(), "tenant-1", "not-managed"))
}

func TestKnowledgeEmbedResolverFailClosedOnCatalogueError(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	logger := zap.New(core)

	registry := llmgateway.NewModelRegistry(
		&knowledgeModelRepo{
			models: []domain.Model{{
				ID: "embedding-1", ProviderID: "provider-1", Name: "managed-embedding",
				Enabled: true, Capabilities: []domain.ModelCapability{domain.CapEmbedding},
			}},
			listErr: errors.New("catalogue db unavailable"),
		},
		&knowledgeProviderRepo{provider: domain.Provider{
			ID: "provider-1", Kind: domain.ProviderOpenAICompat, Enabled: true,
			BaseURL: "https://example.test/v1", APIKey: "test-key",
		}},
		nil,
		map[domain.ProviderKind]llmgateway.EmbedProtocol{domain.ProviderOpenAICompat: nil},
		time.Minute,
	)

	// 目录查询失败必须 fail-closed（nil）并输出 error 日志，不静默按不存在处理。
	require.Nil(t, buildKnowledgeEmbedResolver(registry, logger)(context.Background(), "tenant-1", "embedding-1"))

	entries := logs.FilterMessage("knowledge.embed.resolve_failed")
	require.Equal(t, 1, entries.Len())
	require.Equal(t, "embedding catalogue unavailable", entries.All()[0].ContextMap()["reason"])
	require.Equal(t, "tenant-1", entries.All()[0].ContextMap()["tenant_id"])
	require.Equal(t, "embedding-1", entries.All()[0].ContextMap()["model"])
}

func TestKnowledgeEmbedResolversReturnNilForEmptyCatalogue(t *testing.T) {
	registry := newKnowledgeRegistry(nil)

	require.Nil(t, buildEmbedResolver(newTestTenantEmbeddingResolver(nil, registry), zap.NewNop())(context.Background(), "tenant-1"))
	require.Nil(t, buildKnowledgeEmbedResolver(registry, zap.NewNop())(context.Background(), "tenant-1", ""))
}
