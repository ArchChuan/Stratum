package infrastructure

import (
	"context"
	"testing"
	"time"

	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// stdio 全链禁用：5 条绕过路径（ReconnectServer 路由、getOrRestoreClient 懒恢复、
// scanOrphaned（已随 failover 删除）、evaluation withRevisionClient、
// performHealthCheck 重连）全部收敛于 BaseClient.doConnect 的唯一权威拒绝。
// 这里用真实 BaseClient factory 逐一验证：任一入口对 stdio config 都返回
// ErrUnsupportedTransport 且不注册 client。spawn 不可能发生：connectStdio 已
// 随实现删除，doConnect 的拒绝先于任何进程创建（带 command 也拒绝即证）。
func stdioBypassManager(t *testing.T) *ClientManager {
	t.Helper()
	m := NewClientManager(zap.NewNop(), nil, nil)
	m.clientFactory = func(cfg *MCPServerConfig, logger *zap.Logger) MCPClient {
		return NewBaseClient(cfg, logger)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	return m
}

func stdioBypassConfig() *MCPServerConfig {
	return &MCPServerConfig{
		ID: "stdio-srv", Name: "stdio", Transport: "stdio",
		Command: "/bin/echo", Args: []string{"hi"},
	}
}

// TestStdioBypassManagerConnectRejected covers the ReconnectServer route and
// getOrRestoreClient lazy-restore path: both funnel into manager.Connect,
// whose client.Connect lands on doConnect.
func TestStdioBypassManagerConnectRejected(t *testing.T) {
	manager := stdioBypassManager(t)

	err := manager.Connect(context.Background(), stdioBypassConfig(), nil, "", nil)

	require.ErrorIs(t, err, mcpdomain.ErrUnsupportedTransport)
	require.Empty(t, manager.GetAllClients(context.Background()), "stdio client must not be registered")
	require.NotContains(t, manager.configs, "default:stdio-srv", "stdio config must not be cached")
}

// TestStdioBypassRevisionClientRejected covers the evaluation withRevisionClient
// path: CallToolWithConfig / ListToolsWithConfig build an isolated client from a
// revision config, which must be rejected by the same doConnect authority.
func TestStdioBypassRevisionClientRejected(t *testing.T) {
	manager := stdioBypassManager(t)
	ctx := context.Background()

	_, err := manager.CallToolWithConfig(ctx, stdioBypassConfig(), "tool", map[string]any{})
	require.ErrorIs(t, err, mcpdomain.ErrUnsupportedTransport)

	_, err = manager.ListToolsWithConfig(ctx, stdioBypassConfig())
	require.ErrorIs(t, err, mcpdomain.ErrUnsupportedTransport)
}

// unhealthyStdioStub is a pre-registered unhealthy client standing in for a
// stdio server whose connection predates the transport ban; performHealthCheck
// must attempt a reconnect through the real factory and be refused.
type unhealthyStdioStub struct{ *reconnectMCPClient }

func TestStdioBypassHealthCheckReconnectRejected(t *testing.T) {
	manager := stdioBypassManager(t)

	// Seed an unhealthy stdio client into the manager as a live entry.
	stub := &unhealthyStdioStub{reconnectMCPClient: &reconnectMCPClient{healthy: false}}
	key := "default:stdio-srv"
	manager.mu.Lock()
	manager.clients[key] = stub
	manager.configs[key] = stdioBypassConfig()
	manager.mu.Unlock()

	require.NotPanics(t, func() { manager.performHealthCheck() }, "health-check reconnect must not panic")

	manager.mu.RLock()
	defer manager.mu.RUnlock()
	require.Same(t, stub, manager.clients[key], "rejected reconnect must leave the original client in place")
}

// TestStdioBypassNoSpawnWithCommand pins the rejection ordering: a stdio config
// with a real command is refused before any process creation could occur.
func TestStdioBypassNoSpawnWithCommand(t *testing.T) {
	client := NewBaseClient(stdioBypassConfig(), zap.NewNop())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.ErrorIs(t, client.Connect(ctx), mcpdomain.ErrUnsupportedTransport)
	require.False(t, client.IsConnected())
}
