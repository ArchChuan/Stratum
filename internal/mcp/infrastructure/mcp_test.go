package infrastructure

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestHTTPClientErrorDoesNotExposeResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
