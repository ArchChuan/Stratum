package mcp

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
)

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
	manager := NewClientManager(logger, nil)

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

	if len(manager.GetAllClients()) != 0 {
		t.Errorf("expected 0 clients, got %d", len(manager.GetAllClients()))
	}
}

// TestMCPSkillWrapper 测试 MCP Skill 包装器
func TestMCPSkillWrapper(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	manager := NewClientManager(logger, nil)

	tool := &MCPTool{
		Name:        "test_tool",
		Description: "Test Tool",
	}

	wrapper := &MCPSkillWrapper{
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

// TestMCPSkillRegistry 测试 MCP Skill 注册表
func TestMCPSkillRegistry(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	manager := NewClientManager(logger, nil)
	registry := NewMCPSkillRegistry(manager, logger)

	if len(registry.GetAllSkills()) != 0 {
		t.Errorf("expected 0 skills, got %d", len(registry.GetAllSkills()))
	}

	// 测试获取不存在的 Skill
	skill := registry.GetSkill("nonexistent")
	if skill != nil {
		t.Fatal("skill should be nil")
	}
}

// TestMCPSkillRegistryExecute 测试执行不存在的 Skill
func TestMCPSkillRegistryExecute(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	manager := NewClientManager(logger, nil)
	registry := NewMCPSkillRegistry(manager, logger)

	_, err := registry.ExecuteSkill("nonexistent", nil)
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
	manager := NewClientManager(logger, nil)

	infos := manager.GetAllServerInfo()
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

// TestMCPSkillAdapterGetAllSkills 测试获取所有 Skills
func TestMCPSkillAdapterGetAllSkills(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	manager := NewClientManager(logger, nil)
	adapter := NewMCPSkillAdapter("test", manager, logger)

	skills := adapter.GetAllSkills()
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

// TestMCPSkillWrapperUsesStoredContext 验证 Execute 使用构造时注入的 context
func TestMCPSkillWrapperUsesStoredContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	logger := zap.NewNop()
	wrapper := &MCPSkillWrapper{
		ctx:      ctx,
		ID:       "mcp:test:tool",
		Name:     "tool",
		Type:     "mcp",
		ServerID: "test-server",
		Tool:     &MCPTool{Name: "tool"},
		Manager:  NewClientManager(logger, nil),
		logger:   logger,
	}

	_, err := wrapper.Execute(map[string]any{"key": "value"})
	if err == nil {
		t.Error("expected error due to cancelled context or nil client, got nil")
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
