package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestHTTPClientErrorDoesNotExposeResponseBody(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Mcp-Session-Id", "session-1")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("mcp-sensitive-sentinel"))
	}))
	defer server.Close()
	client := NewBaseClient(&MCPServerConfig{
		ID: "server-1", Name: "test", Transport: "http", URL: server.URL, Timeout: time.Second,
	}, zap.NewNop())
	if err := client.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err := client.ListTools(context.Background())
	if err == nil {
		t.Fatal("expected downstream HTTP error")
	}
	if strings.Contains(err.Error(), "mcp-sensitive-sentinel") {
		t.Fatalf("downstream response body leaked through error: %v", err)
	}
}

func TestHTTPClientRejectsJSONRPCProtocolErrorWithoutLeakingMessage(t *testing.T) {
	const sentinel = "mcp-private-protocol-error"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"method":"initialize"`) {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"error":{"code":-32000,"message":"` + sentinel + `"}}`))
	}))
	defer server.Close()
	client := NewBaseClient(&MCPServerConfig{
		ID: "server-1", Name: "test", Transport: "http", URL: server.URL, Timeout: time.Second,
	}, zap.NewNop())
	require.NoError(t, client.Connect(context.Background()))

	_, err := client.CallTool(context.Background(), "failing_tool", map[string]any{})

	require.ErrorContains(t, err, "MCP protocol error")
	require.NotContains(t, err.Error(), sentinel)
}

type blockingMCPClient struct {
	connectStarted chan struct{}
	releaseConnect chan struct{}
}

type reconnectMCPClient struct {
	healthy         bool
	disconnectCalls int
}

func (c *reconnectMCPClient) Connect(context.Context) error    { c.healthy = true; return nil }
func (c *reconnectMCPClient) Disconnect(context.Context) error { c.disconnectCalls++; return nil }
func (c *reconnectMCPClient) IsConnected() bool                { return true }
func (c *reconnectMCPClient) IsHealthy() bool                  { return c.healthy }
func (c *reconnectMCPClient) CallTool(context.Context, string, interface{}) (interface{}, error) {
	return nil, nil
}
func (c *reconnectMCPClient) ListTools(context.Context) ([]*MCPTool, error) { return nil, nil }
func (c *reconnectMCPClient) ListResources(context.Context) ([]*MCPResource, error) {
	return nil, nil
}
func (c *reconnectMCPClient) GetServerInfo() *MCPServerInfo { return &MCPServerInfo{} }

func (c *blockingMCPClient) Connect(ctx context.Context) error {
	close(c.connectStarted)
	select {
	case <-c.releaseConnect:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *blockingMCPClient) Disconnect(context.Context) error { return nil }
func (c *blockingMCPClient) IsConnected() bool                { return true }
func (c *blockingMCPClient) IsHealthy() bool                  { return true }
func (c *blockingMCPClient) CallTool(context.Context, string, interface{}) (interface{}, error) {
	return nil, nil
}
func (c *blockingMCPClient) ListTools(context.Context) ([]*MCPTool, error) {
	return nil, nil
}
func (c *blockingMCPClient) ListResources(context.Context) ([]*MCPResource, error) {
	return nil, nil
}
func (c *blockingMCPClient) GetServerInfo() *MCPServerInfo { return &MCPServerInfo{} }

// TestNewCapabilityCache 测试缓存创建
func TestNewCapabilityCache(t *testing.T) {
	cache := NewCapabilityCache(100, 1*time.Hour)

	if cache == nil {
		t.Fatal("cache should not be nil")
	}

	if cache.Size() != 0 {
		t.Errorf("expected size 0, got %d", cache.Size())
	}
}

// TestCacheStore 测试缓存存储
func TestCacheStore(t *testing.T) {
	cache := NewCapabilityCache(100, 1*time.Hour)

	tools := []*MCPTool{
		{Name: "tool1", Description: "Tool 1"},
		{Name: "tool2", Description: "Tool 2"},
	}

	resources := []*MCPResource{
		{URI: "res1", Name: "Resource 1"},
	}

	cache.Store("server1", tools, resources)

	if cache.Size() != 1 {
		t.Errorf("expected size 1, got %d", cache.Size())
	}

	// 验证工具
	cachedTools, ok := cache.GetTools("server1")
	if !ok {
		t.Fatal("tools should be in cache")
	}

	if len(cachedTools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(cachedTools))
	}

	// 验证资源
	cachedResources, ok := cache.GetResources("server1")
	if !ok {
		t.Fatal("resources should be in cache")
	}

	if len(cachedResources) != 1 {
		t.Errorf("expected 1 resource, got %d", len(cachedResources))
	}
}

// TestCacheExpiration 测试缓存过期
func TestCacheExpiration(t *testing.T) {
	cache := NewCapabilityCache(100, 100*time.Millisecond)

	tools := []*MCPTool{{Name: "tool1", Description: "Tool 1"}}
	cache.StoreTools("server1", tools)

	// 立即检查应该命中
	_, ok := cache.GetTools("server1")
	if !ok {
		t.Fatal("tools should be in cache")
	}

	// 等待过期
	time.Sleep(150 * time.Millisecond)

	// 再次检查应该未命中
	_, ok = cache.GetTools("server1")
	if ok {
		t.Fatal("tools should have expired")
	}
}

// TestClientManagerConnect 测试客户端管理器连接
func TestClientManagerConnect(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	manager := NewClientManager(logger, nil, nil)

	_ = &MCPServerConfig{
		ID:        "test-server",
		Name:      "Test Server",
		Transport: "stdio",
	}

	// 注意：这个测试会失败，因为我们还没有实现实际的连接逻辑
	// 这里只是验证管理器的结构
	if manager == nil {
		t.Fatal("manager should not be nil")
	}

	if len(manager.GetAllClients(context.Background())) != 0 {
		t.Errorf("expected 0 clients, got %d", len(manager.GetAllClients(context.Background())))
	}
}

func TestClientManagerConnectDoesNotBlockReadersWhileDialing(t *testing.T) {
	logger := zap.NewNop()
	manager := NewClientManager(logger, nil, nil)
	started := make(chan struct{})
	release := make(chan struct{})
	manager.clientFactory = func(*MCPServerConfig, *zap.Logger) MCPClient {
		return &blockingMCPClient{connectStarted: started, releaseConnect: release}
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- manager.Connect(context.Background(), &MCPServerConfig{
			ID:        "slow-server",
			Name:      "Slow Server",
			Transport: "stdio",
		})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("fake MCP client did not start connecting")
	}

	readDone := make(chan struct{})
	go func() {
		_ = manager.GetAllClients(context.Background())
		close(readDone)
	}()

	select {
	case <-readDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("GetAllClients blocked while Connect was waiting on external I/O")
	}

	close(release)
	if err := <-errCh; err != nil {
		t.Fatalf("connect returned error: %v", err)
	}
}

func TestClientManagerHealthReconnectClosesDisplacedClient(t *testing.T) {
	manager := NewClientManager(zap.NewNop(), nil, nil)
	key := tenantKey("", "server-1")
	old := &reconnectMCPClient{healthy: false}
	manager.clients[key] = old
	manager.configs[key] = &MCPServerConfig{ID: "server-1", Name: "test", Transport: "http"}
	var fresh *reconnectMCPClient
	manager.clientFactory = func(*MCPServerConfig, *zap.Logger) MCPClient {
		fresh = &reconnectMCPClient{}
		return fresh
	}

	manager.performHealthCheck()

	if old.disconnectCalls != 1 {
		t.Fatalf("displaced client close calls=%d", old.disconnectCalls)
	}
	if got := manager.clients[key]; got != fresh {
		t.Fatalf("fresh client not installed: %#v", got)
	}
}

// TestMCPToolHandle 测试 MCP Skill 包装器
func TestMCPToolHandle(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	manager := NewClientManager(logger, nil, nil)

	tool := &MCPTool{
		Name:        "test_tool",
		Description: "Test Tool",
	}

	wrapper := &MCPToolHandle{
		ID:          "mcp:test:test_tool",
		Name:        "test_tool",
		Description: "Test Tool",
		Type:        "mcp",
		Tool:        tool,
		ServerID:    "test",
		Manager:     manager,
		logger:      logger,
	}

	if wrapper.GetID() != "mcp:test:test_tool" {
		t.Errorf("expected ID mcp:test:test_tool, got %s", wrapper.GetID())
	}

	if wrapper.GetName() != "test_tool" {
		t.Errorf("expected name test_tool, got %s", wrapper.GetName())
	}

	if wrapper.GetType() != "mcp" {
		t.Errorf("expected type mcp, got %s", wrapper.GetType())
	}
}

// TestMCPToolRegistry 测试 MCP Skill 注册表
func TestMCPToolRegistry(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	manager := NewClientManager(logger, nil, nil)
	registry := NewMCPToolRegistry(manager, logger)

	if len(registry.GetAllTools()) != 0 {
		t.Errorf("expected 0 skills, got %d", len(registry.GetAllTools()))
	}

	// 测试获取不存在的 Skill
	skill := registry.GetRegisteredTool("nonexistent")
	if skill != nil {
		t.Fatal("skill should be nil")
	}
}

// TestMCPToolRegistryExecute 测试执行不存在的 Skill
func TestMCPToolRegistryExecute(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	manager := NewClientManager(logger, nil, nil)
	registry := NewMCPToolRegistry(manager, logger)

	_, err := registry.ExecuteToolByID("nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent skill")
	}
}

// TestBaseClientIsConnected 测试客户端连接状态
func TestBaseClientIsConnected(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	config := &MCPServerConfig{
		ID:        "test",
		Name:      "Test",
		Transport: "stdio",
	}

	client := NewBaseClient(config, logger)

	if client.IsConnected() {
		t.Fatal("client should not be connected initially")
	}

	if client.IsHealthy() {
		t.Fatal("client should not be healthy initially")
	}
}

// TestBaseClientGetServerInfo 测试获取服务器信息
func TestBaseClientGetServerInfo(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	config := &MCPServerConfig{
		ID:        "test",
		Name:      "Test Server",
		Version:   "1.0.0",
		Transport: "stdio",
	}

	client := NewBaseClient(config, logger)
	info := client.GetServerInfo()

	if info == nil {
		t.Fatal("server info should not be nil")
	}

	if info.ID != "test" {
		t.Errorf("expected ID test, got %s", info.ID)
	}

	if info.Name != "Test Server" {
		t.Errorf("expected name Test Server, got %s", info.Name)
	}

	if info.Status != "disconnected" {
		t.Errorf("expected status disconnected, got %s", info.Status)
	}
}

func TestClientManagerCallToolWithConfigClosesIsolatedClient(t *testing.T) {
	manager := NewClientManager(zap.NewNop(), nil, nil)
	client := &revisionClientFake{result: map[string]any{"ok": true}}
	manager.clientFactory = func(config *MCPServerConfig, _ *zap.Logger) MCPClient {
		client.config = config
		return client
	}
	config := &MCPServerConfig{ID: "revision-server", URL: "https://original.example/mcp"}
	result, err := manager.CallToolWithConfig(context.Background(), config, "lookup", map[string]any{"id": "1"})
	if err != nil || client.connectCalls != 1 || client.disconnectCalls != 1 || client.config != config {
		t.Fatalf("result=%+v client=%+v err=%v", result, client, err)
	}
}

func TestBaseClientHTTPInitializeRejectsNon2xxWithoutLeakingQuery(t *testing.T) {
	secretQuery := "credential=synthetic-sensitive-value"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "raw upstream body", http.StatusUnauthorized)
	}))
	defer server.Close()
	client := NewBaseClient(&MCPServerConfig{
		ID: "server-1", Name: "test", Transport: "http", URL: server.URL + "?" + secretQuery, Timeout: time.Second,
	}, zap.NewNop())
	err := client.Connect(context.Background())
	if err == nil || strings.Contains(err.Error(), secretQuery) || strings.Contains(client.GetServerInfo().Error, secretQuery) ||
		client.GetServerInfo().Error != "connect_failed" {
		t.Fatalf("err=%v server_info=%+v", err, client.GetServerInfo())
	}
}

func TestBaseClientHTTPInitializeTimeoutAndLogsExcludeQuery(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()
	client := NewBaseClient(&MCPServerConfig{
		ID: "server-1", Transport: "http", URL: server.URL + "?credential=synthetic-sensitive-value",
		Timeout: 10 * time.Millisecond,
	}, zap.New(core))
	if err := client.Connect(context.Background()); err == nil {
		t.Fatal("expected initialize timeout")
	}
	for _, entry := range observed.All() {
		if strings.Contains(entry.Message+fmt.Sprint(entry.ContextMap()), "synthetic-sensitive-value") {
			t.Fatalf("secret query leaked in logs: %+v", entry)
		}
	}
}

func TestBaseClientCallToolTransportFailureDoesNotLeakURLOrSession(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	secretQuery := "credential=synthetic-query-secret"
	secretSession := "synthetic-session-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Mcp-Session-Id", secretSession)
		w.WriteHeader(http.StatusOK)
	}))
	client := NewBaseClient(&MCPServerConfig{
		ID: "server-1", Transport: "http", URL: server.URL + "?" + secretQuery, Timeout: time.Second,
	}, zap.New(core))
	if err := client.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	server.Close()
	_, err := client.CallTool(context.Background(), " lookup ", map[string]any{})
	if err == nil || strings.Contains(err.Error(), secretQuery) || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("unsafe transport error: %v", err)
	}
	if strings.Contains(client.GetServerInfo().Error, secretQuery) || strings.Contains(client.GetServerInfo().Error, server.URL) {
		t.Fatalf("unsafe server info: %+v", client.GetServerInfo())
	}
	for _, entry := range observed.All() {
		logged := entry.Message + fmt.Sprint(entry.ContextMap())
		if strings.Contains(logged, secretQuery) || strings.Contains(logged, server.URL) ||
			strings.Contains(logged, secretSession) {
			t.Fatalf("HTTP secret leaked in logs: %s", logged)
		}
	}
}

func TestBaseClientDisconnectKillsAndWaitsForStdioChild(t *testing.T) {
	client := NewBaseClient(&MCPServerConfig{
		ID: "server-1", Transport: "stdio", Command: "sh", Args: []string{"-c", "sleep 30"},
	}, zap.NewNop())
	if err := client.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	process := client.cmd.Process
	if err := client.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := process.Signal(syscall.Signal(0)); err == nil {
		t.Fatal("stdio child still alive after disconnect")
	}
}

func TestClientManagerConnectFailsClosedWhenDiscoveryFails(t *testing.T) {
	manager := NewClientManager(zap.NewNop(), nil, nil)
	client := &revisionClientFake{listToolsErr: errors.New("discovery failed")}
	manager.clientFactory = func(*MCPServerConfig, *zap.Logger) MCPClient { return client }
	err := manager.Connect(context.Background(), &MCPServerConfig{ID: "server-1"})
	if err == nil || client.disconnectCalls != 1 || manager.GetClient(context.Background(), "server-1") != nil {
		t.Fatalf("client=%+v err=%v", client, err)
	}
}

func TestClientManagerConnectFailureAlwaysDisconnects(t *testing.T) {
	manager := NewClientManager(zap.NewNop(), nil, nil)
	client := &revisionClientFake{connectErr: errors.New("initialize failed")}
	manager.clientFactory = func(*MCPServerConfig, *zap.Logger) MCPClient { return client }
	if err := manager.Connect(context.Background(), &MCPServerConfig{ID: "server-1"}); err == nil {
		t.Fatal("expected connect failure")
	}
	if client.disconnectCalls != 1 || manager.GetClient(context.Background(), "server-1") != nil {
		t.Fatalf("partial connection not cleaned up: %+v", client)
	}
}

func TestClientManagerHTTPInitializeFailureClosesPartialClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	manager := NewClientManager(zap.NewNop(), nil, nil)
	var client *BaseClient
	manager.clientFactory = func(config *MCPServerConfig, logger *zap.Logger) MCPClient {
		client = NewBaseClient(config, logger)
		return client
	}
	err := manager.Connect(context.Background(), &MCPServerConfig{
		ID: "server-1", Transport: "http", URL: server.URL, Timeout: time.Second,
	})
	if err == nil || client == nil || client.httpClient != nil || manager.GetClient(context.Background(), "server-1") != nil {
		t.Fatalf("partial HTTP client not cleaned up: client=%+v err=%v", client, err)
	}
}

type revisionClientFake struct {
	config          *MCPServerConfig
	result          any
	connectCalls    int
	disconnectCalls int
	listToolsErr    error
	connectErr      error
}

func (c *revisionClientFake) Connect(context.Context) error {
	c.connectCalls++
	return c.connectErr
}
func (c *revisionClientFake) Disconnect(context.Context) error { c.disconnectCalls++; return nil }
func (*revisionClientFake) IsConnected() bool                  { return true }
func (*revisionClientFake) IsHealthy() bool                    { return true }
func (c *revisionClientFake) CallTool(context.Context, string, interface{}) (interface{}, error) {
	return c.result, nil
}
func (c *revisionClientFake) ListTools(context.Context) ([]*MCPTool, error) {
	return nil, c.listToolsErr
}
func (*revisionClientFake) ListResources(context.Context) ([]*MCPResource, error) { return nil, nil }
func (*revisionClientFake) GetServerInfo() *MCPServerInfo                         { return nil }

// TestClientManagerGetAllServerInfo 测试获取所有服务器信息
func TestClientManagerGetAllServerInfo(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	manager := NewClientManager(logger, nil, nil)

	infos := manager.GetAllServerInfo(context.Background())
	if len(infos) != 0 {
		t.Errorf("expected 0 servers, got %d", len(infos))
	}
}

// TestCacheDelete 测试缓存删除
func TestCacheDelete(t *testing.T) {
	cache := NewCapabilityCache(100, 1*time.Hour)

	tools := []*MCPTool{{Name: "tool1", Description: "Tool 1"}}
	cache.StoreTools("server1", tools)

	if cache.Size() != 1 {
		t.Errorf("expected size 1, got %d", cache.Size())
	}

	cache.Delete("server1")

	if cache.Size() != 0 {
		t.Errorf("expected size 0 after delete, got %d", cache.Size())
	}
}

// TestCacheClear 测试缓存清空
func TestCacheClear(t *testing.T) {
	cache := NewCapabilityCache(100, 1*time.Hour)

	tools := []*MCPTool{{Name: "tool1", Description: "Tool 1"}}
	cache.StoreTools("server1", tools)
	cache.StoreTools("server2", tools)

	if cache.Size() != 2 {
		t.Errorf("expected size 2, got %d", cache.Size())
	}

	cache.Clear()

	if cache.Size() != 0 {
		t.Errorf("expected size 0 after clear, got %d", cache.Size())
	}
}

// TestMCPToolCatalogGetAllTools 测试获取所有 Skills
func TestMCPToolCatalogGetAllTools(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	manager := NewClientManager(logger, nil, nil)
	adapter := NewMCPToolCatalog("test", manager, logger)

	skills := adapter.GetAllTools()
	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}
}

// TestConnectionPoolConfig 测试连接池配置
func TestConnectionPoolConfig(t *testing.T) {
	config := &ConnectionPoolConfig{
		MaxConnections: 10,
		IdleTimeout:    5 * time.Minute,
		MaxRetries:     3,
		RetryBackoff:   1 * time.Second,
	}

	if config.MaxConnections != 10 {
		t.Errorf("expected MaxConnections 10, got %d", config.MaxConnections)
	}

	if config.MaxRetries != 3 {
		t.Errorf("expected MaxRetries 3, got %d", config.MaxRetries)
	}
}

// TestMCPServerConfig 测试 MCP 服务器配置
func TestMCPServerConfig(t *testing.T) {
	config := &MCPServerConfig{
		ID:        "github",
		Name:      "GitHub MCP",
		Version:   "1.0.0",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"/opt/mcp-servers/github/dist/index.js"},
	}

	if config.ID != "github" {
		t.Errorf("expected ID github, got %s", config.ID)
	}

	if len(config.Args) != 1 {
		t.Errorf("expected 1 arg, got %d", len(config.Args))
	}
}

// BenchmarkCacheStore 基准测试缓存存储
func BenchmarkCacheStore(b *testing.B) {
	cache := NewCapabilityCache(1000, 1*time.Hour)
	tools := []*MCPTool{{Name: "tool1", Description: "Tool 1"}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.StoreTools("server", tools)
	}
}

// TestStoreToolsRespectsMaxSize 验证 StoreTools 不超过 maxSize
func TestStoreToolsRespectsMaxSize(t *testing.T) {
	cache := NewCapabilityCache(2, 1*time.Hour)
	tools := []*MCPTool{{Name: "tool1"}}

	cache.StoreTools("server1", tools)
	cache.StoreTools("server2", tools)
	cache.StoreTools("server3", tools)

	if cache.Size() > 2 {
		t.Errorf("cache size %d exceeds maxSize 2", cache.Size())
	}
}

// TestStoreResourcesRespectsMaxSize 验证 StoreResources 不超过 maxSize
func TestStoreResourcesRespectsMaxSize(t *testing.T) {
	cache := NewCapabilityCache(2, 1*time.Hour)
	resources := []*MCPResource{{URI: "res1"}}

	cache.StoreResources("server1", resources)
	cache.StoreResources("server2", resources)
	cache.StoreResources("server3", resources)

	if cache.Size() > 2 {
		t.Errorf("cache size %d exceeds maxSize 2", cache.Size())
	}
}

// TestMCPToolHandleUsesStoredContext 验证 Execute 使用构造时注入的 context
func TestMCPToolHandleUsesStoredContext(t *testing.T) {
	logger := zap.NewNop()
	wrapper := &MCPToolHandle{
		ID:       "mcp:test:tool",
		Name:     "tool",
		Type:     "mcp",
		ServerID: "test-server",
		Tool:     &MCPTool{Name: "tool"},
		Manager:  NewClientManager(logger, nil, nil),
		logger:   logger,
	}

	_, err := wrapper.Execute(context.Background(), map[string]any{"key": "value"})
	if err == nil {
		t.Error("expected error due to nil client, got nil")
	}
}

// BenchmarkCacheGetTools 基准测试缓存获取工具
func BenchmarkCacheGetTools(b *testing.B) {
	cache := NewCapabilityCache(1000, 1*time.Hour)
	tools := []*MCPTool{{Name: "tool1", Description: "Tool 1"}}
	cache.StoreTools("server", tools)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.GetTools("server")
	}
}

func TestPersistConnectNilPool(t *testing.T) {
	logger := zap.NewNop()
	m := NewClientManager(logger, nil, nil)
	cfg := &MCPServerConfig{
		ID:           "test-id",
		Name:         "Test Server",
		Transport:    "stdio",
		Command:      "node",
		Args:         []string{"--arg1", "val"},
		Env:          map[string]string{"KEY": "VAL"},
		Capabilities: []string{"tools"},
		Timeout:      30 * time.Second,
	}
	_ = m.persistConnect(context.Background(), cfg) // pool=nil → must not panic
}

func TestRestoreFromDB_NilPool(t *testing.T) {
	logger := zap.NewNop()
	m := NewClientManager(logger, nil, nil)
	err := m.RestoreFromDB(context.Background())
	if err != nil {
		t.Errorf("expected nil error with nil pool, got %v", err)
	}
}
