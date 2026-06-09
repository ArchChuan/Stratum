package llmgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNewGateway(t *testing.T) {
	gateway := NewGateway()
	if gateway == nil {
		t.Error("expected Gateway to be non-nil")
	}
}

func TestListChatModels_empty(t *testing.T) {
	g := NewGateway()
	models := g.ListChatModels()
	if len(models) != 0 {
		t.Errorf("expected empty, got %v", models)
	}
}

func TestListChatModels_sorted(t *testing.T) {
	g := NewGateway()
	g.RegisterClient(ProviderZhipu, &ZhipuClient{})
	g.RegisterClient(ProviderQwen, &QwenClient{})

	models := g.ListChatModels()
	if len(models) == 0 {
		t.Fatal("expected models, got none")
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
