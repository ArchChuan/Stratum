package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/constants"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
)

// TenantSettingsFn reads raw settings JSON for a tenant. Implementations must
// return the JSONB stored in public.tenants.settings for the given tenantID,
// or an error if the tenant does not exist.
type TenantSettingsFn func(ctx context.Context, tenantID string) ([]byte, error)

type tenantModelCache struct {
	clients   map[string]*OpenAICompatClient // provider name → client
	expiresAt time.Time
}

func (c *tenantModelCache) fresh() bool {
	return c != nil && time.Now().Before(c.expiresAt)
}

// ModelRegistry resolves model names to per-tenant provider clients. It
// replaces the old per-tenant Gateway construction pattern: instead of
// building a separate Gateway per tenant (each with its own client map),
// every call goes through a single shared Gateway backed by this registry.
type ModelRegistry struct {
	readSettings TenantSettingsFn
	aesKey       [32]byte
	mu           sync.RWMutex
	caches       map[string]*tenantModelCache
	logger       *zap.Logger
}

// NewModelRegistry creates a ModelRegistry. readSettings should return the raw
// settings JSON from public.tenants for the given tenantID.
func NewModelRegistry(readSettings TenantSettingsFn, aesKey [32]byte, logger *zap.Logger) *ModelRegistry {
	return &ModelRegistry{
		readSettings: readSettings,
		aesKey:       aesKey,
		caches:       make(map[string]*tenantModelCache),
		logger:       logger,
	}
}

func (r *ModelRegistry) getCache(tenantID string) *tenantModelCache {
	r.mu.RLock()
	c := r.caches[tenantID]
	r.mu.RUnlock()
	if c.fresh() {
		return c
	}
	return nil
}

func (r *ModelRegistry) setCache(tenantID string, c *tenantModelCache) {
	c.expiresAt = time.Now().Add(constants.GatewayCacheTTL)
	r.mu.Lock()
	r.caches[tenantID] = c
	r.mu.Unlock()
}

// WarmTenant ensures the tenant's provider clients are loaded and cached.
func (r *ModelRegistry) WarmTenant(ctx context.Context, tenantID string) error {
	if c := r.getCache(tenantID); c != nil {
		return nil
	}
	return r.loadTenant(ctx, tenantID)
}

func (r *ModelRegistry) loadTenant(ctx context.Context, tenantID string) error {
	if r.readSettings == nil {
		return fmt.Errorf("model registry: settings reader unavailable")
	}
	raw, err := r.readSettings(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("model registry: read settings for %s: %w", tenantID, err)
	}

	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		return fmt.Errorf("model registry: decode settings for %s: %w", tenantID, err)
	}

	apiKeysRaw, ok := settings["llm_api_keys"].(map[string]any)
	if !ok || len(apiKeysRaw) == 0 {
		return fmt.Errorf("model registry: no llm_api_keys for tenant %s", tenantID)
	}

	// Read per-provider base URL overrides from settings.
	baseURLs := map[string]string{}
	if raw, ok := settings["base_urls"].(map[string]any); ok {
		for provider, url := range raw {
			if s, ok := url.(string); ok {
				baseURLs[provider] = s
			}
		}
	}

	cache := &tenantModelCache{clients: make(map[string]*OpenAICompatClient)}
	for provider, enc := range apiKeysRaw {
		encStr, ok := enc.(string)
		if !ok || encStr == "" {
			continue
		}
		plain, err := pkgcrypto.Decrypt(r.aesKey, encStr)
		if err != nil {
			r.logger.Warn("model_registry.decrypt_failed",
				zap.String("tenant_id", tenantID),
				zap.String("provider", provider),
				zap.Error(err))
			continue
		}
		switch provider {
		case "qwen":
			if base := baseURLs["qwen"]; base != "" {
				cache.clients["qwen"] = NewQwenClientWithBase(plain, base, r.logger)
			} else {
				cache.clients["qwen"] = NewQwenClient(plain, r.logger)
			}
		case "zhipu":
			if base := baseURLs["zhipu"]; base != "" {
				cache.clients["zhipu"] = NewZhipuClientWithBase(plain, base, r.logger)
			} else {
				cache.clients["zhipu"] = NewZhipuClient(plain, r.logger)
			}
		}
	}
	if len(cache.clients) == 0 {
		return fmt.Errorf("model registry: no usable provider for tenant %s", tenantID)
	}

	r.setCache(tenantID, cache)
	return nil
}

// resolveProvider maps a model name to its provider key using the same
// prefixes the old parseProvider relied on.
func resolveProvider(model string) string {
	switch {
	case strings.HasPrefix(model, "text-embedding-v"), strings.HasPrefix(model, "qwen-"):
		return "qwen"
	case strings.HasPrefix(model, "embedding-3"), strings.HasPrefix(model, "glm-"):
		return "zhipu"
	default:
		return ""
	}
}

// ResolveChat returns the chat client for the given tenant and model.
func (r *ModelRegistry) ResolveChat(ctx context.Context, tenantID, model string) (*OpenAICompatClient, error) {
	if err := r.WarmTenant(ctx, tenantID); err != nil {
		return nil, err
	}
	c := r.getCache(tenantID)
	if c == nil {
		return nil, fmt.Errorf("model registry: tenant %s cache miss after warm", tenantID)
	}

	provider := resolveProvider(model)
	if provider != "" {
		if client, ok := c.clients[provider]; ok {
			return client, nil
		}
	}
	// Fallback: return first available client
	for _, client := range c.clients {
		return client, nil
	}
	return nil, fmt.Errorf("model registry: no provider for model %q (tenant %s)", model, tenantID)
}

// ResolveEmbedding returns the embedding client for the given tenant and model.
// When model is empty the first available embedding-capable client is returned.
func (r *ModelRegistry) ResolveEmbedding(ctx context.Context, tenantID, model string) (*OpenAICompatClient, error) {
	if err := r.WarmTenant(ctx, tenantID); err != nil {
		return nil, err
	}
	c := r.getCache(tenantID)
	if c == nil {
		return nil, fmt.Errorf("model registry: tenant %s cache miss after warm", tenantID)
	}

	if model != "" {
		provider := resolveProvider(model)
		if provider != "" {
			if client, ok := c.clients[provider]; ok {
				return client, nil
			}
		}
	}
	// Fallback: return first available client
	for _, client := range c.clients {
		return client, nil
	}
	return nil, fmt.Errorf("model registry: no embedding provider for tenant %s", tenantID)
}

// ListChatModelsByTenant returns chat model names available to the tenant.
func (r *ModelRegistry) ListChatModelsByTenant(ctx context.Context, tenantID string) ([]string, error) {
	if err := r.WarmTenant(ctx, tenantID); err != nil {
		return nil, err
	}
	c := r.getCache(tenantID)
	if c == nil {
		return nil, fmt.Errorf("model registry: tenant %s not configured", tenantID)
	}
	var models []string
	for _, client := range c.clients {
		models = append(models, client.Models()...)
	}
	return models, nil
}

// ListEmbeddingModelsByTenant returns embedding model names available to the tenant.
func (r *ModelRegistry) ListEmbeddingModelsByTenant(ctx context.Context, tenantID string) ([]string, error) {
	if err := r.WarmTenant(ctx, tenantID); err != nil {
		return nil, err
	}
	c := r.getCache(tenantID)
	if c == nil {
		return nil, fmt.Errorf("model registry: tenant %s not configured", tenantID)
	}
	seen := map[string]bool{}
	var models []string
	for provider := range c.clients {
		for _, m := range embeddingModelsForProvider(provider) {
			if !seen[m] {
				seen[m] = true
				models = append(models, m)
			}
		}
	}
	return models, nil
}

func embeddingModelsForProvider(provider string) []string {
	switch provider {
	case "qwen":
		return []string{"text-embedding-v3", "text-embedding-v2"}
	case "zhipu":
		return []string{"embedding-3"}
	default:
		return nil
	}
}

// Invalidate clears the cached configuration for a tenant so the next
// resolve will reload settings from the database.
func (r *ModelRegistry) Invalidate(tenantID string) {
	r.mu.Lock()
	delete(r.caches, tenantID)
	r.mu.Unlock()
}
