package infrastructure

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCapabilityCacheGetHitAndMiss(t *testing.T) {
	c := NewCapabilityCache(10, time.Hour)
	// 极端情况：未命中。
	_, ok := c.Get("missing")
	require.False(t, ok)
	// 命中。
	c.Store("s1", []*MCPTool{{Name: "t1"}}, []*MCPResource{{URI: "r1"}})
	entry, ok := c.Get("s1")
	require.True(t, ok)
	require.Len(t, entry.Tools, 1)
	require.Len(t, entry.Resources, 1)
	require.Equal(t, 1, c.Size())
}

func TestCapabilityCacheExpiry(t *testing.T) {
	// 极端情况：TTL 为负 → 条目立即过期。
	c := NewCapabilityCache(10, -time.Second)
	c.Store("s1", []*MCPTool{{Name: "t1"}}, nil)
	_, ok := c.Get("s1")
	require.False(t, ok)
	_, ok = c.GetTools("s1")
	require.False(t, ok)
	_, ok = c.GetResources("s1")
	require.False(t, ok)
}

func TestCapabilityCacheEvictsWhenFull(t *testing.T) {
	// 极端情况：maxSize 满 → 逐出一个旧条目再存入。
	c := NewCapabilityCache(1, time.Hour)
	c.Store("s1", nil, nil)
	c.Store("s2", nil, nil)
	require.Equal(t, 1, c.Size())
	_, ok := c.Get("s2")
	require.True(t, ok)
}

func TestCapabilityCacheStoreToolsResourcesMissingEntry(t *testing.T) {
	// 极端情况：StoreTools/StoreResources 对缺失条目先创建。
	c := NewCapabilityCache(10, time.Hour)
	c.StoreTools("s1", []*MCPTool{{Name: "t1"}})
	c.StoreResources("s1", []*MCPResource{{URI: "r1"}})
	tools, ok := c.GetTools("s1")
	require.True(t, ok)
	require.Len(t, tools, 1)
	resources, ok := c.GetResources("s1")
	require.True(t, ok)
	require.Len(t, resources, 1)

	// 极端情况：满容量时 StoreTools 也逐出。
	c2 := NewCapabilityCache(1, time.Hour)
	c2.Store("a", nil, nil)
	c2.StoreTools("b", nil)
	_, ok = c2.Get("a")
	require.False(t, ok)
	_, ok = c2.GetTools("b")
	require.True(t, ok)
}

func TestCapabilityCacheDeleteAndClear(t *testing.T) {
	c := NewCapabilityCache(10, time.Hour)
	c.Store("s1", nil, nil)
	c.Store("s2", nil, nil)
	c.Delete("s1")
	require.Equal(t, 1, c.Size())
	c.Clear()
	require.Equal(t, 0, c.Size())
}
