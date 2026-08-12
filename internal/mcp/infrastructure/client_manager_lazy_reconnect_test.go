package infrastructure

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newEchoSDKServer starts an official SDK MCP server exposing an "echo"
// tool over streamable HTTP on a loopback httptest server.
func newEchoSDKServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "echo", Version: "1.0.0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "echo",
		Description: "echoes input",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"msg": map[string]any{"type": "string"}},
		},
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "echo"}},
		}, nil, nil
	})
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))
	// The SDK client keeps the SSE GET stream open for server->client
	// notifications; CloseClientConnections must force it closed before
	// Close, otherwise ts.Close blocks draining that connection.
	t.Cleanup(func() {
		ts.CloseClientConnections()
		ts.Close()
	})
	return ts
}

// newLazyTestManager builds a ClientManager whose clientFactory relaxes the
// SSRF policy (URLPolicyAllowPrivate) so tests may dial the loopback
// httptest server. Production construction keeps the strict policy.
func newLazyTestManager(logger *zap.Logger) *ClientManager {
	manager := NewClientManager(logger, nil, nil)
	defaultFactory := manager.clientFactory
	manager.clientFactory = func(cfg *MCPServerConfig, logger *zap.Logger) MCPClient {
		client := defaultFactory(cfg, logger).(*BaseClient)
		client.urlPolicy = URLPolicyAllowPrivate
		return client
	}
	return manager
}

// TestLazyReconnectAfterEviction verifies CallTool auto-reconnects
// after the client was evicted from the in-memory map.
func TestLazyReconnectAfterEviction(t *testing.T) {
	logger := zap.NewNop()
	ts := newEchoSDKServer(t)

	manager := newLazyTestManager(logger)
	ctx := context.Background()

	cfg := &domain.ServerConfig{
		ID: "lazy-test", Name: "lazy", Transport: "http",
		URL: ts.URL, Timeout: 2 * time.Second, Enabled: true,
	}
	require.NoError(t, manager.Connect(ctx, cfg, nil, "", nil))

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
	ts := newEchoSDKServer(t)

	manager := newLazyTestManager(logger)
	ctx := context.Background()

	cfg := &domain.ServerConfig{
		ID: "disabled-srv", Name: "disabled", Transport: "http",
		URL: ts.URL, Timeout: 2 * time.Second, Enabled: true,
	}
	require.NoError(t, manager.Connect(ctx, cfg, nil, "", nil))

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
	ts := newEchoSDKServer(t)

	manager := newLazyTestManager(logger)
	ctx := context.Background()

	cfg := &domain.ServerConfig{
		ID: "burst-srv", Name: "burst", Transport: "http",
		URL: ts.URL, Timeout: 2 * time.Second, Enabled: true,
	}
	require.NoError(t, manager.Connect(ctx, cfg, nil, "", nil))

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
	manager := newLazyTestManager(logger)

	_, err := manager.CallTool(context.Background(), "nonexistent-id", "some_tool", map[string]any{})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "client not found"),
		"expected 'client not found', got: %v", err)
}
