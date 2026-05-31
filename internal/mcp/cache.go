package mcp

import (
	"sync"
	"time"
)

// CacheEntry 缓存条目
type CacheEntry struct {
	Tools      []*MCPTool
	Resources  []*MCPResource
	ExpiresAt  time.Time
}

// CapabilityCache 能力缓存
type CapabilityCache struct {
	entries map[string]*CacheEntry
	maxSize int
	ttl     time.Duration
	mu      sync.RWMutex
}

// NewCapabilityCache 创建新的能力缓存
func NewCapabilityCache(maxSize int, ttl time.Duration) *CapabilityCache {
	return &CapabilityCache{
		entries: make(map[string]*CacheEntry),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// Store 存储能力
func (c *CapabilityCache) Store(serverID string, tools []*MCPTool, resources []*MCPResource) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= c.maxSize {
		// 简单的 LRU 清理：删除第一个条目
		for k := range c.entries {
			delete(c.entries, k)
			break
		}
	}

	c.entries[serverID] = &CacheEntry{
		Tools:     tools,
		Resources: resources,
		ExpiresAt: time.Now().Add(c.ttl),
	}
}

// StoreTools 存储工具
func (c *CapabilityCache) StoreTools(serverID string, tools []*MCPTool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, exists := c.entries[serverID]
	if !exists {
		if len(c.entries) >= c.maxSize {
			for k := range c.entries {
				delete(c.entries, k)
				break
			}
		}
		entry = &CacheEntry{}
		c.entries[serverID] = entry
	}

	entry.Tools = tools
	entry.ExpiresAt = time.Now().Add(c.ttl)
}

// StoreResources 存储资源
func (c *CapabilityCache) StoreResources(serverID string, resources []*MCPResource) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, exists := c.entries[serverID]
	if !exists {
		if len(c.entries) >= c.maxSize {
			for k := range c.entries {
				delete(c.entries, k)
				break
			}
		}
		entry = &CacheEntry{}
		c.entries[serverID] = entry
	}

	entry.Resources = resources
	entry.ExpiresAt = time.Now().Add(c.ttl)
}

// GetTools 获取工具
func (c *CapabilityCache) GetTools(serverID string) ([]*MCPTool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.entries[serverID]
	if !exists || time.Now().After(entry.ExpiresAt) {
		return nil, false
	}

	return entry.Tools, true
}

// GetResources 获取资源
func (c *CapabilityCache) GetResources(serverID string) ([]*MCPResource, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.entries[serverID]
	if !exists || time.Now().After(entry.ExpiresAt) {
		return nil, false
	}

	return entry.Resources, true
}

// Get 获取缓存条目
func (c *CapabilityCache) Get(serverID string) (*CacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.entries[serverID]
	if !exists || time.Now().After(entry.ExpiresAt) {
		return nil, false
	}

	return entry, true
}

// Delete 删除缓存条目
func (c *CapabilityCache) Delete(serverID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, serverID)
}

// Clear 清空缓存
func (c *CapabilityCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*CacheEntry)
}

// Size 获取缓存大小
func (c *CapabilityCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.entries)
}
