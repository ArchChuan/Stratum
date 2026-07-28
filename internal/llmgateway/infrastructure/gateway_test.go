package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// testAESKey is derived from a fixed test PEM so encrypted values are deterministic.
var testAESKey = pkgcrypto.DeriveAESKey("test-pem")

// encryptTestKey encrypts a plaintext API key with testAESKey. Panics on error
// (test-only helper).
func encryptTestKey(plain string) string {
	enc, err := pkgcrypto.Encrypt(testAESKey, plain)
	if err != nil {
		panic(fmt.Sprintf("encrypt test key: %v", err))
	}
	return enc
}

// testRegistry builds a ModelRegistry that returns encrypted provider keys for
// every tenant. It is used by tests that need a functional Gateway.
func testRegistry(t *testing.T, provider, apiKey string) *ModelRegistry {
	t.Helper()
	_ = t
	readSettings := func(_ context.Context, tenantID string) ([]byte, error) {
		if tenantID == "missing" {
			return nil, errors.New("no such tenant")
		}
		return json.Marshal(map[string]any{
			"llm_api_keys": map[string]string{provider: apiKey},
		})
	}
	return NewModelRegistry(readSettings, testAESKey, zap.NewNop())
}

// tenantCtx returns a context with the given tenant ID injected via reqctx.
func tenantCtx(ctx context.Context, tenantID string) context.Context {
	return reqctx.WithTenantID(ctx, tenantID)
}

func TestNewGateway(t *testing.T) {
	gateway := NewGateway(testRegistry(t, "qwen", "k"))
	if gateway == nil {
		t.Error("expected Gateway to be non-nil")
	}
}

func TestListChatModels_static(t *testing.T) {
	g := NewGateway(testRegistry(t, "qwen", "fake"))
	models := g.ListChatModels()
	if len(models) == 0 {
		t.Fatal("expected static models, got none")
	}
	for i := 1; i < len(models); i++ {
		if models[i] < models[i-1] {
			t.Errorf("not sorted: %v", models)
			break
		}
	}
	hasQwen, hasGlm := false, false
	for _, m := range models {
		if m == "qwen-turbo" {
			hasQwen = true
		}
		if m == "glm-4-flash" {
			hasGlm = true
		}
	}
	if !hasQwen || !hasGlm {
		t.Errorf("expected qwen-turbo and glm-4-flash in %v", models)
	}
}

func TestCompletionRequestHasToolsField(t *testing.T) {
	req := CompletionRequest{
		Model:    "qwen-turbo",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Tools: []Tool{{
			Type: "function",
			Function: ToolFunction{
				Name:        "get_weather",
				Description: "Get weather",
				Parameters:  map[string]any{"type": "object"},
			},
		}},
		ToolChoice: "auto",
	}
	b, err := json.Marshal(req)
	require.NoError(t, err)
	require.Contains(t, string(b), `"tools"`)
	require.Contains(t, string(b), `"tool_choice"`)
}

func TestMessageHasToolCallFields(t *testing.T) {
	msg := Message{
		Role: "assistant",
		ToolCalls: []ToolCall{{
			ID:   "call_abc",
			Type: "function",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "get_weather", Arguments: `{"city":"Beijing"}`},
		}},
	}
	b, err := json.Marshal(msg)
	require.NoError(t, err)
	require.Contains(t, string(b), `"tool_calls"`)
}

func TestCompletionResponseHasToolCallsField(t *testing.T) {
	resp := CompletionResponse{
		ToolCalls: []ToolCall{{ID: "call_1", Type: "function"}},
	}
	b, err := json.Marshal(resp)
	require.NoError(t, err)
	require.Contains(t, string(b), `"tool_calls"`)
}

func TestGatewayOTelMarksFailureAsError(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	// Registry with no usable provider — will fail on resolve.
	readSettings := func(_ context.Context, _ string) ([]byte, error) {
		return json.Marshal(map[string]any{"llm_api_keys": map[string]string{}})
	}
	reg := NewModelRegistry(readSettings, [32]byte{}, zap.NewNop())
	gateway := NewGateway(reg).WithLogger(zap.NewNop())

	_, err := gateway.CompleteStream(tenantCtx(context.Background(), "test-tenant"), &CompletionRequest{Model: "qwen-turbo"}, func(string) {})
	require.Error(t, err)

	for _, span := range recorder.Ended() {
		if span.Name() == "llm.complete" {
			require.Equal(t, codes.Error, span.Status().Code)
			return
		}
	}
	t.Fatal("llm.complete span not found")
}

func TestGatewayLLMLogsExcludePromptToolAndResponsePayloads(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"model": "qwen-turbo",
			"choices": [{"finish_reason": "stop", "message": {"content": "ok"}}],
			"usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
		}`)
	}))
	defer srv.Close()

	// Build a custom registry that resolves to a Qwen client pointing at our test server.
	readSettings := func(_ context.Context, _ string) ([]byte, error) {
		return json.Marshal(map[string]any{
			"llm_api_keys": map[string]string{"qwen": encryptTestKey("test-key")},
			"base_urls":    map[string]string{"qwen": srv.URL},
		})
	}
	reg := NewModelRegistry(readSettings, testAESKey, zap.NewNop())
	gateway := NewGateway(reg).WithLogger(zap.New(core))

	_, err := gateway.CompleteStream(tenantCtx(context.Background(), "test-tenant"), &CompletionRequest{
		Model:    "qwen-turbo",
		Messages: []Message{{Role: "user", Content: "private prompt"}},
		Tools:    []Tool{{Type: "function", Function: ToolFunction{Name: "private_tool"}}},
	}, func(string) {})
	require.NoError(t, err)

	for _, entry := range logs.All() {
		if entry.Message != "llm.request" && entry.Message != "llm.complete" {
			continue
		}
		for _, field := range entry.Context {
			if field.Key == "messages" || field.Key == "tools" || field.Key == "output" {
				t.Fatalf("%s log contained sensitive payload field %q", entry.Message, field.Key)
			}
		}
	}
}

func TestQwenComplete_ToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"model": "qwen-turbo",
			"choices": [{
				"finish_reason": "tool_calls",
				"message": {
					"role": "assistant",
					"content": null,
					"tool_calls": [{
						"id": "call_001",
						"type": "function",
						"function": {"name": "get_weather", "arguments": "{\"city\":\"Beijing\"}"}
					}]
				}
			}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
		}`)
	}))
	defer srv.Close()

	client := NewQwenClientWithBase("test-key", srv.URL, zap.NewNop())
	resp, err := client.Complete(context.Background(), &CompletionRequest{
		Model:    "qwen-turbo",
		Messages: []Message{{Role: "user", Content: "weather?"}},
	})
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	require.Equal(t, "call_001", resp.ToolCalls[0].ID)
	require.Equal(t, "get_weather", resp.ToolCalls[0].Function.Name)
	require.Equal(t, `{"city":"Beijing"}`, resp.ToolCalls[0].Function.Arguments)
	require.Empty(t, resp.Content)
}

func TestZhipuComplete_ToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"model": "glm-4-flash",
			"choices": [{
				"finish_reason": "tool_calls",
				"message": {
					"role": "assistant",
					"content": null,
					"tool_calls": [{
						"id": "call_002",
						"type": "function",
						"function": {"name": "search", "arguments": "{\"query\":\"Go Temporal\"}"}
					}]
				}
			}],
			"usage": {"prompt_tokens": 8, "completion_tokens": 4, "total_tokens": 12}
		}`)
	}))
	defer srv.Close()

	client := NewZhipuClientWithBase("test-key", srv.URL, zap.NewNop())
	resp, err := client.Complete(context.Background(), &CompletionRequest{
		Model:    "glm-4-flash",
		Messages: []Message{{Role: "user", Content: "search?"}},
	})
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	require.Equal(t, "call_002", resp.ToolCalls[0].ID)
	require.Equal(t, "search", resp.ToolCalls[0].Function.Name)
	require.Equal(t, `{"query":"Go Temporal"}`, resp.ToolCalls[0].Function.Arguments)
	require.Empty(t, resp.Content)
}
