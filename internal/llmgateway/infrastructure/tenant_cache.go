package infrastructure

import (
	"sync"
	"time"
)

// TenantGatewayCache is a TTL-based in-memory cache mapping tenantID → *Gateway + decrypted API keys.
type TenantGatewayCache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry
}

type cacheEntry struct {
	gateway   *Gateway
	apiKeys   map[string]string
	expiresAt time.Time
}

// NewTenantGatewayCache returns an initialized cache.
func NewTenantGatewayCache() *TenantGatewayCache {
	return &TenantGatewayCache{
		entries: make(map[string]*cacheEntry),
	}
}

// Get returns the cached Gateway and decrypted API keys for tenantID, or (nil, nil, false) on miss/expiry.
func (c *TenantGatewayCache) Get(tenantID string) (*Gateway, map[string]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[tenantID]
	if !ok {
		return nil, nil, false
	}
	if time.Now().After(e.expiresAt) {
		delete(c.entries, tenantID)
		return nil, nil, false
	}
	return e.gateway, e.apiKeys, true
}

// Set stores a Gateway and its decrypted API keys with the given TTL.
func (c *TenantGatewayCache) Set(tenantID string, gw *Gateway, keys map[string]string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[tenantID] = &cacheEntry{gateway: gw, apiKeys: keys, expiresAt: time.Now().Add(ttl)}
}

// Invalidate removes the cached entry for tenantID immediately.
func (c *TenantGatewayCache) Invalidate(tenantID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, tenantID)
}
