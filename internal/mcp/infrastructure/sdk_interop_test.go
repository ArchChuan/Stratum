package infrastructure

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// interopServer hosts a real SDK streamable-HTTP server behind an outer
// handler that lets tests stage network faults (transient 503 on the SSE
// connect, session-expired 404, transient 429 on tools/call) and capture
// every request's headers for credential assertions.
type interopServer struct {
	ts *httptest.Server

	// headerMu guards captured; captured holds the headers of the most
	// recent request (assertions run after the call completes).
	headerMu sync.Mutex
	captured http.Header

	// gateFirstSSE503 makes the first SSE (GET) request return 503 so the
	// client's connect retry budget is exercised.
	gateFirstSSE503 atomic.Bool
	// failCalls holds how many tools/call requests should return 429 before
	// succeeding.
	failCalls atomic.Int32
	// expireSession makes every request return 404 (session gone).
	expireSession atomic.Bool
}

// newInteropServer starts an SDK server with an echo tool and a
// switchable-failure outer wrapper.
func newInteropServer(t *testing.T) *interopServer {
	t.Helper()
	s := &interopServer{}

	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "stratum-test", Version: "1.0"}, nil)
	mcpSrv.AddTool(&mcp.Tool{
		Name:        "echo",
		Description: "echoes the value argument",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"value"},
			"properties": map[string]any{
				"value": map[string]any{"type": "string"},
			},
		},
	}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args map[string]any
		_ = json.Unmarshal(req.Params.Arguments, &args)
		value, _ := args["value"].(string)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "echo:" + value}}}, nil
	})
	mcpSrv.AddTool(&mcp.Tool{
		Name:        "explode",
		Description: "always returns a tool-level error",
		InputSchema: map[string]any{"type": "object"},
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "boom"}}}, nil
	})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil)
	s.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.headerMu.Lock()
		s.captured = r.Header.Clone()
		s.headerMu.Unlock()

		if s.expireSession.Load() {
			// 404 means the session id on the request is unknown to the
			// server (deleted/expired).
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodGet && s.gateFirstSSE503.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.Method == http.MethodPost && s.failCalls.Load() > 0 && strings.Contains(r.URL.Path, "call") {
			s.failCalls.Add(-1)
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(s.ts.Close)
	return s
}

func (s *interopServer) lastHeader(t *testing.T) http.Header {
	t.Helper()
	s.headerMu.Lock()
	defer s.headerMu.Unlock()
	require.NotNil(t, s.captured, "no request captured")
	return s.captured.Clone()
}

// newInteropClient builds a BaseClient pointed at the interop server. The
// test-only urlPolicy flips the SSRF gate so httptest's loopback address is
// reachable; production construction never sets it.
func newInteropClient(t *testing.T, url string) *BaseClient {
	t.Helper()
	c := NewBaseClient(&MCPServerConfig{
		ID: "interop-server", Name: "interop", Version: "1.0",
		Transport: "streamable-http",
		URL:       url,
	}, zap.NewNop())
	c.urlPolicy = URLPolicyAllowPrivate
	return c
}

// TestInteropInitializeHandshake 验证真实 SDK server 握手:session id 分配、
// 协议版本回显、服务器信息暴露。
func TestInteropInitializeHandshake(t *testing.T) {
	s := newInteropServer(t)
	c := newInteropClient(t, s.ts.URL)
	require.NoError(t, c.Connect(context.Background()))
	t.Cleanup(func() { _ = c.Disconnect(context.Background()) })

	require.True(t, c.IsConnected())
	require.True(t, c.IsHealthy())
	info := c.GetServerInfo()
	require.Equal(t, "connected", info.Status)
	require.Equal(t, constants.MCPProtocolVersion, info.Protocol)
	require.NotEmpty(t, c.session.ID(), "server must allocate a session id")
	// GetServerInfo 暴露的是本地 config 快照;服务器真实身份在握手结果里。
	require.Equal(t, "interop", info.Name)
	require.Equal(t, "1.0", info.Version)
	require.Equal(t, "stratum-test", c.session.InitializeResult().ServerInfo.Name)
}

// TestInteropLegacyVersionNegotiatesUp 验证客户端显式请求支持列表外的
// 过旧版本:SDK server 的 negotiatedVersion 静默降级到 2025-11-25 回显,
// 连接成功——这是官方协商语义(服务器兼容旧客户端),不是拒绝场景。
func TestInteropLegacyVersionNegotiatesUp(t *testing.T) {
	s := newInteropServer(t)
	c := newInteropClient(t, s.ts.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, connectWithVersion(ctx, c, "1999-01-01"))
	t.Cleanup(func() { _ = c.Disconnect(context.Background()) })
	require.True(t, c.IsConnected())
	require.Equal(t, "2025-11-25", c.session.InitializeResult().ProtocolVersion,
		"server falls back to the latest legacy version when the client version is unknown")
}

// TestInteropExplicitVersionEchoed 验证 pin 2025-06-18 时服务器原样回显
// (在支持列表且 < 2025-11-25 → negotiatedVersion 返回客户端版本)。
func TestInteropExplicitVersionEchoed(t *testing.T) {
	s := newInteropServer(t)
	c := newInteropClient(t, s.ts.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, connectWithVersion(ctx, c, constants.MCPProtocolVersion))
	t.Cleanup(func() { _ = c.Disconnect(context.Background()) })
	require.Equal(t, constants.MCPProtocolVersion, c.session.InitializeResult().ProtocolVersion)
}

// TestInteropUnknownServerVersionRejected 验证服务器回显客户端不认识
// 的版本时客户端干净报错(client.go unsupportedProtocolVersionError),
// 不留半连接。SDK server 无法构造该场景(自身协商恒合法),用 raw
// JSON-RPC handler 伪造 initialize 回显版本。
func TestInteropUnknownServerVersionRejected(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = r.Body.Close()
		if r.Method != http.MethodPost || req.Method != "initialize" {
			http.Error(w, "unsupported", http.StatusNotFound)
			return
		}
		// 回显一个客户端 supportedProtocolVersions 之外的版本。
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` +
			strconv.Itoa(req.ID) +
			`,"result":{"protocolVersion":"9999-01-01","capabilities":{},"serverInfo":{"name":"rogue","version":"1.0"}}}`))
	}))
	t.Cleanup(ts.Close)

	c := newInteropClient(t, ts.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := connectWithVersion(ctx, c, constants.MCPProtocolVersion)
	require.Error(t, err, "server echoing an unsupported version must fail the connection")
	require.False(t, c.IsConnected(), "failed negotiation must not leave a connected client")
}

// connectWithVersion connects c with an explicit protocol version override.
// It mirrors doConnect's session construction so the negotiation matrix can
// be exercised without touching production paths.
func connectWithVersion(ctx context.Context, c *BaseClient, version string) error {
	origin, err := ValidateMCPURL(c.config.URL)
	if err != nil {
		return err
	}
	sdkClient := mcp.NewClient(&mcp.Implementation{Name: "stratum", Version: "1.0"}, nil)
	session, err := sdkClient.Connect(ctx, newSDKTransport(origin, c.config, c.urlPolicy),
		&mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		return translateSDKError(err)
	}
	c.session = session
	c.sdk = sdkClient
	// doConnect 才会置位 connected;helper 直连 SDK,手动镜像连接态。
	c.mu.Lock()
	c.connected = true
	c.healthy = true
	c.mu.Unlock()
	return nil
}

// TestInteropToolCallRealExecution 验证 tools/call 真实执行:参数到达
// handler、结果回传。
func TestInteropToolCallRealExecution(t *testing.T) {
	s := newInteropServer(t)
	c := newInteropClient(t, s.ts.URL)
	require.NoError(t, c.Connect(context.Background()))
	t.Cleanup(func() { _ = c.Disconnect(context.Background()) })

	tools, err := c.ListTools(context.Background())
	require.NoError(t, err)
	require.Len(t, tools, 2)
	var echo *MCPTool
	for _, tl := range tools {
		if tl.Name == "echo" {
			echo = tl
		}
	}
	require.NotNil(t, echo, "echo tool must be listed")
	require.Contains(t, echo.Description, "echoes")
	require.Contains(t, echo.InputSchema, "properties")

	result, err := c.CallTool(context.Background(), "echo", map[string]any{"value": "hello"})
	require.NoError(t, err)
	callResult, ok := result.(*mcp.CallToolResult)
	require.True(t, ok)
	require.False(t, callResult.IsError)
	require.Len(t, callResult.Content, 1)
	text, ok := callResult.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "echo:hello", text.Text)
}

// TestInteropToolErrorSurfacesIsError 验证工具实现返回的 isError 标志
// 不丢失:错误作为结果内容回传而不是连接级失败。
func TestInteropToolErrorSurfacesIsError(t *testing.T) {
	s := newInteropServer(t)
	c := newInteropClient(t, s.ts.URL)
	require.NoError(t, c.Connect(context.Background()))
	t.Cleanup(func() { _ = c.Disconnect(context.Background()) })

	result, err := c.CallTool(context.Background(), "explode", map[string]any{})
	require.NoError(t, err)
	callResult, ok := result.(*mcp.CallToolResult)
	require.True(t, ok)
	require.True(t, callResult.IsError)
	text, ok := callResult.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "boom", text.Text)
}

// TestInteropTransient429KeepsSession 验证瞬态 429 不消耗重连预算:
// 会话存活,重试成功,连接不重建。
func TestInteropTransient429KeepsSession(t *testing.T) {
	s := newInteropServer(t)
	c := newInteropClient(t, s.ts.URL)
	require.NoError(t, c.Connect(context.Background()))
	t.Cleanup(func() { _ = c.Disconnect(context.Background()) })

	sessionBefore := c.session.ID()
	s.failCalls.Store(2)
	for range 3 {
		_, err := c.CallTool(context.Background(), "echo", map[string]any{"value": "retry"})
		require.NoError(t, err, "transient 429 must retry within the session")
	}
	require.Equal(t, sessionBefore, c.session.ID(), "429 must not rebuild the session")
	require.True(t, c.IsHealthy())
}

// TestInteropSSEConnectRetriesOn503 验证 standalone SSE 首次连接 503:
// SDK connectSSE 重试预算生效,最终连接成功且工具可用。
func TestInteropSSEConnectRetriesOn503(t *testing.T) {
	s := newInteropServer(t)
	s.gateFirstSSE503.Store(true)
	c := newInteropClient(t, s.ts.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	t.Cleanup(func() { _ = c.Disconnect(context.Background()) })

	_, err := c.CallTool(context.Background(), "echo", map[string]any{"value": "after-retry"})
	require.NoError(t, err)
	require.True(t, c.IsHealthy())
}

// TestInteropSessionExpiredMapsToErrSessionMissing 验证服务器 404
// (会话过期/删除)翻译为 ErrSessionMissing 并标记 unhealthy——这是
// manager 单飞重连的触发输入。
func TestInteropSessionExpiredMapsToErrSessionMissing(t *testing.T) {
	s := newInteropServer(t)
	c := newInteropClient(t, s.ts.URL)
	require.NoError(t, c.Connect(context.Background()))
	t.Cleanup(func() { _ = c.Disconnect(context.Background()) })
	require.True(t, c.IsHealthy())

	s.expireSession.Store(true)
	_, err := c.CallTool(context.Background(), "echo", map[string]any{"value": "x"})
	require.Error(t, err)
	require.ErrorIs(t, err, mcpdomain.ErrSessionMissing)
	require.False(t, c.IsHealthy(), "expired session must flip health for reconnect")
}

// TestInteropConcurrentCallToolAndDisconnect 验证并发 CallTool 与
// Disconnect 无 panic、无死锁,错误路径有界。
func TestInteropConcurrentCallToolAndDisconnect(t *testing.T) {
	s := newInteropServer(t)
	c := newInteropClient(t, s.ts.URL)
	require.NoError(t, c.Connect(context.Background()))

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = c.CallTool(ctx, "echo", map[string]any{"value": "race"})
		}()
	}
	time.Sleep(50 * time.Millisecond) // let callers enter
	require.NoError(t, c.Disconnect(context.Background()))
	wg.Wait()
	require.False(t, c.IsConnected())
}

// TestInteropStatelessServer 验证 stateless SDK server(不维护 session id)
// 兼容:连接成功、工具可调。
func TestInteropStatelessServer(t *testing.T) {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "stateless", Version: "1.0"}, nil)
	mcpSrv.AddTool(&mcp.Tool{
		Name:        "echo",
		Description: "echoes",
		InputSchema: map[string]any{"type": "object"},
	}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args map[string]any
		_ = json.Unmarshal(req.Params.Arguments, &args)
		v, _ := args["value"].(string)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "s:" + v}}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv },
		&mcp.StreamableHTTPOptions{Stateless: true})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	c := newInteropClient(t, ts.URL)
	require.NoError(t, c.Connect(context.Background()))
	t.Cleanup(func() { _ = c.Disconnect(context.Background()) })

	result, err := c.CallTool(context.Background(), "echo", map[string]any{"value": "stateless"})
	require.NoError(t, err)
	text, ok := result.(*mcp.CallToolResult).Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "s:stateless", text.Text)
}

// TestInteropHeadersAndAuthReachServer 验证 config.Headers 与
// AuthConfig(bearer)经 roundTripper 真实到达服务器。
func TestInteropHeadersAndAuthReachServer(t *testing.T) {
	s := newInteropServer(t)
	c := NewBaseClient(&MCPServerConfig{
		ID: "interop-server", Name: "interop", Version: "1.0",
		Transport: "streamable-http",
		URL:       s.ts.URL,
		Headers: map[string]string{
			"X-Tenant": "t-42",
		},
		Auth: &MCPAuthConfig{Type: mcpdomain.AuthTypeBearer, Token: "secret-bearer"},
	}, zap.NewNop())
	c.urlPolicy = URLPolicyAllowPrivate
	require.NoError(t, c.Connect(context.Background()))
	t.Cleanup(func() { _ = c.Disconnect(context.Background()) })

	_, err := c.ListTools(context.Background())
	require.NoError(t, err)

	h := s.lastHeader(t)
	require.Equal(t, "Bearer secret-bearer", h.Get("Authorization"))
	require.Equal(t, "t-42", h.Get("X-Tenant"))
}

// TestInteropOversizedLineFailsSafely 验证恶意超大单行 SSE 帧:lineLimited
// reader 报错 → 该次调用失败,绝不返回部分结果当成功、绝不 OOM。SDK
// 语义:POST 流错误只终结本次调用,连接本身保留——后续小结果调用必须
// 仍然成功(帧上限是防御护栏,不是连接级故障)。
func TestInteropOversizedLineFailsSafely(t *testing.T) {
	big := strings.Repeat("x", constants.MCPSSEFrameMaxBytes+1)
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "bigline", Version: "1.0"}, nil)
	mcpSrv.AddTool(&mcp.Tool{
		Name:        "big",
		Description: "returns an oversized single line",
		InputSchema: map[string]any{"type": "object"},
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: big}}}, nil
	})
	mcpSrv.AddTool(&mcp.Tool{
		Name:        "small",
		Description: "returns a tiny result",
		InputSchema: map[string]any{"type": "object"},
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil))
	t.Cleanup(ts.Close)

	c := newInteropClient(t, ts.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, c.Connect(ctx))
	t.Cleanup(func() { _ = c.Disconnect(context.Background()) })

	_, err := c.CallTool(ctx, "big", map[string]any{})
	require.Error(t, err, "oversized frame must surface as a failure, not a success")
	require.NotErrorIs(t, err, context.DeadlineExceeded, "failure must come from the line bound, not the caller timeout")

	// 流错误不杀连接:后续小结果调用必须成功,证明帧上限没有把连接打成
	// 不可用状态。
	result, err := c.CallTool(ctx, "small", map[string]any{})
	require.NoError(t, err)
	require.True(t, c.IsHealthy(), "a rejected frame must not poison the connection")
	_ = result
}
