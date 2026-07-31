package infrastructure

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/internal/mcp/infrastructure/testserver"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// echoTool returns a minimal MCP tool used by lazy-reconnect tests.
func echoTool() testserver.Tool {
	return testserver.Tool{
		Name:        "echo",
		Description: "echoes input",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"msg": map[string]any{"type": "string"}},
		},
	}
}

// TestLazyReconnectAfterEviction verifies CallTool auto-reconnects
// after the client was evicted from the in-memory map.
func TestLazyReconnectAfterEviction(t *testing.T) {
	logger := zap.NewNop()
	ts := testserver.New(t)
	ts.SetTools([]testserver.Tool{echoTool()})
	ts.SetBehavior("echo", testserver.Behavior{Result: map[string]any{"echo": true}})
	defer ts.Close()

	manager := NewClientManager(logger, nil, nil, "")
	ctx := context.Background()

	cfg := &domain.ServerConfig{
		ID: "lazy-test", Name: "lazy", Transport: "http",
		URL: ts.URL(), Timeout: 2 * time.Second, Enabled: true,
	}
	require.NoError(t, manager.Connect(ctx, cfg))

	// Sanity check before eviction.
	_, err := manager.CallTool(ctx, "lazy-test", "echo", map[string]any{"msg": "before"})
	require.NoError(t, err)

	// Simulate eviction: remove client & cache, keep config in m.configs
	// so GetServerConfig finds it in memory (mirrors DB reload in prod).
	manager.mu.Lock()
	key := tenantKey("", "lazy-test")
	delete(manager.clients, key)
	manager.cache.Delete(key)
	manager.mu.Unlock()

	// CallTool must lazy-reconnect and succeed.
	result, err := manager.CallTool(ctx, "lazy-test", "echo", map[string]any{"msg": "after"})
	require.NoError(t, err)
	require.NotNil(t, result)

	// Client must be re-registered.
	manager.mu.RLock()
	client := manager.clients[key]
	manager.mu.RUnlock()
	require.NotNil(t, client, "client should be re-registered after lazy reconnect")
}

// TestLazyReconnectSkipsDisabledServer verifies enabled=false servers
// are NOT resurrected by lazy reconnect.
func TestLazyReconnectSkipsDisabledServer(t *testing.T) {
	logger := zap.NewNop()
	ts := testserver.New(t)
	ts.SetTools([]testserver.Tool{echoTool()})
	ts.SetBehavior("echo", testserver.Behavior{Result: map[string]any{"echo": true}})
	defer ts.Close()

	manager := NewClientManager(logger, nil, nil, "")
	ctx := context.Background()

	cfg := &domain.ServerConfig{
		ID: "disabled-srv", Name: "disabled", Transport: "http",
		URL: ts.URL(), Timeout: 2 * time.Second, Enabled: true,
	}
	require.NoError(t, manager.Connect(ctx, cfg))

	// Evict client, keep config but mark disabled.
	manager.mu.Lock()
	key := tenantKey("", "disabled-srv")
	delete(manager.clients, key)
	if c, ok := manager.configs[key]; ok {
		c.Enabled = false
	}
	manager.cache.Delete(key)
	manager.mu.Unlock()

	_, err := manager.CallTool(ctx, "disabled-srv", "echo", map[string]any{})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "client not found"),
		"expected 'client not found' for disabled server, got: %v", err)
}

// TestLazyReconnectConcurrentBurst verifies concurrent CallTool calls
// after eviction all succeed via connect-dedup.
func TestLazyReconnectConcurrentBurst(t *testing.T) {
	logger := zap.NewNop()
	ts := testserver.New(t)
	ts.SetTools([]testserver.Tool{echoTool()})
	ts.SetBehavior("echo", testserver.Behavior{Result: map[string]any{"echo": true}})
	defer ts.Close()

	manager := NewClientManager(logger, nil, nil, "")
	ctx := context.Background()

	cfg := &domain.ServerConfig{
		ID: "burst-srv", Name: "burst", Transport: "http",
		URL: ts.URL(), Timeout: 2 * time.Second, Enabled: true,
	}
	require.NoError(t, manager.Connect(ctx, cfg))

	// Evict.
	manager.mu.Lock()
	key := tenantKey("", "burst-srv")
	delete(manager.clients, key)
	manager.cache.Delete(key)
	manager.mu.Unlock()

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			_, err := manager.CallTool(ctx, "burst-srv", "echo", map[string]any{"msg": "burst"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err, "all concurrent calls must succeed after lazy reconnect")
	}

	manager.mu.RLock()
	client := manager.clients[key]
	manager.mu.RUnlock()
	require.NotNil(t, client, "client must be re-registered")
}

// TestLazyReconnectServerNotFound preserves contract: unknown ID
// returns "client not found".
func TestLazyReconnectServerNotFound(t *testing.T) {
	logger := zap.NewNop()
	manager := NewClientManager(logger, nil, nil, "")

	_, err := manager.CallTool(context.Background(), "nonexistent-id", "some_tool", map[string]any{})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "client not found"),
		"expected 'client not found', got: %v", err)
}
