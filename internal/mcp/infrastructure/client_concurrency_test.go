package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// 串行化语义为什么不存在：旧实现用 reqMu 保证同一 client 同时只有一个
// in-flight 请求（自研 sendRequest 按顺序配对响应）。官方 SDK 的 session 是
// 并发安全的——每个请求分配独立 ID，响应按 ID 路由回各自调用方，因此无需
// 也不存在锁。以下测试验证的正是该并发安全契约。

// newSDKEchoServer builds an in-process official-SDK MCP server exposing one
// tool ("echo") that echoes the "text" argument back as a text result.
// handler overrides the default echo behavior (e.g. to block or to count
// arrivals). The SDK server handles the initialize handshake, the standalone
// SSE stream and concurrent POSTs natively, so no self-built JSON-RPC handler
// is needed (a raw echo handler cannot interoperate: its 400 on the
// standalone SSE GET breaks the SDK session, see connectStandaloneSSE).
func newSDKEchoServer(t *testing.T, handler mcp.ToolHandler) *httptest.Server {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "echo", Version: "1.0"}, nil)
	echo := func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if handler != nil {
			return handler(ctx, req)
		}
		var args struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(req.Params.Arguments, &args)
		if args.Text == "" {
			args.Text = "ok"
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: args.Text}}}, nil
	}
	srv.AddTool(&mcp.Tool{
		Name: "echo", Description: "echoes the text argument",
		InputSchema: map[string]any{"type": "object"},
	}, echo)
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{}))
	t.Cleanup(ts.Close)
	return ts
}

// newHTTPTestClient wires a BaseClient to an in-process SDK server and
// completes the initialize handshake. URLPolicyAllowPrivate is required:
// httptest loopback addresses are blocked under the production
// URLPolicyStrict (zero value), so tests must opt in.
func newHTTPTestClient(t *testing.T, handler mcp.ToolHandler) (*BaseClient, *httptest.Server) {
	t.Helper()
	srv := newSDKEchoServer(t, handler)
	client := NewBaseClient(&MCPServerConfig{
		ID: "srv", Name: "test", Transport: "streamable-http", URL: srv.URL,
	}, zap.NewNop())
	client.urlPolicy = URLPolicyAllowPrivate
	require.NoError(t, client.Connect(context.Background()))
	// Cleanup 逆序执行：必须先断开客户端再关 server，否则 SDK 的 standalone
	// SSE 长连接还挂在 httptest 上，ts.Close 会永久等待活跃请求。
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	return client, srv
}

// callToolText extracts the first text content of a CallTool result.
func callToolText(t *testing.T, result any) string {
	t.Helper()
	res, ok := result.(*mcp.CallToolResult)
	require.True(t, ok, "unexpected result type %T", result)
	if len(res.Content) == 0 {
		return ""
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok, "unexpected content type %T", res.Content[0])
	return tc.Text
}

// TestCallToolConcurrentCallsAllSucceed hammers one client with N=16
// concurrent CallTool calls. The SDK session is concurrency-safe: ids are
// allocated per request and responses are routed by id, so no request mutex
// exists (the old reqMu serialization is gone). Every call must succeed and
// each must receive its own echoed payload — a response misrouted to another
// caller would fail the payload check. -race guards the low-level
// request/response path.
func TestCallToolConcurrentCallsAllSucceed(t *testing.T) {
	client, _ := newHTTPTestClient(t, nil)

	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 1; i <= n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			result, err := client.CallTool(ctx, "echo", map[string]any{"text": fmt.Sprintf("payload-%d", i)})
			if err != nil {
				errs <- fmt.Errorf("call %d: %w", i, err)
				return
			}
			if text := callToolText(t, result); text != fmt.Sprintf("payload-%d", i) {
				errs <- fmt.Errorf("call %d: got payload %q", i, text)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestCallToolTimeoutThenFollowUpSucceeds verifies that a call abandoned by
// ctx deadline maps to ErrTransportTimeout and does not kill the SDK session:
// the follow-up call on the same client must still succeed. Under the old
// reqMu semantics this test asserted that a timeout releases the
// serialization lock; there is no lock anymore — the SDK session survives a
// cancelled POST, and the server-side handler is released explicitly.
func TestCallToolTimeoutThenFollowUpSucceeds(t *testing.T) {
	release := make(chan struct{})
	var first atomic.Bool
	client, _ := newHTTPTestClient(t, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !first.Swap(true) {
			// 模拟慢 server：第一个调用挂住直到被释放；兜底超时避免
			// Cleanup 阶段 httptest 等待活跃请求。ctx 兜底是防御性的：
			// 2025-06-18 协议下 server handler 的 ctx 不与请求取消联动。
			select {
			case <-release:
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
			}
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err := client.CallTool(ctx, "echo", map[string]any{})
	cancel()
	require.ErrorIs(t, err, ErrTransportTimeout)

	close(release)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	_, err = client.CallTool(ctx2, "echo", map[string]any{})
	require.NoError(t, err)
}

// TestDisconnectDuringInFlightCallToolRacesSafely races Disconnect against an
// in-flight CallTool. Session Close tears down the standalone SSE stream and
// pending request channels, so the in-flight call may succeed (response
// already delivered) or fail (connection torn down); it must always return
// boundedly, must not panic, and must not leak goroutines. CallTool holds
// only a snapshot of the session pointer, so Disconnect never blocks on it —
// the old reqMu deadlock/races around shared transport state no longer exist.
func TestDisconnectDuringInFlightCallToolRacesSafely(t *testing.T) {
	block := make(chan struct{})
	received := make(chan struct{}, 1)
	client, _ := newHTTPTestClient(t, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		select {
		case received <- struct{}{}:
		default:
		}
		select {
		case <-block:
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})

	baseline := runtime.NumGoroutine()

	result := make(chan error, 1)
	go func() {
		_, err := client.CallTool(context.Background(), "echo", map[string]any{})
		result <- err
	}()

	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("request never reached the server")
	}

	// 真正的竞态：server 在 100ms 后释放 in-flight 请求，同时立刻 Disconnect。
	// 两种结局都可能：请求先完成（结果送达）或连接先关闭（结果丢弃）。
	timer := time.AfterFunc(100*time.Millisecond, func() { close(block) })
	defer timer.Stop()

	if err := client.Disconnect(context.Background()); err != nil {
		t.Fatalf("disconnect failed: %v", err)
	}

	select {
	case err := <-result:
		if err != nil {
			t.Logf("in-flight CallTool after Disconnect: %v (allowed: session Close cannot be interrupted)", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight CallTool did not return after server release")
	}
	if client.IsConnected() {
		t.Fatal("client still connected after Disconnect")
	}

	// No goroutine leak: the client's SDK goroutines (SSE reader, response
	// dispatchers) exit asynchronously after Close, so poll briefly instead
	// of asserting an immediate count. A small tolerance absorbs server-side
	// long-lived goroutines.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline+3 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: %d goroutines, baseline %d", runtime.NumGoroutine(), baseline)
}

// TestCallToolUnreachableServerReturnsTranslatedError covers a client that
// was never connected: CallTool first attempts to connect
// (ensureConnected → doConnect), so the failure surfaces as the translated
// transport error rather than ErrClientClosed. The old sendHTTPRequest path
// returned ErrClientClosed for a closed transport; the new semantics have no
// pre-connected state a call could short-circuit on.
func TestCallToolUnreachableServerReturnsTranslatedError(t *testing.T) {
	// Port 1 refuses connections; the test URL policy lets the dial attempt
	// happen (production strict policy is bypassed by urlPolicy).
	client := NewBaseClient(&MCPServerConfig{
		ID: "srv", Name: "test", Transport: "streamable-http", URL: "http://localhost:1",
	}, zap.NewNop())
	client.urlPolicy = URLPolicyAllowPrivate

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := client.CallTool(ctx, "echo", map[string]any{})
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrClientClosed),
		"CallTool on a fresh client must attempt to connect, not report closed: %v", err)
	require.ErrorIs(t, err, mcpdomain.ErrTransportFailed)
}
