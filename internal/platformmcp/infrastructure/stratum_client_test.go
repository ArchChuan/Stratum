package infrastructure

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/byteBuilderX/stratum/internal/platformmcp/requestctx"
)

func TestStratumClientExchangesInvocationBeforeDelegatedRequest(t *testing.T) {
	const invocation = "invocation-secret"
	var mu sync.Mutex
	var exchangeCalls, businessCalls int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch req.URL.Path {
		case "/internal/platform-mcp/token/exchange":
			exchangeCalls++
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if payload["invocation_token"] != invocation {
				t.Errorf("exchange invocation token was not propagated")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "delegation"})
		case "/internal/platform-assistant/docs/search":
			businessCalls++
			if got := req.Header.Get("Authorization"); got != "Bearer delegation" {
				t.Errorf("business authorization=%q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"citations": []any{}})
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewStratumClient(server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithInvocationToken(t.Context(), invocation)

	result, err := client.Call(ctx, "stratum_search_official_docs", map[string]any{"query": "Agent"})

	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 || exchangeCalls != 1 || businessCalls != 1 {
		t.Fatalf("result=%+v exchangeCalls=%d businessCalls=%d", result, exchangeCalls, businessCalls)
	}
}

func TestStratumClientRejectsMissingInvocationAndUnknownTool(t *testing.T) {
	client, err := NewStratumClient(http.DefaultClient, "https://stratum-internal:8443")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		ctx  context.Context
		tool string
	}{
		{name: "missing invocation", ctx: t.Context(), tool: "stratum_diagnose_tenant"},
		{name: "unknown tool", ctx: requestctx.WithInvocationToken(t.Context(), "invocation"), tool: "tenant_tool"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.Call(tc.ctx, tc.tool, map[string]any{})
			if err == nil {
				t.Fatal("expected call to fail")
			}
		})
	}
}
