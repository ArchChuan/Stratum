package infrastructure

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestBaseClientSendsConfiguredStaticAuthorizationHeader(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		authorization = req.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1, "result": map[string]any{"content": []any{}},
		})
	}))
	t.Cleanup(server.Close)
	client := NewBaseClient(&MCPServerConfig{
		ID: "orders", Transport: "http", URL: server.URL,
		Timeout: time.Second, Headers: map[string]string{"Authorization": "Bearer static"},
	}, zap.NewNop())
	client.connected = true
	client.httpClient = server.Client()
	client.negotiatedVersion = mcpProtocolVersion

	_, err := client.CallTool(t.Context(), "get_order", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer static" {
		t.Fatalf("authorization=%q", authorization)
	}
}
