package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/internal/platformmcp/requestctx"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestServerImplementsPhase1MCPProtocol(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantCode   int
		wantResult string
		wantError  float64
		wantCalls  int
	}{
		{
			name:     "initialize",
			body:     `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
			wantCode: http.StatusOK, wantResult: "protocolVersion",
		},
		{
			name: "list tools", body: `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
			wantCode: http.StatusOK, wantResult: "tools",
		},
		{
			name: "call tool", body: `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tool-1","arguments":{"query":"ok"}}}`,
			wantCode: http.StatusOK, wantResult: "content", wantCalls: 1,
		},
		{
			name: "list resources", body: `{"jsonrpc":"2.0","id":4,"method":"resources/list","params":{}}`,
			wantCode: http.StatusOK, wantResult: "resources",
		},
		{
			name: "unknown envelope field", body: `{"jsonrpc":"2.0","id":5,"method":"tools/list","params":{},"extra":true}`,
			wantCode: http.StatusOK, wantError: -32600,
		},
		{
			name: "unknown call field", body: `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"tool-1","arguments":{},"url":"https://example.test"}}`,
			wantCode: http.StatusOK, wantError: -32602,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dispatcher := &dispatcherFake{}
			handler := newTestServer(t, dispatcher, readinessFake{})
			res := performRPCRequest(t, handler, tc.body)
			if res.Code != tc.wantCode || dispatcher.calls != tc.wantCalls {
				t.Fatalf("status=%d calls=%d, want status=%d calls=%d", res.Code, dispatcher.calls, tc.wantCode, tc.wantCalls)
			}
			payload := decodeResponse(t, res)
			if tc.wantResult != "" {
				result, ok := payload["result"].(map[string]any)
				if !ok || result[tc.wantResult] == nil {
					t.Fatalf("response=%s", res.Body.String())
				}
			}
			if tc.wantError != 0 {
				rpcErr, ok := payload["error"].(map[string]any)
				if !ok || rpcErr["code"] != tc.wantError {
					t.Fatalf("response=%s", res.Body.String())
				}
			}
		})
	}
}

func TestServerReturnsSafeToolErrorWithoutLoggingArguments(t *testing.T) {
	const secret = "raw-argument-secret"
	core, logs := observer.New(zap.InfoLevel)
	dispatcher := &dispatcherFake{err: errors.New("upstream body contains " + secret)}
	handler, err := New(Config{
		Dispatcher: dispatcher, Readiness: readinessFake{}, Logger: zap.New(core),
	})
	if err != nil {
		t.Fatal(err)
	}

	res := performRPCRequest(t, handler.Handler(),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tool-1","arguments":{"token":"`+secret+`"}}}`)

	if strings.Contains(res.Body.String(), secret) {
		t.Fatalf("response exposed raw argument or upstream error: %s", res.Body.String())
	}
	for _, entry := range logs.All() {
		if strings.Contains(entry.Message, secret) {
			t.Fatalf("logs exposed raw arguments: %+v", entry)
		}
		for _, value := range entry.ContextMap() {
			if strings.Contains(fmt.Sprint(value), secret) {
				t.Fatalf("logs exposed raw arguments: %+v", entry)
			}
		}
	}
}

func TestServerBindsInvocationBearerOnlyToToolCallContext(t *testing.T) {
	dispatcher := &dispatcherFake{}
	handler := newTestServer(t, dispatcher, readinessFake{})
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tool-1","arguments":{}}}`,
	))
	req.Header.Set("Authorization", "Bearer invocation")
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK || dispatcher.invocation != "invocation" {
		t.Fatalf("status=%d invocation=%q", res.Code, dispatcher.invocation)
	}
}

func TestServerHealthAndReadiness(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		readyErr   error
		wantStatus int
	}{
		{name: "live", path: "/healthz", wantStatus: http.StatusOK},
		{name: "ready", path: "/readyz", wantStatus: http.StatusOK},
		{name: "backend unavailable", path: "/readyz", readyErr: errors.New("backend unavailable"), wantStatus: http.StatusServiceUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := newTestServer(t, &dispatcherFake{}, readinessFake{err: tc.readyErr})
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			res := httptest.NewRecorder()

			handler.ServeHTTP(res, req)

			if res.Code != tc.wantStatus {
				t.Fatalf("status=%d, want %d", res.Code, tc.wantStatus)
			}
		})
	}
}

func TestServerRecordsBoundedToolMetrics(t *testing.T) {
	metrics := &metricsFake{}
	server, err := New(Config{
		Dispatcher: &dispatcherFake{}, Readiness: readinessFake{}, Logger: zap.NewNop(), Metrics: metrics,
	})
	if err != nil {
		t.Fatal(err)
	}

	res := performRPCRequest(t, server.Handler(),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"stratum_diagnose_tenant","arguments":{}}}`)

	if res.Code != http.StatusOK || metrics.requests != 1 || metrics.toolClass != "diagnostic" ||
		metrics.riskLevel != "read" || metrics.outcome != "success" || metrics.inFlight != 0 {
		t.Fatalf("status=%d metrics=%+v", res.Code, metrics)
	}
}

func TestServerRecordsMissingInvocationCredential(t *testing.T) {
	metrics := &metricsFake{}
	server, err := New(Config{
		Dispatcher: &dispatcherFake{}, Readiness: readinessFake{}, Logger: zap.NewNop(), Metrics: metrics,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"stratum_diagnose_tenant","arguments":{}}}`,
	))
	res := httptest.NewRecorder()

	server.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized || metrics.authDenials != 1 {
		t.Fatalf("status=%d auth_denials=%d", res.Code, metrics.authDenials)
	}
}

func TestServerExposesConfiguredMetricsHandler(t *testing.T) {
	server, err := New(Config{
		Dispatcher: &dispatcherFake{}, Readiness: readinessFake{}, Logger: zap.NewNop(),
		MetricsHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()

	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if res.Code != http.StatusNoContent {
		t.Fatalf("status=%d, want %d", res.Code, http.StatusNoContent)
	}
}

func newTestServer(t *testing.T, dispatcher Dispatcher, readiness ReadinessChecker) http.Handler {
	t.Helper()
	server, err := New(Config{Dispatcher: dispatcher, Readiness: readiness, Logger: zap.NewNop()})
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler()
}

func performRPCRequest(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer invocation")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func decodeResponse(t *testing.T, res *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

type dispatcherFake struct {
	calls      int
	err        error
	invocation string
}

func (f *dispatcherFake) ListTools(context.Context) []mcpdomain.Tool {
	return []mcpdomain.Tool{{Name: "tool-1", InputSchema: map[string]any{"type": "object"}}}
}

func (f *dispatcherFake) CallTool(
	ctx context.Context,
	_ string,
	_ map[string]any,
) (agentport.MCPToolResult, error) {
	f.calls++
	f.invocation, _ = requestctx.InvocationToken(ctx)
	return agentport.MCPToolResult{
		Content: []agentport.MCPContent{{Type: "text", Text: "ok"}},
	}, f.err
}

type readinessFake struct {
	err error
}

type metricsFake struct {
	requests, inFlight, authDenials int
	toolClass, riskLevel, outcome   string
}

func (f *metricsFake) IncPlatformMCPRequest(toolClass, riskLevel, outcome string) {
	f.requests++
	f.toolClass, f.riskLevel, f.outcome = toolClass, riskLevel, outcome
}
func (f *metricsFake) RecordPlatformMCPRequestDuration(_, _ string, _ float64) {}
func (f *metricsFake) IncPlatformMCPRequestsInFlight()                         { f.inFlight++ }
func (f *metricsFake) DecPlatformMCPRequestsInFlight()                         { f.inFlight-- }
func (f *metricsFake) IncPlatformMCPAuthDenial(_ string)                       { f.authDenials++ }

func (f readinessFake) Check(context.Context) error {
	return f.err
}
