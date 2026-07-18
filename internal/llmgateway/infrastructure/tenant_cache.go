package infrastructure

import (
	"sync"
	"time"
)

// TenantGatewayCache is a TTL-based in-memory cache mapping tenantID → *Gateway + decrypted API keys.
type TenantGatewayCache struct {
	mu             sync.Mutex
	entries        map[string]*cacheEntry
	generations    map[string]*generationState
	nextGeneration uint64
}

type generationState struct {
	token       uint64
	activeLoads uint64
}

type cacheEntry struct {
	gateway   *Gateway
	apiKeys   map[string]string
	expiresAt time.Time
}

// NewTenantGatewayCache returns an initialized cache.
func NewTenantGatewayCache() *TenantGatewayCache {
	return &TenantGatewayCache{
		entries:     make(map[string]*cacheEntry),
		generations: make(map[string]*generationState),
	}
}

// Get returns the cached Gateway and decrypted API keys for tenantID, or (nil, nil, false) on miss/expiry.
func (c *TenantGatewayCache) Get(tenantID string) (*Gateway, map[string]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expireEntryLocked(tenantID)
	e, ok := c.entries[tenantID]
	if !ok {
		return nil, nil, false
	}
	return e.gateway, cloneKeys(e.apiKeys), true
}

// GetWithGeneration atomically returns the cache result and tenant generation.
// Callers use the generation to conditionally publish work performed after a miss.
func (c *TenantGatewayCache) GetWithGeneration(tenantID string) (*Gateway, map[string]string, bool, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expireEntryLocked(tenantID)
	e, ok := c.entries[tenantID]
	state, stateOK := c.generations[tenantID]
	if !stateOK {
		state = &generationState{token: c.allocateGenerationLocked()}
		c.generations[tenantID] = state
	}
	if !ok {
		state.activeLoads++
		return nil, nil, false, state.token
	}
	return e.gateway, cloneKeys(e.apiKeys), true, state.token
}

// Set stores a Gateway and its decrypted API keys with the given TTL.
func (c *TenantGatewayCache) Set(tenantID string, gw *Gateway, keys map[string]string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expireEntryLocked(tenantID)
	if _, ok := c.generations[tenantID]; !ok {
		c.generations[tenantID] = &generationState{token: c.allocateGenerationLocked()}
	}
	c.entries[tenantID] = &cacheEntry{gateway: gw, apiKeys: cloneKeys(keys), expiresAt: time.Now().Add(ttl)}
}

// SetIfGeneration stores a load result only if no invalidation occurred since
// the caller captured generation with GetWithGeneration.
func (c *TenantGatewayCache) SetIfGeneration(tenantID string, gw *Gateway, keys map[string]string, ttl time.Duration, generation uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.expireEntryLocked(tenantID) {
		return false
	}
	state, ok := c.generations[tenantID]
	if !ok || state.token != generation {
		return false
	}
	c.entries[tenantID] = &cacheEntry{gateway: gw, apiKeys: cloneKeys(keys), expiresAt: time.Now().Add(ttl)}
	return true
}

// ReleaseLoad releases an unpublished load token if it is still current.
func (c *TenantGatewayCache) ReleaseLoad(tenantID string, generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, ok := c.generations[tenantID]
	if !ok || state.token != generation || state.activeLoads == 0 {
		return
	}
	state.activeLoads--
	if state.activeLoads == 0 {
		if _, ok := c.entries[tenantID]; ok {
			return
		}
		delete(c.generations, tenantID)
	}
}

// Invalidate removes the cached entry for tenantID immediately.
func (c *TenantGatewayCache) Invalidate(tenantID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, tenantID)
	delete(c.generations, tenantID)
}

func (c *TenantGatewayCache) allocateGenerationLocked() uint64 {
	c.nextGeneration++
	return c.nextGeneration
}

func (c *TenantGatewayCache) expireEntryLocked(tenantID string) bool {
	entry, ok := c.entries[tenantID]
	if !ok || !time.Now().After(entry.expiresAt) {
		return false
	}
	delete(c.entries, tenantID)
	delete(c.generations, tenantID)
	return true
}

func cloneKeys(keys map[string]string) map[string]string {
	if keys == nil {
		return nil
	}
	out := make(map[string]string, len(keys))
	for k, v := range keys {
		out[k] = v
	}
	return out
}
