package llmgateway

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
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
