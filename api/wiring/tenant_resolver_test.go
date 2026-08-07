package wiring

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	llmport "github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
)

type resolverModelRepo struct {
	models []llmdomain.Model
	err    error
}

func (r *resolverModelRepo) Create(context.Context, string, *llmdomain.Model) error { return r.err }
func (r *resolverModelRepo) Get(context.Context, string, string) (*llmdomain.Model, error) {
	return nil, r.err
}
func (r *resolverModelRepo) List(_ context.Context, _ string, filter llmport.ModelFilter) ([]llmdomain.Model, error) {
	if r.err != nil {
		return nil, r.err
	}
	models := make([]llmdomain.Model, 0, len(r.models))
	for _, model := range r.models {
		if filter.Enabled != nil && model.Enabled != *filter.Enabled {
			continue
		}
		if filter.Capability != "" && !modelHasCapability(model, filter.Capability) {
			continue
		}
		models = append(models, model)
	}
	return models, nil
}
func (r *resolverModelRepo) Update(context.Context, string, *llmdomain.Model) error { return r.err }
func (r *resolverModelRepo) UpsertDiscovered(
	context.Context, string, string, []llmdomain.Model,
) ([]llmdomain.Model, error) {
	return nil, r.err
}
func (r *resolverModelRepo) Delete(context.Context, string, string) error       { return r.err }
func (r *resolverModelRepo) Toggle(context.Context, string, string, bool) error { return r.err }

func modelHasCapability(model llmdomain.Model, capability llmdomain.ModelCapability) bool {
	for _, candidate := range model.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

type resolverProviderRepo struct {
	providers map[string]*llmdomain.Provider
}

func (r *resolverProviderRepo) Create(context.Context, string, *llmdomain.Provider) error { return nil }
func (r *resolverProviderRepo) Get(_ context.Context, _, id string) (*llmdomain.Provider, error) {
	provider, ok := r.providers[id]
	if !ok {
		return nil, errors.New("provider not found")
	}
	return provider, nil
}
func (r *resolverProviderRepo) GetMeta(ctx context.Context, tenantID, id string) (*llmdomain.Provider, error) {
	provider, err := r.Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	cp := *provider
	cp.APIKey = ""
	return &cp, nil
}
func (r *resolverProviderRepo) List(context.Context, string) ([]llmdomain.Provider, error) {
	return nil, nil
}
func (r *resolverProviderRepo) Update(context.Context, string, *llmdomain.Provider) error { return nil }
func (r *resolverProviderRepo) Delete(context.Context, string, string) error              { return nil }

func newResolverRegistry(models []llmdomain.Model, providers map[string]*llmdomain.Provider) *llmgateway.ModelRegistry {
	return llmgateway.NewModelRegistry(
		&resolverModelRepo{models: models},
		&resolverProviderRepo{providers: providers},
		map[llmdomain.ProviderKind]llmgateway.ChatProtocol{llmdomain.ProviderOpenAICompat: nil},
		map[llmdomain.ProviderKind]llmgateway.EmbedProtocol{llmdomain.ProviderOpenAICompat: nil},
		time.Minute,
	)
}

func TestNewTenantCapabilityResolverPreservesNilRegistryBehavior(t *testing.T) {
	resolver := newTenantCapabilityResolver(nil, nil, zap.NewNop()).(*tenantCapabilityResolver)

	client, err := resolver.ResolveWorkerLLM(context.Background(), "tenant-1")
	require.Nil(t, client)
	require.ErrorContains(t, err, "registry unavailable")
	_, err = resolver.DiagnosticModelStatus(context.Background(), "tenant-1")
	require.ErrorContains(t, err, "registry unavailable")
}

func TestTenantCapabilityResolverUsesRegistryForDiagnosticsAndValidation(t *testing.T) {
	models := []llmdomain.Model{{
		ID: "model-1", ProviderID: "provider-1", Name: "chat-model", Enabled: true,
		Capabilities: []llmdomain.ModelCapability{llmdomain.CapChat},
	}}
	providers := map[string]*llmdomain.Provider{
		"provider-1": {ID: "provider-1", Kind: llmdomain.ProviderOpenAICompat, Enabled: true},
	}
	resolver := &tenantCapabilityResolver{registry: newResolverRegistry(models, providers), logger: zap.NewNop()}

	status, err := resolver.DiagnosticModelStatus(context.Background(), "tenant-1")
	require.NoError(t, err)
	require.True(t, status.Configured)
	require.NoError(t, resolver.ValidateTenantChatModel(context.Background(), "tenant-1", "chat-model"))
	require.ErrorIs(t,
		resolver.ValidateTenantChatModel(context.Background(), "tenant-1", "unknown"),
		agentdomain.ErrInvalidSystemAssistantModel,
	)
	require.Equal(t, []string{"chat-model"}, mustListTenantChatModels(t, resolver))
}

func TestTenantCapabilityResolverReportsEmptyWhenNoEligibleModelExists(t *testing.T) {
	models := []llmdomain.Model{{
		ID: "model-1", ProviderID: "provider-1", Name: "chat-model", Enabled: true,
		Capabilities: []llmdomain.ModelCapability{llmdomain.CapChat},
	}}
	providers := map[string]*llmdomain.Provider{
		"provider-1": {ID: "provider-1", Kind: llmdomain.ProviderAnthropic, Enabled: true},
	}
	resolver := &tenantCapabilityResolver{registry: newResolverRegistry(models, providers), logger: zap.NewNop()}

	status, err := resolver.DiagnosticModelStatus(context.Background(), "tenant-1")
	require.NoError(t, err)
	require.False(t, status.Configured)
	require.Empty(t, mustListTenantChatModels(t, resolver))
}

func TestTenantCapabilityResolverPropagatesRegistryFailure(t *testing.T) {
	registry := llmgateway.NewModelRegistry(
		&resolverModelRepo{err: errors.New("database unavailable")},
		&resolverProviderRepo{}, nil, nil, time.Minute,
	)
	resolver := &tenantCapabilityResolver{registry: registry, logger: zap.NewNop()}

	_, err := resolver.DiagnosticModelStatus(context.Background(), "tenant-1")
	require.ErrorContains(t, err, "database unavailable")
	_, err = resolver.ResolveWorkerLLM(context.Background(), "tenant-1")
	require.ErrorContains(t, err, "database unavailable")
}

func mustListTenantChatModels(t *testing.T, resolver *tenantCapabilityResolver) []string {
	t.Helper()
	models, err := resolver.ListTenantChatModels(context.Background(), "tenant-1")
	require.NoError(t, err)
	return models
}
