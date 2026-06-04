package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestMCPIntegration 测试 MCP 系统的端到端集成
func TestMCPIntegration(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer func() { _ = logger.Sync() }()

	// 创建客户端管理器
	manager := NewClientManager(logger, nil, nil)

	// 创建技能注册表
	registry := NewMCPSkillRegistry(manager, logger)

	// 验证初始状态
	if len(registry.GetAllSkills()) != 0 {
		t.Errorf("expected 0 skills initially, got %d", len(registry.GetAllSkills()))
	}

	// 创建测试配置
	config := &MCPServerConfig{
		ID:        "test-server",
		Name:      "Test Server",
		Version:   "1.0.0",
		Transport: "http",
		URL:       "http://localhost:3000",
		Timeout:   5 * time.Second,
	}

	// 创建客户端
	client := NewBaseClient(config, logger)

	// 验证客户端初始状态
	if client.IsConnected() {
		t.Fatal("client should not be connected initially")
	}

	if client.IsHealthy() {
		t.Fatal("client should not be healthy initially")
	}

	// 获取服务器信息
	info := client.GetServerInfo()
	if info == nil {
		t.Fatal("server info should not be nil")
	}

	if info.Status != "disconnected" {
		t.Errorf("expected status disconnected, got %s", info.Status)
	}

	// 测试缓存
	cache := NewCapabilityCache(100, 1*time.Hour)

	tools := []*MCPTool{
		{Name: "tool1", Description: "Tool 1"},
		{Name: "tool2", Description: "Tool 2"},
	}

	resources := []*MCPResource{
		{URI: "res1", Name: "Resource 1"},
	}

	cache.Store("test-server", tools, resources)

	// 验证缓存
	cachedTools, ok := cache.GetTools("test-server")
	if !ok {
		t.Fatal("tools should be in cache")
	}

	if len(cachedTools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(cachedTools))
	}

	cachedResources, ok := cache.GetResources("test-server")
	if !ok {
		t.Fatal("resources should be in cache")
	}

	if len(cachedResources) != 1 {
		t.Errorf("expected 1 resource, got %d", len(cachedResources))
	}

	// 测试技能适配器
	adapter := NewMCPSkillAdapter("test-server", manager, logger)

	// 验证适配器初始状态
	if len(adapter.GetAllSkills()) != 0 {
		t.Errorf("expected 0 skills initially, got %d", len(adapter.GetAllSkills()))
	}

	// 测试连接池配置
	poolConfig := &ConnectionPoolConfig{
		MaxConnections: 10,
		IdleTimeout:    5 * time.Minute,
		MaxRetries:     3,
		RetryBackoff:   1 * time.Second,
	}

	if poolConfig.MaxConnections != 10 {
		t.Errorf("expected MaxConnections 10, got %d", poolConfig.MaxConnections)
	}

	// 测试缓存配置
	cacheConfig := &CacheConfig{
		Enabled: true,
		TTL:     3600 * time.Second,
		MaxSize: 1000,
	}

	if !cacheConfig.Enabled {
		t.Fatal("cache should be enabled")
	}

	// 测试监控配置
	monitoringConfig := &MonitoringConfig{
		Enabled:             true,
		MetricsInterval:     30 * time.Second,
		HealthCheckInterval: 30 * time.Second,
	}

	if !monitoringConfig.Enabled {
		t.Fatal("monitoring should be enabled")
	}

	// 测试 MCP 配置
	mcpConfig := &MCPConfig{
		Servers:        []*MCPServerConfig{config},
		ConnectionPool: poolConfig,
		Cache:          cacheConfig,
		Monitoring:     monitoringConfig,
	}

	if len(mcpConfig.Servers) != 1 {
		t.Errorf("expected 1 server, got %d", len(mcpConfig.Servers))
	}

	t.Log("MCP integration test passed")
}

// TestMCPSkillExecutionFlow 测试技能执行流程
func TestMCPSkillExecutionFlow(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer func() { _ = logger.Sync() }()

	manager := NewClientManager(logger, nil, nil)

	// 创建测试工具
	tool := &MCPTool{
		Name:        "test_tool",
		Description: "Test Tool",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"input": map[string]interface{}{"type": "string"},
			},
		},
	}

	// 创建技能包装器
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

	// 验证技能属性
	if wrapper.GetID() != "mcp:test:test_tool" {
		t.Errorf("expected ID mcp:test:test_tool, got %s", wrapper.GetID())
	}

	if wrapper.GetName() != "test_tool" {
		t.Errorf("expected name test_tool, got %s", wrapper.GetName())
	}

	if wrapper.GetType() != "mcp" {
		t.Errorf("expected type mcp, got %s", wrapper.GetType())
	}

	if wrapper.GetDescription() != "Test Tool" {
		t.Errorf("expected description Test Tool, got %s", wrapper.GetDescription())
	}

	t.Log("MCP skill execution flow test passed")
}

// TestMCPCacheExpiration 测试缓存过期机制
func TestMCPCacheExpiration(t *testing.T) {
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

	t.Log("MCP cache expiration test passed")
}

// TestSSETransportFunctional 验证 SSE transport 可以正常发送请求
func TestSSETransportFunctional(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(MCPResponse{
			Result: json.RawMessage(`[]`),
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	logger, _ := zap.NewDevelopment()
	cfg := &MCPServerConfig{
		ID:        "test-sse",
		Transport: "sse",
		URL:       srv.URL,
		Timeout:   5 * time.Second,
	}
	client := NewBaseClient(cfg, logger)

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools via SSE failed: %v", err)
	}
	_ = tools
}

// TestHealthCheckDoesNotBlockConcurrentReads 验证 HealthCheck 不阻塞并发 ListTools
func TestHealthCheckDoesNotBlockConcurrentReads(t *testing.T) {
	var slowOnce sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/rpc", func(w http.ResponseWriter, r *http.Request) {
		slowOnce.Do(func() {
			time.Sleep(200 * time.Millisecond)
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(MCPResponse{Result: json.RawMessage(`[]`)})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	logger, _ := zap.NewDevelopment()
	cfg := &MCPServerConfig{
		ID:        "test-hc-concurrent",
		Transport: "http",
		URL:       srv.URL,
		Timeout:   5 * time.Second,
	}
	client := NewBaseClient(cfg, logger)
	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	start := time.Now()
	go func() {
		defer wg.Done()
		client.HealthCheck(ctx) //nolint:errcheck,gosec
	}()
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		client.ListTools(ctx) //nolint:errcheck,gosec
	}()
	wg.Wait()

	elapsed := time.Since(start)
	if elapsed > 300*time.Millisecond {
		t.Errorf("HealthCheck blocked concurrent ListTools for %v (expected < 300ms)", elapsed)
	}
}
