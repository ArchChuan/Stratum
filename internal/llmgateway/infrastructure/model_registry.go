package infrastructure

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
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
	cache map[string]map[string]*resolvedEntry // tenantID -> "chat:"|"embed:"+modelName -> entry
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
	cacheKey := "chat:" + modelName
	if e := r.cacheGet(tenantID, cacheKey); e != nil {
		proto, ok := r.chatProtos[e.provider.Kind]
		if !ok {
			return ProviderConfig{}, nil, fmt.Errorf("model registry: no chat protocol for provider kind %q", e.provider.Kind)
		}
		return e.config, proto, nil
	}
	cfg, provider, err := r.resolveModelFromDB(ctx, tenantID, modelName, cacheKey, domain.CapChat)
	if err != nil {
		return ProviderConfig{}, nil, err
	}
	proto, ok := r.chatProtos[provider.Kind]
	if !ok {
		return ProviderConfig{}, nil, fmt.Errorf("model registry: no chat protocol for %q", provider.Kind)
	}
	return cfg, proto, nil
}

// ResolveEmbedding looks up modelName for the given tenant and returns the
// provider configuration and embedding protocol. Results are cached.
func (r *ModelRegistry) ResolveEmbedding(ctx context.Context, tenantID, modelName string) (ProviderConfig, EmbedProtocol, error) {
	cacheKey := "embed:" + modelName
	if e := r.cacheGet(tenantID, cacheKey); e != nil {
		proto, ok := r.embedProtos[e.provider.Kind]
		if !ok {
			return ProviderConfig{}, nil, fmt.Errorf("model registry: no embed protocol for provider kind %q", e.provider.Kind)
		}
		return e.config, proto, nil
	}
	cfg, provider, err := r.resolveModelFromDB(ctx, tenantID, modelName, cacheKey, domain.CapEmbedding)
	if err != nil {
		return ProviderConfig{}, nil, err
	}
	proto, ok := r.embedProtos[provider.Kind]
	if !ok {
		return ProviderConfig{}, nil, fmt.Errorf("model registry: no embed protocol for %q", provider.Kind)
	}
	return cfg, proto, nil
}

// resolveModelFromDB performs the shared cache-miss resolution: lists enabled
// models, finds the matching model, looks up its provider, caches the result,
// and returns the provider config and provider info.
func (r *ModelRegistry) resolveModelFromDB(
	ctx context.Context,
	tenantID, modelName, cacheKey string,
	capability domain.ModelCapability,
) (ProviderConfig, domain.Provider, error) {
	enabled := true
	models, err := r.modelRepo.List(ctx, tenantID, port.ModelFilter{Enabled: &enabled, Capability: capability})
	if err != nil {
		return ProviderConfig{}, domain.Provider{}, fmt.Errorf("model registry: list models: %w", err)
	}
	for _, m := range models {
		if m.Name == modelName {
			provider, err := r.providerRepo.Get(ctx, tenantID, m.ProviderID)
			if err != nil {
				return ProviderConfig{}, domain.Provider{}, fmt.Errorf("model registry: get provider: %w", err)
			}
			if !provider.Enabled || !r.supports(provider.Kind, capability) {
				continue
			}
			cfg := ProviderConfig{
				Name:        provider.Name,
				BaseURL:     provider.BaseURL,
				APIKey:      provider.APIKey,
				HealthModel: provider.DefaultModel,
				Models:      []string{m.Name},
			}
			r.cacheSet(tenantID, cacheKey, cfg, *provider)
			return cfg, *provider, nil
		}
	}
	return ProviderConfig{}, domain.Provider{}, fmt.Errorf("model registry: model %q not found for tenant %s", modelName, tenantID)
}

// FallbackCandidate 是已解析的 fallback 候选：模型名 + 可直接调用的
// provider 配置与 chat 协议。
type FallbackCandidate struct {
	Model    string
	Config   ProviderConfig
	Protocol ChatProtocol
}

// ResolveFallbackCandidates 有序列举主模型之外的可用 chat 模型（上限
// constants.MaxModelFallbackCandidates），供 Gateway 在瞬态失败时降级。
// 排序：与主模型同 provider 优先 → Recommended desc → name asc；
// 跳过 disabled provider 与不支持 chat 协议的模型。primary 必须可解析，
// 否则调用方无法发起主调用，直接返回解析错误。
func (r *ModelRegistry) ResolveFallbackCandidates(ctx context.Context, tenantID, primary string) ([]FallbackCandidate, error) {
	primaryCfg, _, err := r.Resolve(ctx, tenantID, primary)
	if err != nil {
		return nil, err
	}
	cands, err := r.listFallbackCandidates(ctx, tenantID, primary, primaryCfg.Name)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(cands, func(i, j int) bool { return candidateLess(cands[i], cands[j]) })
	if len(cands) > constants.MaxModelFallbackCandidates {
		cands = cands[:constants.MaxModelFallbackCandidates]
	}
	result := make([]FallbackCandidate, 0, len(cands))
	for _, c := range cands {
		cfg := ProviderConfig{
			Name:        c.provider.Name,
			BaseURL:     c.provider.BaseURL,
			APIKey:      c.provider.APIKey,
			HealthModel: c.provider.DefaultModel,
			Models:      []string{c.model.Name},
		}
		// 复用 TTL 缓存语义：WarmTenant/Resolve 已缓存的 entry 保持有效，
		// 这里与缓存数据同源（同 modelRepo/providerRepo），直接写回。
		r.cacheSet(tenantID, "chat:"+c.model.Name, cfg, c.provider)
		proto, ok := r.chatProtos[c.provider.Kind]
		if !ok {
			continue
		}
		result = append(result, FallbackCandidate{Model: c.model.Name, Config: cfg, Protocol: proto})
	}
	return result, nil
}

// fallbackCand 是候选模型及其 provider（samePrimary 标记与主模型同 provider）。
type fallbackCand struct {
	model       domain.Model
	provider    domain.Provider
	samePrimary bool
}

// listFallbackCandidates 列举主模型之外的 enabled chat 模型，跳过 disabled
// provider 与不支持 chat 协议的模型。
func (r *ModelRegistry) listFallbackCandidates(ctx context.Context, tenantID, primary, primaryProviderName string) ([]fallbackCand, error) {
	enabled := true
	models, err := r.modelRepo.List(ctx, tenantID, port.ModelFilter{Enabled: &enabled, Capability: domain.CapChat})
	if err != nil {
		return nil, fmt.Errorf("model registry: list models: %w", err)
	}
	cands := make([]fallbackCand, 0, len(models))
	for _, m := range models {
		if m.Name == primary {
			continue
		}
		provider, err := r.providerRepo.Get(ctx, tenantID, m.ProviderID)
		if err != nil {
			return nil, fmt.Errorf("model registry: get provider: %w", err)
		}
		if !provider.Enabled || !r.supports(provider.Kind, domain.CapChat) {
			continue
		}
		cands = append(cands, fallbackCand{model: m, provider: *provider, samePrimary: provider.Name == primaryProviderName})
	}
	return cands, nil
}

// candidateLess 是 fallback 候选的排序比较：同 provider 优先 → Recommended
// desc → name asc。
func candidateLess(a, b fallbackCand) bool {
	if a.samePrimary != b.samePrimary {
		return a.samePrimary
	}
	if a.model.Recommended != b.model.Recommended {
		return a.model.Recommended
	}
	return a.model.Name < b.model.Name
}

// ListChatModelsByTenant returns sorted enabled chat model names for a tenant.
func (r *ModelRegistry) ListChatModelsByTenant(ctx context.Context, tenantID string) ([]string, error) {
	return r.listModelsByCapability(ctx, tenantID, domain.CapChat)
}

// ListEmbeddingModelsByTenant returns sorted enabled embedding model names for a tenant.
func (r *ModelRegistry) ListEmbeddingModelsByTenant(ctx context.Context, tenantID string) ([]string, error) {
	return r.listModelsByCapability(ctx, tenantID, domain.CapEmbedding)
}

// ResolveDefaultEmbeddingModel 解析 tenant 的默认嵌入模型名：
// 1. enabled 且 provider 可用且标记 default_embedding 的模型优先；
// 2. 无标记 → enabled 列表第一个（保留现状 sort.Strings 字典序语义）；
// 3. 列表为空 → 返回 ""，调用方 fail-closed（不默认放行）。
func (r *ModelRegistry) ResolveDefaultEmbeddingModel(ctx context.Context, tenantID string) (string, error) {
	enabled := true
	models, err := r.modelRepo.List(ctx, tenantID, port.ModelFilter{Enabled: &enabled, Capability: domain.CapEmbedding})
	if err != nil {
		return "", fmt.Errorf("model registry: list embedding models: %w", err)
	}
	names := make([]string, 0, len(models))
	var marked string
	for _, m := range models {
		provider, err := r.providerRepo.Get(ctx, tenantID, m.ProviderID)
		if err != nil {
			return "", fmt.Errorf("model registry: get provider: %w", err)
		}
		if !provider.Enabled || !r.supports(provider.Kind, domain.CapEmbedding) {
			continue
		}
		if m.DefaultEmbedding {
			marked = m.Name
		}
		names = append(names, m.Name)
	}
	if marked != "" {
		return marked, nil
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "", nil
	}
	return names[0], nil
}

func (r *ModelRegistry) listModelsByCapability(
	ctx context.Context,
	tenantID string,
	capability domain.ModelCapability,
) ([]string, error) {
	enabled := true
	models, err := r.modelRepo.List(ctx, tenantID, port.ModelFilter{
		Enabled:    &enabled,
		Capability: capability,
	})
	if err != nil {
		return nil, fmt.Errorf("model registry: list models: %w", err)
	}
	names := make([]string, 0, len(models))
	for _, m := range models {
		provider, err := r.providerRepo.Get(ctx, tenantID, m.ProviderID)
		if err != nil {
			return nil, fmt.Errorf("model registry: get provider: %w", err)
		}
		if !provider.Enabled || !r.supports(provider.Kind, capability) {
			continue
		}
		names = append(names, m.Name)
	}
	sort.Strings(names)
	return names, nil
}

func (r *ModelRegistry) supports(kind domain.ProviderKind, capability domain.ModelCapability) bool {
	switch capability {
	case domain.CapChat:
		_, ok := r.chatProtos[kind]
		return ok
	case domain.CapEmbedding:
		_, ok := r.embedProtos[kind]
		return ok
	default:
		return false
	}
}

// WarmTenant pre-warms the cache by listing enabled models for a tenant and
// populating cache entries for each model so that subsequent Resolve and
// ResolveEmbedding calls hit the cache.
func (r *ModelRegistry) WarmTenant(ctx context.Context, tenantID string) error {
	enabled := true
	models, err := r.modelRepo.List(ctx, tenantID, port.ModelFilter{Enabled: &enabled})
	if err != nil {
		return fmt.Errorf("model registry: warm tenant: %w", err)
	}
	for _, m := range models {
		provider, err := r.providerRepo.Get(ctx, tenantID, m.ProviderID)
		if err != nil {
			return fmt.Errorf("model registry: warm tenant: get provider: %w", err)
		}
		if !provider.Enabled {
			continue
		}
		cfg := ProviderConfig{
			Name:        provider.Name,
			BaseURL:     provider.BaseURL,
			APIKey:      provider.APIKey,
			HealthModel: provider.DefaultModel,
			Models:      []string{m.Name},
		}
		for _, cap := range m.Capabilities {
			if !r.supports(provider.Kind, cap) {
				continue
			}
			switch cap {
			case domain.CapChat:
				r.cacheSet(tenantID, "chat:"+m.Name, cfg, *provider)
			case domain.CapEmbedding:
				r.cacheSet(tenantID, "embed:"+m.Name, cfg, *provider)
			}
		}
	}
	return nil
}

// GetChatModelContextWindow returns the ContextWindow for a named chat model
// belonging to tenantID. Returns 0 when the model is not found or has no
// known context window.
func (r *ModelRegistry) GetChatModelContextWindow(ctx context.Context, tenantID, modelName string) (int, error) {
	enabled := true
	models, err := r.modelRepo.List(ctx, tenantID, port.ModelFilter{Enabled: &enabled, Capability: domain.CapChat})
	if err != nil {
		return 0, fmt.Errorf("model registry: get context window: %w", err)
	}
	for _, m := range models {
		if m.Name == modelName {
			return m.ContextWindow, nil
		}
	}
	return 0, nil
}

// ListChatModels returns an empty slice. Tenant-scoped model lists are
// available via ListChatModels(ctx, tenantID). This method satisfies
// port.ModelCatalog for non-tenant contexts.
func (r *ModelRegistry) ListChatModels() []string {
	return []string{}
}

// ListEmbeddingModels returns an empty slice. Tenant-scoped model lists
// are available via ListEmbeddingModels(ctx, tenantID).
func (r *ModelRegistry) ListEmbeddingModels() []string {
	return []string{}
}

// Invalidate removes all cached entries for a tenant.
func (r *ModelRegistry) Invalidate(tenantID string) {
	r.mu.Lock()
	delete(r.cache, tenantID)
	r.mu.Unlock()
}

// cacheGet returns a non-expired cached entry, or nil.
func (r *ModelRegistry) cacheGet(tenantID, key string) *resolvedEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tenantCache, ok := r.cache[tenantID]
	if !ok {
		return nil
	}
	e, ok := tenantCache[key]
	if !ok || time.Now().After(e.expires) {
		return nil
	}
	return e
}

// cacheSet stores an entry in the cache.
func (r *ModelRegistry) cacheSet(tenantID, key string, cfg ProviderConfig, provider domain.Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache[tenantID] == nil {
		r.cache[tenantID] = make(map[string]*resolvedEntry)
	}
	r.cache[tenantID][key] = &resolvedEntry{
		config:   cfg,
		provider: provider,
		expires:  time.Now().Add(r.cacheTTL),
	}
}
