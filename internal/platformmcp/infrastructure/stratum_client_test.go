package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestStratumClientRecordsTokenExchangeAndBackendOutcomes(t *testing.T) {
	metrics := &clientMetricsFake{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/internal/platform-mcp/token/exchange":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "delegation"})
		case "/internal/platform-assistant/diagnostics":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewObservedStratumClient(server.Client(), server.URL, metrics)
	if err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithInvocationToken(t.Context(), "invocation")

	_, err = client.Call(ctx, "stratum_diagnose_tenant", map[string]any{"areas": []any{"agents"}})

	if err == nil || metrics.exchangeOutcome != "success" || metrics.toolClass != "diagnostic" ||
		metrics.backendStatus != "5xx" {
		t.Fatalf("err=%v metrics=%+v", err, metrics)
	}
}

func TestStratumClientRecordsUnknownProposalOutcomeOnTransportFailure(t *testing.T) {
	metrics := &clientMetricsFake{}
	client, err := NewObservedStratumClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/internal/platform-mcp/token/exchange" {
			return jsonResponse(http.StatusOK, `{"access_token":"delegation"}`), nil
		}
		return nil, errors.New("connection reset")
	}), "https://stratum-internal:8443", metrics)
	if err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithInvocationToken(t.Context(), "invocation")

	_, err = client.Call(ctx, "stratum_propose_resource_change", map[string]any{
		"kind": "agent", "operation": "create", "payload": map[string]any{"name": "test"},
	})

	if err == nil || metrics.unknownOutcome != "proposal" {
		t.Fatalf("err=%v metrics=%+v", err, metrics)
	}
}

type clientMetricsFake struct {
	exchangeOutcome, toolClass, backendStatus, unknownOutcome string
}

func (f *clientMetricsFake) IncPlatformMCPTokenExchange(outcome string) {
	f.exchangeOutcome = outcome
}
func (f *clientMetricsFake) IncPlatformMCPBackendRequest(toolClass, statusClass string) {
	f.toolClass, f.backendStatus = toolClass, statusClass
}
func (f *clientMetricsFake) IncPlatformMCPUnknownOutcome(toolClass string) {
	f.unknownOutcome = toolClass
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}
