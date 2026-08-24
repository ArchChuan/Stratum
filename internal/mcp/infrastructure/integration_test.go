package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// TestStreamableHTTPClientPropagatesMCPAndTraceHeaders 验证 SDK streamable
// HTTP 客户端的所有请求都携带租户配置的 Authorization 与 otel trace 头。
// 服务端是观察型 handler（非 SDK server）：POST 按 JSON-RPC 回包，GET 以
// text/event-stream 挂起直到连接关闭。SDK 的请求序列与自研握手不同：
// initialize POST → standalone SSE GET（服务端→客户端通知通道，保持打开）
// → notifications/initialized → tools/list → tools/call。
func TestStreamableHTTPClientPropagatesMCPAndTraceHeaders(t *testing.T) {
	type observedRequest struct {
		method  string
		headers http.Header
		request MCPRequest
	}

	var (
		mu       sync.Mutex
		observed []observedRequest
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			// Disconnect 时 SDK 发送 DELETE 结束会话；不记录，也不参与断言。
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if r.Method == http.MethodGet {
			// Standalone SSE 流：必须在写头后 Flush，否则 SDK 客户端会一直
			// 等响应头；随后保持打开直到连接关闭。
			mu.Lock()
			observed = append(observed, observedRequest{method: r.Method, headers: r.Header.Clone()})
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		var request MCPRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid JSON-RPC request", http.StatusBadRequest)
			return
		}
		mu.Lock()
		observed = append(observed, observedRequest{method: r.Method, headers: r.Header.Clone(), request: request})
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if request.Method == "initialize" {
			w.Header().Set("MCP-Session-Id", "session-1")
			_ = json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "protocol-contract", "version": "1.0.0"},
			}})
			return
		}
		if request.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		result := map[string]any{}
		if request.Method == "tools/list" {
			result["tools"] = []any{}
		}
		if request.Method == "tools/call" {
			result["content"] = []any{}
		}
		_ = json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: request.ID, Result: result})
	}))
	t.Cleanup(server.Close)

	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}))
	t.Cleanup(func() { otel.SetTextMapPropagator(previousPropagator) })
	traceState, err := trace.ParseTraceState("vendor=value")
	if err != nil {
		t.Fatal(err)
	}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{
			0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
			0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
		},
		SpanID:     trace.SpanID{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18},
		TraceFlags: trace.FlagsSampled,
		TraceState: traceState,
		Remote:     true,
	})
	ctx := trace.ContextWithRemoteSpanContext(context.Background(), spanContext)

	client := NewBaseClient(&MCPServerConfig{
		ID:        "protocol-contract",
		Name:      "protocol-contract",
		Transport: "streamable-http",
		URL:       server.URL,
		Headers:   map[string]string{"Authorization": "Bearer invocation-token"},
		Timeout:   time.Second,
	}, zap.NewNop())
	client.urlPolicy = URLPolicyAllowPrivate
	if _, err := client.ListTools(ctx); err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if _, err := client.CallTool(ctx, "read_order", map[string]any{"id": "order-1"}); err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	// 主动关闭会话以结束 standalone SSE 流，否则 server.Close 会因连接仍
	// 处于 active 而阻塞。
	if err := client.Disconnect(ctx); err != nil {
		t.Fatalf("Disconnect failed: %v", err)
	}

	mu.Lock()
	requests := append([]observedRequest(nil), observed...)
	mu.Unlock()

	var sseGets, posts []observedRequest
	for _, request := range requests {
		if request.method == http.MethodGet {
			sseGets = append(sseGets, request)
			continue
		}
		posts = append(posts, request)
	}
	if len(sseGets) < 1 {
		t.Fatalf("observed %d standalone SSE GETs, want >= 1", len(sseGets))
	}
	if len(posts) != 4 {
		t.Fatalf("observed %d POST requests, want initialize, notifications/initialized, tools/list, tools/call", len(posts))
	}

	// 所有请求（含 standalone SSE GET）都必须携带租户凭据和 otel 注入的
	// trace 头。secureRoundTripper 对同源请求统一注入，与请求类型无关。
	all := append(append([]observedRequest(nil), sseGets...), posts...)
	for i, request := range all {
		if got := request.headers.Get("Authorization"); got != "Bearer invocation-token" {
			t.Errorf("request %d Authorization = %q, want Bearer invocation-token", i, got)
		}
		if got := request.headers.Get("Traceparent"); got != "00-0102030405060708090a0b0c0d0e0f10-1112131415161718-01" {
			t.Errorf("request %d traceparent = %q", i, got)
		}
		if got := request.headers.Get("Tracestate"); got != "vendor=value" {
			t.Errorf("request %d tracestate = %q", i, got)
		}
	}

	wantMethods := []string{"initialize", "notifications/initialized", "tools/list", "tools/call"}
	for i, request := range posts {
		if request.method != http.MethodPost {
			t.Errorf("request %d HTTP method = %q, want POST", i, request.method)
		}
		if request.request.JSONRPC != "2.0" || request.request.Method != wantMethods[i] {
			t.Errorf("request %d JSON-RPC = %q %q, want 2.0 %q", i, request.request.JSONRPC,
				request.request.Method, wantMethods[i])
		}
		if got := request.headers.Get("Accept"); got != "application/json, text/event-stream" {
			t.Errorf("request %d Accept = %q", i, got)
		}
		if got := request.headers.Get("Content-Type"); got != "application/json" {
			t.Errorf("request %d Content-Type = %q", i, got)
		}
	}

	// MCP-Protocol-Version 与 MCP-Session-Id 由 SDK 管理：握手后（含 SSE GET）
	// 每次请求都会带上协商版本与 session。initialize 请求本身由 SDK 在
	// setMCPHeaders 中不设置版本头（此时尚未协商）。
	for i, request := range append(append([]observedRequest(nil), sseGets...), posts[1:]...) {
		if got := request.headers.Get("MCP-Protocol-Version"); got != "2025-06-18" {
			t.Errorf("request %d MCP-Protocol-Version = %q", i, got)
		}
		if got := request.headers.Get("MCP-Session-Id"); got != "session-1" {
			t.Errorf("request %d MCP-Session-Id = %q", i, got)
		}
	}

	params, ok := posts[0].request.Params.(map[string]any)
	if !ok || params["protocolVersion"] != "2025-06-18" {
		t.Errorf("initialize protocolVersion = %#v, want 2025-06-18", posts[0].request.Params)
	}
}

func TestStreamableHTTPClientRejectsInitializeJSONRPCError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(MCPResponse{
			JSONRPC: "2.0",
			ID:      1,
			Error:   json.RawMessage(`{"code":-32603,"message":"initialization rejected"}`),
		})
	}))
	t.Cleanup(server.Close)

	client := NewBaseClient(&MCPServerConfig{
		ID: "initialize-error", Name: "initialize-error", Transport: "streamable-http",
		URL: server.URL, Timeout: time.Second,
	}, zap.NewNop())
	client.urlPolicy = URLPolicyAllowPrivate
	if err := client.Connect(context.Background()); err == nil {
		t.Fatal("Connect succeeded after initialize JSON-RPC error")
	}
}

func TestStreamableHTTPClientRejectsUnsupportedSelectedProtocolVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: 1, Result: map[string]any{
			// SDK 支持列表是 ["2026-07-28","2025-11-25","2025-06-18",
			// "2025-03-26","2024-11-05"]；"2025-11-25" 本身受支持，必须回显
			// 一个列表外的版本才能触发客户端严格成员检查。
			"protocolVersion": "2025-11-26",
			"capabilities":    map[string]any{},
			"serverInfo":      map[string]any{"name": "incompatible", "version": "1.0.0"},
		}})
	}))
	t.Cleanup(server.Close)

	client := NewBaseClient(&MCPServerConfig{
		ID: "unsupported-version", Name: "unsupported-version", Transport: "streamable-http",
		URL: server.URL, Timeout: time.Second,
	}, zap.NewNop())
	client.urlPolicy = URLPolicyAllowPrivate
	if err := client.Connect(context.Background()); err == nil {
		t.Fatal("Connect succeeded with unsupported selected protocol version")
	}
}

// TestBaseClientAgainstDeterministicFakeServer 用官方 SDK server 验证
// BaseClient 的 ListTools / CallTool 全链路。原 fake_server（testserver 包）
// 将被删除，这里以 mcp.NewServer + mcp.NewStreamableHTTPHandler 取代：
//   - 成功分支：ToolHandler 返回结构化结果，CallTool 成功且结果含
//     structuredContent；
//   - 协议错误分支：ToolHandler 返回 error → SDK server 回 JSON-RPC 错误响应
//     → 客户端 CallTool 返回错误（isError 结果不是客户端可见错误，只有
//     handler 返回 error 才表达协议错误）；
//   - 断连分支：外层包装 handler 截获 tools/call POST，hijack 后直接关连接，
//     客户端得到传输级 EOF（与协议错误区分开）。
func TestBaseClientAgainstDeterministicFakeServer(t *testing.T) {
	const (
		modeOK = iota
		modeProtocolError
		modeDisconnect
	)
	var (
		mode     atomic.Int32
		attempts atomic.Int32
	)
	srv := mcp.NewServer(&mcp.Implementation{Name: "stratum-fake-mcp", Version: "1.0"}, nil)
	srv.AddTool(&mcp.Tool{
		Name:         "read_order",
		Description:  "read an order",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		attempts.Add(1)
		if mode.Load() == modeProtocolError {
			return nil, errors.New("injected protocol error")
		}
		return &mcp.CallToolResult{StructuredContent: map[string]any{"id": "order-1"}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && mode.Load() == modeDisconnect {
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "tools/call") {
				if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
					_ = conn.Close()
					return
				}
				http.Error(w, "hijack failed", http.StatusInternalServerError)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	client := NewBaseClient(&MCPServerConfig{
		ID: "fake", Name: "fake", Transport: "streamable-http", URL: server.URL, Timeout: time.Second,
	}, zap.NewNop())
	client.urlPolicy = URLPolicyAllowPrivate
	if err := client.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	tools, err := client.ListTools(context.Background())
	if err != nil || len(tools) != 1 || tools[0].Name != "read_order" {
		t.Fatalf("tools=%#v err=%v", tools, err)
	}
	result, err := client.CallTool(context.Background(), "read_order", map[string]any{"id": "order-1"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if !strings.Contains(string(encoded), "structuredContent") || attempts.Load() != 1 {
		t.Fatalf("result=%s attempts=%d", encoded, attempts.Load())
	}

	mode.Store(modeProtocolError)
	if _, err := client.CallTool(context.Background(), "read_order", map[string]any{}); err == nil {
		t.Fatal("protocol error unexpectedly succeeded")
	}
	mode.Store(modeDisconnect)
	if _, err := client.CallTool(context.Background(), "read_order", map[string]any{}); err == nil {
		t.Fatal("disconnect unexpectedly succeeded")
	}
	// 关闭会话以结束 SDK server 的 standalone SSE 流，让 server.Close 收尾。
	if err := client.Disconnect(context.Background()); err != nil {
		t.Fatalf("Disconnect failed: %v", err)
	}
}

// TestMCPIntegration 测试 MCP 系统的端到端集成
func TestMCPIntegration(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer func() { _ = logger.Sync() }()

	// 创建客户端管理器
	manager := NewClientManager(logger, nil, nil)

	// 创建技能注册表
	registry := NewMCPToolRegistry(manager, logger)

	// 验证初始状态：未注册 server 无 catalog。
	if got := registry.GetCatalogForServer("t1", "test-server"); got != nil {
		t.Errorf("expected nil catalog initially, got %v", got)
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
	adapter := NewMCPToolCatalog("t1", "test-server", manager, logger)

	// 验证适配器初始状态
	if len(adapter.GetAllTools()) != 0 {
		t.Errorf("expected 0 skills initially, got %d", len(adapter.GetAllTools()))
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

// TestMCPToolExecutionFlow 测试技能执行流程
func TestMCPToolExecutionFlow(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer func() { _ = logger.Sync() }()

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
	wrapper := &MCPToolHandle{
		ID:          "mcp:test:test_tool",
		Name:        "test_tool",
		Description: "Test Tool",
		Type:        "mcp",
		Tool:        tool,
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

	t.Log("MCP tool execution flow test passed")
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
	t.Skip("SSE transport not implemented; client supports stdio/http/streamable-http only")
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

// TestHealthCheckDoesNotBlockConcurrentReads 验证 HealthCheck（协议 ping）
// 不阻塞并发 ListTools。SDK 客户端本身并发安全无锁；这里在服务端让首个
// ping 延迟 200ms，验证慢 ping 期间 ListTools 照常完成，总耗时不受 ping
// 延迟之外的影响。
func TestHealthCheckDoesNotBlockConcurrentReads(t *testing.T) {
	var slowOnce sync.Once
	srv := mcp.NewServer(&mcp.Implementation{Name: "stratum-hc", Version: "1.0"}, nil)
	srv.AddTool(&mcp.Tool{
		Name:        "echo",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"ok": true}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), `"ping"`) {
				slowOnce.Do(func() { time.Sleep(200 * time.Millisecond) })
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	client := NewBaseClient(&MCPServerConfig{
		ID: "test-hc-concurrent", Transport: "streamable-http", URL: server.URL, Timeout: 5 * time.Second,
	}, zap.NewNop())
	client.urlPolicy = URLPolicyAllowPrivate
	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	var hcErr, listErr error
	start := time.Now()
	go func() {
		defer wg.Done()
		hcErr = client.HealthCheck(ctx)
	}()
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		_, listErr = client.ListTools(ctx)
	}()
	wg.Wait()

	elapsed := time.Since(start)
	if hcErr != nil || listErr != nil {
		t.Errorf("HealthCheck=%v ListTools=%v, want both nil", hcErr, listErr)
	}
	if elapsed > 300*time.Millisecond {
		t.Errorf("HealthCheck blocked concurrent ListTools for %v (expected < 300ms)", elapsed)
	}
	// 关闭会话以结束 SDK server 的 standalone SSE 流，让 server.Close 收尾。
	if err := client.Disconnect(context.Background()); err != nil {
		t.Fatalf("Disconnect failed: %v", err)
	}
}

// TestBaseClientHealthCheckRefreshesLastActivity 验证保活 B：协议 ping 成功后
// 刷新 lastActivity。仅被 health check ping 而无工具调用的连接，若 ping 不刷新
// lastActivity，会在 MCPIdleTimeout 后被 idle eviction 按 LastActivity 误驱逐，
// 导致 agent 下次工具调用需重建连接。
func TestBaseClientHealthCheckRefreshesLastActivity(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "stratum-hc-refresh", Version: "1.0"}, nil)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client := NewBaseClient(&MCPServerConfig{
		ID: "test-hc-refresh", Transport: "streamable-http", URL: server.URL, Timeout: 5 * time.Second,
	}, zap.NewNop())
	client.urlPolicy = URLPolicyAllowPrivate
	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// 模拟长时间仅被 ping 保活、即将被 idle eviction 命中的状态。
	stale := time.Now().Add(-10 * time.Minute)
	client.mu.Lock()
	client.lastActivity = stale
	client.mu.Unlock()

	if err := client.HealthCheck(ctx); err != nil {
		t.Fatalf("HealthCheck failed: %v", err)
	}
	if after := client.LastActivity(); !after.After(stale) {
		t.Fatalf("HealthCheck must refresh lastActivity: before=%v after=%v", stale, after)
	}

	// 失败分支：连接关闭后 HealthCheck 报错，lastActivity 不得被刷新，
	// 否则失败的 ping 会掩盖真实空闲时间、推迟 idle eviction。
	if err := client.Disconnect(context.Background()); err != nil {
		t.Fatalf("Disconnect failed: %v", err)
	}
	client.mu.Lock()
	client.lastActivity = stale
	client.mu.Unlock()
	if err := client.HealthCheck(context.Background()); err == nil {
		t.Fatalf("HealthCheck on disconnected client must fail")
	}
	if got := client.LastActivity(); !got.Equal(stale) {
		t.Fatalf("failed HealthCheck must not refresh lastActivity: got=%v want=%v", got, stale)
	}
}
