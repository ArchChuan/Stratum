package infrastructure

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/pkg/platformmcp"
	"go.uber.org/zap"
)

type invocationCredentialFake struct {
	calls int
}

type managedTransportFake struct {
	transport http.RoundTripper
}

func (f managedTransportFake) Transport() (http.RoundTripper, error) {
	return f.transport, nil
}

func (f *invocationCredentialFake) Authorization(context.Context, string, string) (string, error) {
	f.calls++
	return "Bearer invocation-secret", nil
}

func TestBaseClientUsesInvocationCredentialOnlyForManagedPlatformIdentity(t *testing.T) {
	tests := []struct {
		name       string
		systemKey  string
		management string
		wantCalls  int
		wantAuth   string
	}{
		{name: "managed platform", systemKey: platformmcp.SystemServerKey,
			management: platformmcp.ManagementPlatform, wantCalls: 1, wantAuth: "Bearer invocation-secret"},
		{name: "copied URL without identity", management: platformmcp.ManagementTenant, wantAuth: "Bearer static"},
		{name: "forged key without management", systemKey: platformmcp.SystemServerKey,
			management: platformmcp.ManagementTenant, wantAuth: "Bearer static"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var authorization string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				authorization = req.Header.Get("Authorization")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0", "id": 1, "result": map[string]any{"content": []any{}},
				})
			}))
			t.Cleanup(server.Close)
			provider := &invocationCredentialFake{}
			client := NewBaseClient(&MCPServerConfig{
				ID: "stratum-platform-mcp", Transport: "http", URL: server.URL,
				Timeout: time.Second, Headers: map[string]string{"Authorization": "Bearer static"},
				SystemKey: tc.systemKey, ManagementMode: tc.management,
			}, zap.NewNop())
			client.connected = true
			client.httpClient = server.Client()
			client.negotiatedVersion = mcpProtocolVersion
			client.SetInvocationCredentialProvider(provider)

			_, err := client.CallTool(t.Context(), "stratum_diagnose_tenant", map[string]any{"areas": []string{"agent"}})
			if err != nil {
				t.Fatal(err)
			}
			if provider.calls != tc.wantCalls || authorization != tc.wantAuth {
				t.Fatalf("provider calls=%d authorization=%q", provider.calls, authorization)
			}
		})
	}
}

func TestBaseClientConnectsManagedPlatformTransportWithoutLockingItself(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var request MCPRequest
		if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if request.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeTestInitializeResult(w, request.ID)
	}))
	t.Cleanup(server.Close)
	client := NewBaseClient(&MCPServerConfig{
		ID: "stratum-platform-mcp", Transport: "streamable-http", URL: server.URL,
		Timeout: time.Second, SystemKey: platformmcp.SystemServerKey, ManagementMode: platformmcp.ManagementPlatform,
	}, zap.NewNop())
	client.SetManagedHTTPTransportProvider(managedTransportFake{transport: server.Client().Transport})

	done := make(chan error, 1)
	go func() { done <- client.Connect(t.Context()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("managed Platform MCP connection deadlocked")
	}
}
