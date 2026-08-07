package infrastructure

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newStdioTestClient wires the client to pipe pairs (the same transport shape
// exec.StdoutPipe produces: pollable *os.File) without spawning a child.
func newStdioTestClient(t *testing.T) (*BaseClient, ioReadCloserPair) {
	t.Helper()
	serverRead, clientWrite, err := os.Pipe()
	require.NoError(t, err)
	clientRead, serverWrite, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = serverRead.Close()
		_ = clientWrite.Close()
		_ = clientRead.Close()
		_ = serverWrite.Close()
	})
	client := NewBaseClient(&MCPServerConfig{ID: "srv", Name: "test", Transport: "stdio"}, zap.NewNop())
	client.stdin = clientWrite
	client.stdout = clientRead
	client.connected = true
	return client, ioReadCloserPair{serverRead: serverRead, serverWrite: serverWrite}
}

type ioReadCloserPair struct {
	serverRead  *os.File
	serverWrite *os.File
}

// TestSendStdioRequestTimeoutWaitsForReaderGoroutine verifies that a ctx
// timeout returns ctx.Err while the blocked reader goroutine exits boundedly
// (woken via a read deadline), so the single-reader lock is released and the
// connection stays usable.
func TestSendStdioRequestTimeoutWaitsForReaderGoroutine(t *testing.T) {
	client, pair := newStdioTestClient(t)

	// Server never responds: the request must time out.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err := client.sendStdioRequest(ctx, &MCPRequest{JSONRPC: "2.0", ID: 1, Method: "tools/list"})
	cancel()
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// A leaked reader goroutine or a stuck single-reader lock would swallow
	// the response below or block this request until its own timeout; success
	// within a fresh bound proves the previous reader exited and the deadline
	// was cleared for the next read.
	_ = json.NewEncoder(pair.serverWrite).Encode(MCPResponse{JSONRPC: "2.0", ID: 2, Result: map[string]any{"ok": true}})
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	resp, err := client.sendStdioRequest(ctx2, &MCPRequest{JSONRPC: "2.0", ID: 2, Method: "tools/list"})
	require.NoError(t, err)
	require.Equal(t, 2, resp.ID)
}

// TestSendStdioRequestConcurrentReadsAreSerialized hammers one client with
// concurrent requests over shared pipes. Per-client serialisation (reqMu)
// guarantees at most one in-flight request, so every response line is
// consumed by its own request: all requests must succeed and the multiset of
// response IDs must equal the multiset of request IDs (no response lost,
// stolen, or duplicated). -race guards the lower-level read/write locks.
func TestSendStdioRequestConcurrentReadsAreSerialized(t *testing.T) {
	client, pair := newStdioTestClient(t)

	// Fake MCP server: echo one response per request line.
	go func() {
		scanner := bufio.NewReader(pair.serverRead)
		for {
			line, err := scanner.ReadBytes('\n')
			if err != nil {
				return
			}
			var req MCPRequest
			if err := json.Unmarshal(bytes.TrimSpace(line), &req); err != nil {
				return
			}
			if err := json.NewEncoder(pair.serverWrite).Encode(MCPResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"ok": true}}); err != nil {
				return
			}
		}
	}()

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
			resp, err := client.sendRequest(ctx, &MCPRequest{JSONRPC: "2.0", ID: i, Method: "tools/call"})
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
	client, pair := newStdioTestClient(t)

	type recv struct{ id int }
	received := make(chan recv, 4)
	release := make(chan struct{})

	// Server: report each request, then block until released before replying.
	go func() {
		scanner := bufio.NewReader(pair.serverRead)
		for {
			line, err := scanner.ReadBytes('\n')
			if err != nil {
				return
			}
			var req MCPRequest
			if err := json.Unmarshal(bytes.TrimSpace(line), &req); err != nil {
				return
			}
			received <- recv{id: req.ID}
			<-release
			_ = json.NewEncoder(pair.serverWrite).Encode(MCPResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"ok": true}})
		}
	}()

	errCh := make(chan error, 2)
	go func() { _, err := client.CallTool(context.Background(), "tool", map[string]any{}); errCh <- err }()
	go func() { _, err := client.CallTool(context.Background(), "tool", map[string]any{}); errCh <- err }()

	first := <-received
	// The first request is still blocked at the server, so its serialisation
	// lock is held: the second request must not have reached the server yet.
	select {
	case r := <-received:
		t.Fatalf("second request %d reached the server while the first was in flight", r.id)
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
	if first.id == second.id {
		t.Fatalf("expected two distinct requests, got %d twice", first.id)
	}
}

// TestCallToolParallelAcrossClients verifies that the serialisation lock is
// per-client, not global: requests on different clients reach their servers
// concurrently.
func TestCallToolParallelAcrossClients(t *testing.T) {
	clientA, pairA := newStdioTestClient(t)
	clientB, pairB := newStdioTestClient(t)

	receivedA := make(chan struct{})
	receivedB := make(chan struct{})
	release := make(chan struct{})
	server := func(pair ioReadCloserPair, received chan struct{}) {
		scanner := bufio.NewReader(pair.serverRead)
		for {
			line, err := scanner.ReadBytes('\n')
			if err != nil {
				return
			}
			var req MCPRequest
			_ = json.Unmarshal(bytes.TrimSpace(line), &req)
			received <- struct{}{}
			<-release
			_ = json.NewEncoder(pair.serverWrite).Encode(MCPResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
		}
	}
	go server(pairA, receivedA)
	go server(pairB, receivedB)

	errCh := make(chan error, 2)
	go func() { _, err := clientA.CallTool(context.Background(), "tool", map[string]any{}); errCh <- err }()
	go func() { _, err := clientB.CallTool(context.Background(), "tool", map[string]any{}); errCh <- err }()

	select {
	case <-receivedA:
	case <-time.After(time.Second):
		t.Fatal("client A request never reached its server")
	}
	select {
	case <-receivedB:
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
	client, pair := newStdioTestClient(t)

	serverReady := make(chan struct{})
	respond := make(chan struct{})
	go func() {
		scanner := bufio.NewReader(pair.serverRead)
		// First request: consume it but never respond (forces the timeout).
		if _, err := scanner.ReadBytes('\n'); err != nil {
			return
		}
		close(serverReady)
		<-respond
		// Subsequent requests get an immediate response.
		for {
			line, err := scanner.ReadBytes('\n')
			if err != nil {
				return
			}
			var req MCPRequest
			_ = json.Unmarshal(bytes.TrimSpace(line), &req)
			_ = json.NewEncoder(pair.serverWrite).Encode(MCPResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"ok": true}})
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err := client.CallTool(ctx, "tool", map[string]any{})
	cancel()
	require.ErrorIs(t, err, context.DeadlineExceeded)

	<-serverReady
	close(respond)

	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	_, err = client.CallTool(ctx2, "tool", map[string]any{})
	require.NoError(t, err)
}

// TestDisconnectDuringInFlightCallToolReturnsErrClientClosed races Disconnect
// against an in-flight CallTool. The call must return ErrClientClosed (never
// panic on a nil transport) and Disconnect must leave the client disconnected.
func TestDisconnectDuringInFlightCallToolReturnsErrClientClosed(t *testing.T) {
	for iter := 0; iter < 25; iter++ {
		client, pair := newStdioTestClient(t)

		// Fake server: signal once the request line is readable, then never reply.
		received := make(chan struct{})
		go func() {
			buf := make([]byte, 4096)
			_, _ = pair.serverRead.Read(buf)
			close(received)
		}()

		result := make(chan error, 1)
		go func() {
			_, err := client.CallTool(context.Background(), "slow_tool", map[string]any{})
			result <- err
		}()

		select {
		case <-received:
		case <-time.After(2 * time.Second):
			t.Fatalf("iter %d: request never reached the server", iter)
		}

		if err := client.Disconnect(context.Background()); err != nil {
			t.Fatalf("iter %d: disconnect failed: %v", iter, err)
		}

		select {
		case err := <-result:
			if !errors.Is(err, ErrClientClosed) {
				t.Fatalf("iter %d: in-flight CallTool error = %v, want ErrClientClosed", iter, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("iter %d: in-flight CallTool did not return after Disconnect", iter)
		}
		if client.IsConnected() {
			t.Fatalf("iter %d: client still connected after Disconnect", iter)
		}
	}
}

// TestSendStdioRequestClosedTransportReturnsErrClientClosed verifies that a
// request issued after the transport was released fails closed with
// ErrClientClosed instead of a nil-interface panic.
func TestSendStdioRequestClosedTransportReturnsErrClientClosed(t *testing.T) {
	client := NewBaseClient(&MCPServerConfig{ID: "srv", Transport: "stdio"}, zap.NewNop())
	_, err := client.sendStdioRequest(context.Background(), &MCPRequest{JSONRPC: "2.0", ID: 1, Method: "tools/list"})
	require.ErrorIs(t, err, ErrClientClosed)
}

// TestSendHTTPRequestClosedTransportReturnsErrClientClosed is the HTTP
// counterpart: a call racing Disconnect must not dereference a nil client.
func TestSendHTTPRequestClosedTransportReturnsErrClientClosed(t *testing.T) {
	client := NewBaseClient(&MCPServerConfig{ID: "srv", Transport: "http", URL: "http://localhost:1"}, zap.NewNop())
	_, err := client.sendHTTPRequest(context.Background(), &MCPRequest{JSONRPC: "2.0", ID: 1, Method: "tools/list"})
	require.ErrorIs(t, err, ErrClientClosed)
}
