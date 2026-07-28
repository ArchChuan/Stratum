package infrastructure

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
)

type resolvedEntry struct {
	config   ProviderConfig
	provider domain.Provider
	expires  time.Time
}

// ModelRegistry wraps a ModelRepository and ProviderRepository with an
// in-memory LRU cache and resolves model names to provider config + protocol.
type ModelRegistry struct {
	modelRepo    port.ModelRepository
	providerRepo port.ProviderRepository
	chatProtos   map[domain.ProviderKind]ChatProtocol
	embedProtos  map[domain.ProviderKind]EmbedProtocol
	cacheTTL     time.Duration

	mu    sync.RWMutex
	cache map[string]map[string]*resolvedEntry // tenantID -> modelName -> entry
}

// NewModelRegistry returns a new ModelRegistry.
func NewModelRegistry(
	modelRepo port.ModelRepository,
	providerRepo port.ProviderRepository,
	chatProtos map[domain.ProviderKind]ChatProtocol,
	embedProtos map[domain.ProviderKind]EmbedProtocol,
	cacheTTL time.Duration,
) *ModelRegistry {
	return &ModelRegistry{
		modelRepo:    modelRepo,
		providerRepo: providerRepo,
		chatProtos:   chatProtos,
		embedProtos:  embedProtos,
		cacheTTL:     cacheTTL,
		cache:        make(map[string]map[string]*resolvedEntry),
	}
}

// Resolve looks up modelName for the given tenant and returns the provider
// configuration and chat protocol. Results are cached per tenant per model.
func (r *ModelRegistry) Resolve(ctx context.Context, tenantID, modelName string) (ProviderConfig, ChatProtocol, error) {
	if e := r.cacheGet(tenantID, modelName); e != nil {
		proto, ok := r.chatProtos[e.provider.Kind]
		if !ok {
			return ProviderConfig{}, nil, fmt.Errorf("model registry: no chat protocol for provider kind %q", e.provider.Kind)
		}
		return e.config, proto, nil
	}
	return r.resolveFromDB(ctx, tenantID, modelName)
}

// ResolveEmbedding looks up modelName for the given tenant and returns the
// provider configuration and embedding protocol. Results are cached.
func (r *ModelRegistry) ResolveEmbedding(ctx context.Context, tenantID, modelName string) (ProviderConfig, EmbedProtocol, error) {
	if e := r.cacheGet(tenantID, modelName); e != nil {
		proto, ok := r.embedProtos[e.provider.Kind]
		if !ok {
			return ProviderConfig{}, nil, fmt.Errorf("model registry: no embed protocol for provider kind %q", e.provider.Kind)
		}
		return e.config, proto, nil
	}
	return r.resolveEmbedFromDB(ctx, tenantID, modelName)
}

// resolveFromDB performs a cache-miss resolution for a chat model.
func (r *ModelRegistry) resolveFromDB(ctx context.Context, tenantID, modelName string) (ProviderConfig, ChatProtocol, error) {
	enabled := true
	models, err := r.modelRepo.List(ctx, tenantID, port.ModelFilter{Enabled: &enabled})
	if err != nil {
		return ProviderConfig{}, nil, fmt.Errorf("model registry: list models: %w", err)
	}
	for _, m := range models {
		if m.Name == modelName {
			provider, err := r.providerRepo.Get(ctx, tenantID, m.ProviderID)
			if err != nil {
				return ProviderConfig{}, nil, fmt.Errorf("model registry: get provider: %w", err)
			}
			cfg := ProviderConfig{
				Name:        provider.Name,
				BaseURL:     provider.BaseURL,
				APIKey:      provider.APIKey,
				HealthModel: provider.DefaultModel,
				Models:      []string{m.Name},
			}
			r.cacheSet(tenantID, modelName, cfg, *provider)
			proto, ok := r.chatProtos[provider.Kind]
			if !ok {
				return ProviderConfig{}, nil, fmt.Errorf("model registry: no chat protocol for %q", provider.Kind)
			}
			return cfg, proto, nil
		}
	}
	return ProviderConfig{}, nil, fmt.Errorf("model registry: model %q not found for tenant %s", modelName, tenantID)
}

// resolveEmbedFromDB performs a cache-miss resolution for an embedding model.
func (r *ModelRegistry) resolveEmbedFromDB(ctx context.Context, tenantID, modelName string) (ProviderConfig, EmbedProtocol, error) {
	enabled := true
	models, err := r.modelRepo.List(ctx, tenantID, port.ModelFilter{Enabled: &enabled})
	if err != nil {
		return ProviderConfig{}, nil, fmt.Errorf("model registry: list models: %w", err)
	}
	for _, m := range models {
		if m.Name == modelName {
			provider, err := r.providerRepo.Get(ctx, tenantID, m.ProviderID)
			if err != nil {
				return ProviderConfig{}, nil, fmt.Errorf("model registry: get provider: %w", err)
			}
			cfg := ProviderConfig{
				Name:        provider.Name,
				BaseURL:     provider.BaseURL,
				APIKey:      provider.APIKey,
				HealthModel: provider.DefaultModel,
				Models:      []string{m.Name},
			}
			r.cacheSet(tenantID, modelName, cfg, *provider)
			proto, ok := r.embedProtos[provider.Kind]
			if !ok {
				return ProviderConfig{}, nil, fmt.Errorf("model registry: no embed protocol for %q", provider.Kind)
			}
			return cfg, proto, nil
		}
	}
	return ProviderConfig{}, nil, fmt.Errorf("model registry: embedding model %q not found for tenant %s", modelName, tenantID)
}

// ListChatModels returns sorted enabled chat model names for a tenant.
func (r *ModelRegistry) ListChatModels(ctx context.Context, tenantID string) ([]string, error) {
	enabled := true
	models, err := r.modelRepo.List(ctx, tenantID, port.ModelFilter{
		Enabled:    &enabled,
		Capability: domain.CapChat,
	})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(models))
	for _, m := range models {
		names = append(names, m.Name)
	}
	sort.Strings(names)
	return names, nil
}

// ListEmbeddingModels returns sorted enabled embedding model names for a tenant.
func (r *ModelRegistry) ListEmbeddingModels(ctx context.Context, tenantID string) ([]string, error) {
	enabled := true
	models, err := r.modelRepo.List(ctx, tenantID, port.ModelFilter{
		Enabled:    &enabled,
		Capability: domain.CapEmbedding,
	})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(models))
	for _, m := range models {
		names = append(names, m.Name)
	}
	sort.Strings(names)
	return names, nil
}

// WarmTenant pre-warms the cache by listing enabled models for a tenant.
// The actual caching happens on subsequent Resolve / ResolveEmbedding calls.
func (r *ModelRegistry) WarmTenant(ctx context.Context, tenantID string) error {
	enabled := true
	_, err := r.modelRepo.List(ctx, tenantID, port.ModelFilter{Enabled: &enabled})
	return err
}

// Invalidate removes all cached entries for a tenant.
func (r *ModelRegistry) Invalidate(tenantID string) {
	r.mu.Lock()
	delete(r.cache, tenantID)
	r.mu.Unlock()
}

// cacheGet returns a non-expired cached entry, or nil.
func (r *ModelRegistry) cacheGet(tenantID, modelName string) *resolvedEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tenantCache, ok := r.cache[tenantID]
	if !ok {
		return nil
	}
	e, ok := tenantCache[modelName]
	if !ok || time.Now().After(e.expires) {
		return nil
	}
	return e
}

// cacheSet stores an entry in the cache.
func (r *ModelRegistry) cacheSet(tenantID, modelName string, cfg ProviderConfig, provider domain.Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache[tenantID] == nil {
		r.cache[tenantID] = make(map[string]*resolvedEntry)
	}
	r.cache[tenantID][modelName] = &resolvedEntry{
		config:   cfg,
		provider: provider,
		expires:  time.Now().Add(r.cacheTTL),
	}
}
