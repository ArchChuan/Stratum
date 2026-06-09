package llmgateway

import (
	"sync"
	"time"
)

// TenantGatewayCache is a TTL-based in-memory cache mapping tenantID → *Gateway.
type TenantGatewayCache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry
}

type cacheEntry struct {
	gateway   *Gateway
	expiresAt time.Time
}

// NewTenantGatewayCache returns an initialized cache.
func NewTenantGatewayCache() *TenantGatewayCache {
	return &TenantGatewayCache{
		entries: make(map[string]*cacheEntry),
	}
}

// Get returns the cached Gateway for tenantID, or (nil, false) on miss/expiry.
func (c *TenantGatewayCache) Get(tenantID string) (*Gateway, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[tenantID]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expiresAt) {
		delete(c.entries, tenantID)
		return nil, false
	}
	return e.gateway, true
}

// Set stores a Gateway with the given TTL.
func (c *TenantGatewayCache) Set(tenantID string, gw *Gateway, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[tenantID] = &cacheEntry{gateway: gw, expiresAt: time.Now().Add(ttl)}
}

// Invalidate removes the cached entry for tenantID immediately.
func (c *TenantGatewayCache) Invalidate(tenantID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, tenantID)
}
