package infrastructure

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mcpEchoHandler is a minimal streamable-http MCP server for concurrency
// tests: initialize handshake + per-method JSON-RPC echo. notify (optional)
// receives every decoded request method; when block is non-nil the handler
// waits on it before replying to tools/call.
func mcpEchoHandler(notify func(method string, id any), block <-chan struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if notify != nil {
			notify(req.Method, req.ID)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			writeEchoResult(w, req.ID, map[string]any{
				"protocolVersion": constants.MCPProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
				"serverInfo":      map[string]any{"name": "echo", "version": "1.0"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			if block != nil {
				// 随客户端断开退出：httptest Close 等待活跃连接，永久阻塞会
				// 在 Cleanup 阶段拖垮测试进程（断言失败也先释放 server）。
				select {
				case <-block:
				case <-r.Context().Done():
					return
				}
			}
			writeEchoResult(w, req.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}})
		default:
			http.Error(w, "method not found", http.StatusNotFound)
		}
	})
}

func writeEchoResult(w http.ResponseWriter, id any, result any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

// newHTTPTestClient wires a BaseClient to an in-process echo server and
// completes the initialize handshake.
func newHTTPTestClient(t *testing.T, handler http.Handler) (*BaseClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	client := NewBaseClient(&MCPServerConfig{
		ID: "srv", Name: "test", Transport: "streamable-http", URL: srv.URL,
	}, zap.NewNop())
	require.NoError(t, client.Connect(context.Background()))
	return client, srv
}

// TestSendRequestConcurrentRequestsAreSerialized hammers one client with
// concurrent requests. Per-client serialisation (reqMu) guarantees at most
// one in-flight request, so every response is consumed by its own request:
// all requests must succeed and the multiset of response IDs must equal the
// multiset of request IDs (no response lost, stolen, or duplicated).
// -race guards the lower-level request/response path.
func TestSendRequestConcurrentRequestsAreSerialized(t *testing.T) {
	client, _ := newHTTPTestClient(t, mcpEchoHandler(nil, nil))

	const n = 16
	gotIDs := make(chan int, n)
	var wg sync.WaitGroup
	for i := 1; i <= n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			resp, err := client.sendRequest(ctx, &MCPRequest{JSONRPC: "2.0", ID: i, Method: "tools/call", Params: map[string]any{"name": "echo"}})
			if err != nil {
				t.Errorf("request %d failed: %v", i, err)
				return
			}
			gotIDs <- resp.ID
		}()
	}
	wg.Wait()
	close(gotIDs)
	got := make([]int, 0, n)
	for id := range gotIDs {
		got = append(got, id)
	}
	sort.Ints(got)
	for i := 1; i <= n; i++ {
		if got[i-1] != i {
			t.Fatalf("response IDs not preserved under concurrency: got %v", got)
		}
	}
}

// TestCallToolSerializesSameClient verifies that two concurrent CallTool calls
// on the same client are serialised: the second request must not reach the
// server until the first has completed.
func TestCallToolSerializesSameClient(t *testing.T) {
	received := make(chan int, 4)
	release := make(chan struct{})
	client, _ := newHTTPTestClient(t, mcpEchoHandler(func(method string, id any) {
		if method == "tools/call" {
			if f, ok := id.(float64); ok {
				received <- int(f)
			}
		}
	}, release))

	errCh := make(chan error, 2)
	go func() { _, err := client.CallTool(context.Background(), "echo", map[string]any{}); errCh <- err }()
	go func() { _, err := client.CallTool(context.Background(), "echo", map[string]any{}); errCh <- err }()

	first := <-received
	// The first request is still blocked at the server, so its serialisation
	// lock is held: the second request must not have reached the server yet.
	select {
	case r := <-received:
		t.Fatalf("second request %d reached the server while the first was in flight", r)
	case <-time.After(100 * time.Millisecond):
	}
	_ = first
	release <- struct{}{} // complete the first request
	second := <-received
	release <- struct{}{} // complete the second request
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("CallTool failed: %v", err)
		}
	}
	if first == second {
		t.Fatalf("expected two distinct requests, got %d twice", first)
	}
}

// TestCallToolParallelAcrossClients verifies that the serialisation lock is
// per-client, not global: requests on different clients reach their servers
// concurrently.
func TestCallToolParallelAcrossClients(t *testing.T) {
	arriveA := make(chan struct{}, 1)
	arriveB := make(chan struct{}, 1)
	release := make(chan struct{})
	notifyArrival := func(ch chan struct{}) func(string, any) {
		return func(method string, _ any) {
			if method == "tools/call" {
				ch <- struct{}{}
			}
		}
	}
	clientA, _ := newHTTPTestClient(t, mcpEchoHandler(notifyArrival(arriveA), release))
	clientB, _ := newHTTPTestClient(t, mcpEchoHandler(notifyArrival(arriveB), release))

	errCh := make(chan error, 2)
	go func() { _, err := clientA.CallTool(context.Background(), "echo", map[string]any{}); errCh <- err }()
	go func() { _, err := clientB.CallTool(context.Background(), "echo", map[string]any{}); errCh <- err }()

	select {
	case <-arriveA:
	case <-time.After(time.Second):
		t.Fatal("client A request never reached its server")
	}
	select {
	case <-arriveB:
	case <-time.After(time.Second):
		t.Fatal("client B request never reached its server; per-client lock leaked across clients")
	}
	release <- struct{}{}
	release <- struct{}{}
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("CallTool failed: %v", err)
		}
	}
}

// TestSendRequestTimeoutReleasesSerializationLock verifies that a timed-out
// request releases the per-client lock, so the next request on the same
// client can execute.
func TestSendRequestTimeoutReleasesSerializationLock(t *testing.T) {
	serverReady := make(chan struct{})
	respond := make(chan struct{})
	var first atomic.Bool
	client, _ := newHTTPTestClient(t, mcpEchoHandler(func(method string, _ any) {
		// 第一个 tools/call 挂住（模拟慢 server）；其余立即响应。
		// 阻塞须随客户端断开退出（r.Context().Done()），否则 srv.Close 在
		// Cleanup 阶段永久等待活跃连接，失败断言也会拖垮整个测试进程。
		if method == "tools/call" && !first.Swap(true) {
			close(serverReady)
			// 兜底超时：即使断言提前失败，server 侧也能退出，
			// httptest Close 不会永久等待活跃连接。
			select {
			case <-respond:
			case <-time.After(5 * time.Second):
			}
		}
	}, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err := client.CallTool(ctx, "echo", map[string]any{})
	cancel()
	// url.Error 原文含完整 URL（凭据防泄漏），超时经 ErrTransportTimeout
	// sentinel 保留语义，不落原始错误文本。
	require.ErrorIs(t, err, ErrTransportTimeout)

	<-serverReady
	close(respond)

	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	_, err = client.CallTool(ctx2, "echo", map[string]any{})
	require.NoError(t, err)
}

// TestDisconnectDuringInFlightCallToolRacesSafely races Disconnect against
// an in-flight CallTool. HTTP requests hold a client snapshot taken under
// the lock, so Disconnect cannot interrupt an in-flight request; it must
// instead not panic on shared transport state, and the in-flight request
// must exit boundedly once the server releases it. The client ends
// disconnected; a later CallTool transparently reconnects (stdio's
// closed-pipe semantics no longer exist).
func TestDisconnectDuringInFlightCallToolRacesSafely(t *testing.T) {
	block := make(chan struct{})
	received := make(chan struct{}, 1)
	client, _ := newHTTPTestClient(t, mcpEchoHandler(func(method string, _ any) {
		if method == "tools/call" {
			select {
			case received <- struct{}{}:
			default:
			}
			// 兜底超时：断言失败提前退出时 server 也能释放连接。
			select {
			case <-block:
			case <-time.After(5 * time.Second):
			}
		}
	}, nil))

	result := make(chan error, 1)
	go func() {
		_, err := client.CallTool(context.Background(), "slow_tool", map[string]any{})
		result <- err
	}()

	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("request never reached the server")
	}

	if err := client.Disconnect(context.Background()); err != nil {
		t.Fatalf("disconnect failed: %v", err)
	}

	// Release the server so the in-flight request exits boundedly; it may
	// complete or fail, but must not panic or leak the goroutine.
	close(block)
	select {
	case err := <-result:
		if err != nil {
			t.Logf("in-flight CallTool after Disconnect: %v (allowed: HTTP cannot interrupt in-flight requests)", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight CallTool did not return after server release")
	}
	if client.IsConnected() {
		t.Fatal("client still connected after Disconnect")
	}
}

// TestSendHTTPRequestClosedTransportReturnsErrClientClosed is the HTTP
// counterpart: a call racing Disconnect must not dereference a nil client.
func TestSendHTTPRequestClosedTransportReturnsErrClientClosed(t *testing.T) {
	client := NewBaseClient(&MCPServerConfig{ID: "srv", Transport: "http", URL: "http://localhost:1"}, zap.NewNop())
	_, err := client.sendHTTPRequest(context.Background(), &MCPRequest{JSONRPC: "2.0", ID: 1, Method: "tools/list"})
	require.ErrorIs(t, err, ErrClientClosed)
}
