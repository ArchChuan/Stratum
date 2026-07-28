package infrastructure_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// errChatProto returns errors on all chat methods.
type errChatProto struct{}

func (errChatProto) Complete(ctx context.Context, cfg infrastructure.ProviderConfig, req *infrastructure.CompletionRequest) (*infrastructure.CompletionResponse, error) {
	return nil, errors.New("provider error")
}
func (errChatProto) CompleteStream(ctx context.Context, cfg infrastructure.ProviderConfig, req *infrastructure.CompletionRequest, onToken func(string)) (*infrastructure.CompletionResponse, error) {
	return nil, errors.New("provider error")
}
func (errChatProto) Health(ctx context.Context, cfg infrastructure.ProviderConfig) error { return errors.New("provider error") }
func (errChatProto) ListModels(ctx context.Context, cfg infrastructure.ProviderConfig) ([]string, error) { return nil, nil }

// successChatProto returns successful responses.
type successChatProto struct{}

func (successChatProto) Complete(ctx context.Context, cfg infrastructure.ProviderConfig, req *infrastructure.CompletionRequest) (*infrastructure.CompletionResponse, error) {
	return &infrastructure.CompletionResponse{Content: "ok", Usage: infrastructure.TokenUsage{PromptTokens: 1, CompletionTokens: 1}}, nil
}
func (successChatProto) CompleteStream(ctx context.Context, cfg infrastructure.ProviderConfig, req *infrastructure.CompletionRequest, onToken func(string)) (*infrastructure.CompletionResponse, error) {
	onToken("test")
	return &infrastructure.CompletionResponse{Content: "test", Usage: infrastructure.TokenUsage{PromptTokens: 1, CompletionTokens: 1}}, nil
}
func (successChatProto) Health(ctx context.Context, cfg infrastructure.ProviderConfig) error { return nil }
func (successChatProto) ListModels(ctx context.Context, cfg infrastructure.ProviderConfig) ([]string, error) { return nil, nil }

func TestNewGateway(t *testing.T) {
	gateway := infrastructure.NewGateway(nil, nil, nil)
	if gateway == nil {
		t.Error("expected Gateway to be non-nil")
	}
}

func TestCompletionRequestHasToolsField(t *testing.T) {
	req := infrastructure.CompletionRequest{
		Model:    "qwen-turbo",
		Messages: []infrastructure.Message{{Role: "user", Content: "hi"}},
		Tools: []infrastructure.Tool{{
			Type: "function",
			Function: infrastructure.ToolFunction{
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
	msg := infrastructure.Message{
		Role: "assistant",
		ToolCalls: []infrastructure.ToolCall{{
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
	resp := infrastructure.CompletionResponse{
		ToolCalls: []infrastructure.ToolCall{{ID: "call_1", Type: "function"}},
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

	modelRepo := &mockModelRepo{
		models: []domain.Model{
			{ID: "m1", ProviderID: "p1", Name: "qwen-turbo", Enabled: true},
		},
	}
	providerRepo := &mockProviderRepo{
		providers: map[string]*domain.Provider{
			"p1": {
				ID: "p1", Name: "Test Qwen", Kind: domain.ProviderOpenAICompat,
				BaseURL: "https://api.test", APIKey: "sk-test", DefaultModel: "qwen-turbo",
			},
		},
	}
	chatProtos := map[domain.ProviderKind]infrastructure.ChatProtocol{
		domain.ProviderOpenAICompat: errChatProto{},
	}
	embedProtos := map[domain.ProviderKind]infrastructure.EmbedProtocol{}
	reg := infrastructure.NewModelRegistry(modelRepo, providerRepo, chatProtos, embedProtos, 5*time.Minute)

	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")
	gateway := infrastructure.NewGateway(reg, chatProtos, embedProtos).WithLogger(zap.NewNop())
	_, err := gateway.CompleteStream(ctx, &infrastructure.CompletionRequest{Model: "qwen-turbo"}, func(string) {})
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

	modelRepo := &mockModelRepo{
		models: []domain.Model{
			{ID: "m1", ProviderID: "p1", Name: "qwen-turbo", Enabled: true},
		},
	}
	providerRepo := &mockProviderRepo{
		providers: map[string]*domain.Provider{
			"p1": {
				ID: "p1", Name: "Test Qwen", Kind: domain.ProviderOpenAICompat,
				BaseURL: "https://api.test", APIKey: "sk-test", DefaultModel: "qwen-turbo",
			},
		},
	}
	chatProtos := map[domain.ProviderKind]infrastructure.ChatProtocol{
		domain.ProviderOpenAICompat: successChatProto{},
	}
	embedProtos := map[domain.ProviderKind]infrastructure.EmbedProtocol{}
	reg := infrastructure.NewModelRegistry(modelRepo, providerRepo, chatProtos, embedProtos, 5*time.Minute)

	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")
	gateway := infrastructure.NewGateway(reg, chatProtos, embedProtos).WithLogger(zap.New(core))

	_, err := gateway.CompleteStream(ctx, &infrastructure.CompletionRequest{
		Model:    "qwen-turbo",
		Messages: []infrastructure.Message{{Role: "user", Content: "private prompt"}},
		Tools:    []infrastructure.Tool{{Type: "function", Function: infrastructure.ToolFunction{Name: "private_tool"}}},
	}, func(string) {})
	require.NoError(t, err)

	for _, entry := range logs.All() {
		if entry.Message != "llm.request" && entry.Message != "llm.complete" && entry.Message != "llm.response" {
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

	client := infrastructure.NewQwenClientWithBase("test-key", srv.URL, zap.NewNop())
	resp, err := client.Complete(context.Background(), &infrastructure.CompletionRequest{
		Model:    "qwen-turbo",
		Messages: []infrastructure.Message{{Role: "user", Content: "weather?"}},
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

	client := infrastructure.NewZhipuClientWithBase("test-key", srv.URL, zap.NewNop())
	resp, err := client.Complete(context.Background(), &infrastructure.CompletionRequest{
		Model:    "glm-4-flash",
		Messages: []infrastructure.Message{{Role: "user", Content: "search?"}},
	})
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	require.Equal(t, "call_002", resp.ToolCalls[0].ID)
	require.Equal(t, "search", resp.ToolCalls[0].Function.Name)
	require.Equal(t, `{"query":"Go Temporal"}`, resp.ToolCalls[0].Function.Arguments)
	require.Empty(t, resp.Content)
}
