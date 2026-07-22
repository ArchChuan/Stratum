package testserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFakeServerSupportsDynamicCatalogAndDeterministicCalls(t *testing.T) {
	server := New(t)
	server.SetTools([]Tool{{
		Name: "get_order", Description: "read", InputSchema: map[string]any{"type": "object"},
		Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true},
	}})
	server.SetBehavior("get_order", Behavior{
		Result: map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}},
	})

	listed := rpcCall(t, server.URL(), "tools/list", map[string]any{})
	require.Contains(t, string(listed), "destructiveHint")
	rpcCall(t, server.URL(), "tools/call", map[string]any{
		"name": "get_order", "arguments": map[string]any{"id": "order-1", "include": true},
	})

	require.Equal(t, 1, server.Attempts("get_order"))
	firstDigest := server.LastArgumentsDigest("get_order")
	require.NotEmpty(t, firstDigest)

	server.Reset()
	server.SetTools([]Tool{{Name: "list_orders", InputSchema: map[string]any{"type": "object"}}})
	listed = rpcCall(t, server.URL(), "tools/list", map[string]any{})
	require.NotContains(t, string(listed), "get_order")
	require.Contains(t, string(listed), "list_orders")
	require.Zero(t, server.Attempts("get_order"))
}

func TestFakeServerSupportsFailureAndResultScenarios(t *testing.T) {
	server := New(t)
	server.SetTools([]Tool{{Name: "scenario", InputSchema: map[string]any{"type": "object"}}})

	tests := []struct {
		name     string
		behavior Behavior
		contains string
	}{
		{name: "protocol error", behavior: Behavior{ProtocolError: true}, contains: `"error"`},
		{name: "is error", behavior: Behavior{Result: map[string]any{"isError": true}}, contains: `"isError":true`},
		{name: "schema mismatch", behavior: Behavior{Result: map[string]any{"structuredContent": map[string]any{"count": "bad"}}}, contains: `"count":"bad"`},
		{name: "sensitive", behavior: Behavior{Result: map[string]any{"structuredContent": map[string]any{"api_key": "sentinel"}}}, contains: "sentinel"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server.SetBehavior("scenario", tt.behavior)
			body := rpcCall(t, server.URL(), "tools/call", map[string]any{"name": "scenario", "arguments": map[string]any{}})
			require.Contains(t, string(body), tt.contains)
		})
	}
}

func TestFakeServerDelayHonorsCancellation(t *testing.T) {
	server := New(t)
	server.SetTools([]Tool{{Name: "slow", InputSchema: map[string]any{"type": "object"}}})
	server.SetBehavior("slow", Behavior{Delay: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL(), bytes.NewReader([]byte(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"slow","arguments":{}}}`,
	)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	response, err := http.DefaultClient.Do(req)
	if response != nil {
		response.Body.Close()
	}

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, 1, server.Attempts("slow"))
}

func rpcCall(t *testing.T, url, method string, params map[string]any) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	require.NoError(t, err)
	response, err := http.Post(url, "application/json", bytes.NewReader(payload))
	require.NoError(t, err)
	defer response.Body.Close()
	var body bytes.Buffer
	_, err = body.ReadFrom(response.Body)
	require.NoError(t, err)
	return body.Bytes()
}
