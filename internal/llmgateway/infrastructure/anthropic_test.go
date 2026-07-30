package infrastructure_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
)

func TestAnthropicComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/messages", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "test-api-key", r.Header.Get("x-api-key"))
		require.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))

		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"messages"`
			MaxTokens int `json:"max_tokens"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "claude-sonnet-4-20250514", req.Model)
		require.Equal(t, "user", req.Messages[0].Role)
		require.Equal(t, "text", req.Messages[0].Content[0].Type)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"model": "claude-sonnet-4-20250514",
			"content": [{"type": "text", "text": "Hello from Claude!"}],
			"usage": {"input_tokens": 10, "output_tokens": 5}
		}`)
	}))
	defer srv.Close()

	client := infrastructure.NewAnthropicClient(
		infrastructure.ProviderConfig{Name: "test-anthropic", BaseURL: srv.URL, APIKey: "test-api-key"},
		zap.NewNop(),
	)
	resp, err := client.Complete(context.Background(), &infrastructure.CompletionRequest{
		Model:    "claude-sonnet-4-20250514",
		Messages: []infrastructure.Message{{Role: "user", Content: "Hello"}},
	})
	require.NoError(t, err)
	require.Equal(t, "Hello from Claude!", resp.Content)
	require.Equal(t, 10, resp.Usage.PromptTokens)
	require.Equal(t, 5, resp.Usage.CompletionTokens)
	require.Equal(t, 15, resp.Usage.TotalTokens)
}

func TestAnthropicCompleteWithToolUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"model": "claude-sonnet-4-20250514",
			"content": [{
				"type": "tool_use",
				"id": "toolu_001",
				"name": "get_weather",
				"input": {"city": "Beijing"}
			}],
			"usage": {"input_tokens": 20, "output_tokens": 15}
		}`)
	}))
	defer srv.Close()

	client := infrastructure.NewAnthropicClient(
		infrastructure.ProviderConfig{Name: "test-anthropic", BaseURL: srv.URL, APIKey: "test-api-key"},
		zap.NewNop(),
	)
	resp, err := client.Complete(context.Background(), &infrastructure.CompletionRequest{
		Model:    "claude-sonnet-4-20250514",
		Messages: []infrastructure.Message{{Role: "user", Content: "What's the weather?"}},
		Tools: []infrastructure.Tool{{
			Type: "function",
			Function: infrastructure.ToolFunction{
				Name:        "get_weather",
				Description: "Get weather",
				Parameters:  map[string]any{"type": "object"},
			},
		}},
	})
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	require.Equal(t, "toolu_001", resp.ToolCalls[0].ID)
	require.Equal(t, "tool_use", resp.ToolCalls[0].Type)
	require.Equal(t, "get_weather", resp.ToolCalls[0].Function.Name)
	require.Equal(t, `{"city":"Beijing"}`, resp.ToolCalls[0].Function.Arguments)
	require.Empty(t, resp.Content)
}

func TestAnthropicCompleteStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/messages", r.URL.Path)

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}

		// Simulate Anthropic SSE stream
		events := []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_001\",\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":10}}}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\" world\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"!\"}}\n\n",
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		}
		for _, event := range events {
			_, _ = fmt.Fprint(w, event)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	client := infrastructure.NewAnthropicClient(
		infrastructure.ProviderConfig{Name: "test-anthropic", BaseURL: srv.URL, APIKey: "test-api-key"},
		zap.NewNop(),
	)

	var tokens []string
	resp, err := client.CompleteStream(context.Background(), &infrastructure.CompletionRequest{
		Model:    "claude-sonnet-4-20250514",
		Messages: []infrastructure.Message{{Role: "user", Content: "Hello"}},
	}, func(token string) {
		tokens = append(tokens, token)
	})
	require.NoError(t, err)
	require.Equal(t, "Hello world!", resp.Content)
	require.Equal(t, []string{"Hello", " world", "!"}, tokens)
	require.Equal(t, 10, resp.Usage.PromptTokens)
	require.Equal(t, 3, resp.Usage.CompletionTokens)
}

func TestAnthropicStreamWithToolUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}

		events := []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_001\",\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":10}}}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"name\":\"get_weather\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"city\\\":\\\"\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"Beijing\\\"}\"}}\n\n",
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":15}}\n\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		}
		for _, event := range events {
			_, _ = fmt.Fprint(w, event)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	client := infrastructure.NewAnthropicClient(
		infrastructure.ProviderConfig{Name: "test-anthropic", BaseURL: srv.URL, APIKey: "test-api-key"},
		zap.NewNop(),
	)
	resp, err := client.CompleteStream(context.Background(), &infrastructure.CompletionRequest{
		Model:    "claude-sonnet-4-20250514",
		Messages: []infrastructure.Message{{Role: "user", Content: "Weather in Beijing?"}},
		Tools: []infrastructure.Tool{{
			Type: "function",
			Function: infrastructure.ToolFunction{
				Name:        "get_weather",
				Description: "Get weather",
				Parameters:  map[string]any{"type": "object"},
			},
		}},
	}, func(_ string) {})
	require.NoError(t, err)

	// Check that the tool_use was accumulated correctly.
	toolCallFound := false
	for _, tc := range resp.ToolCalls {
		if tc.Function.Name == "get_weather" {
			toolCallFound = true
			require.Equal(t, `{"city":"Beijing"}`, tc.Function.Arguments)
			require.Equal(t, "tool_use", tc.Type)
		}
	}
	require.True(t, toolCallFound, "expected get_weather tool call in response")
}

func TestAnthropicListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/models", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":[{"id":"claude-sonnet-4-20250514"},{"id":"claude-opus-4-8"}]}`)
	}))
	defer srv.Close()

	client := infrastructure.NewAnthropicClient(
		infrastructure.ProviderConfig{Name: "test-anthropic", BaseURL: srv.URL, APIKey: "test-api-key"},
		zap.NewNop(),
	)
	models, err := client.ListModels(context.Background())
	require.NoError(t, err)
	require.Equal(t, []infrastructure.DiscoveredModel{
		{Name: "claude-sonnet-4-20250514"},
		{Name: "claude-opus-4-8"},
	}, models)
}

func TestAnthropicProtocolUsesResolvedProviderConfig(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/messages", r.URL.Path)
		require.Equal(t, "tenant-key", r.Header.Get("x-api-key"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"model": "claude-sonnet-4-20250514",
			"content": [{"type": "text", "text": "ok"}],
			"usage": {"input_tokens": 5, "output_tokens": 1}
		}`)
	}))
	defer srv.Close()

	template := infrastructure.NewAnthropicClient(
		infrastructure.ProviderConfig{Name: "template"},
		zap.NewNop(),
	)
	protocol := infrastructure.NewAnthropicProtocol(template)
	resp, err := protocol.Complete(context.Background(),
		infrastructure.ProviderConfig{Name: "tenant-anthropic", BaseURL: srv.URL, APIKey: "tenant-key"},
		&infrastructure.CompletionRequest{
			Model:    "claude-sonnet-4-20250514",
			Messages: []infrastructure.Message{{Role: "user", Content: "hello"}},
		},
	)
	require.NoError(t, err)
	require.Equal(t, "ok", resp.Content)
}

func TestAnthropicBuildRequestSystemMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			System string `json:"system"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "You are a helpful assistant.", req.System)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"model": "claude-sonnet-4-20250514",
			"content": [{"type": "text", "text": "I am Claude."}],
			"usage": {"input_tokens": 12, "output_tokens": 4}
		}`)
	}))
	defer srv.Close()

	client := infrastructure.NewAnthropicClient(
		infrastructure.ProviderConfig{Name: "test-anthropic", BaseURL: srv.URL, APIKey: "test-api-key"},
		zap.NewNop(),
	)
	resp, err := client.Complete(context.Background(), &infrastructure.CompletionRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []infrastructure.Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Who are you?"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "I am Claude.", resp.Content)
}

func TestAnthropicBuildRequestMaxTokensDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			MaxTokens int `json:"max_tokens"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, 4096, req.MaxTokens) // default when MaxTokens is 0

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"model": "claude-sonnet-4-20250514",
			"content": [{"type": "text", "text": "ok"}],
			"usage": {"input_tokens": 5, "output_tokens": 1}
		}`)
	}))
	defer srv.Close()

	client := infrastructure.NewAnthropicClient(
		infrastructure.ProviderConfig{Name: "test-anthropic", BaseURL: srv.URL, APIKey: "test-api-key"},
		zap.NewNop(),
	)
	_, err := client.Complete(context.Background(), &infrastructure.CompletionRequest{
		Model:    "claude-sonnet-4-20250514",
		Messages: []infrastructure.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
}

func TestAnthropicToolsInRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tools []struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				InputSchema map[string]any `json:"input_schema"`
			} `json:"tools"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Len(t, req.Tools, 1)
		require.Equal(t, "get_weather", req.Tools[0].Name)
		require.Equal(t, "Get weather for a city", req.Tools[0].Description)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"model": "claude-sonnet-4-20250514",
			"content": [{"type": "text", "text": "ok"}],
			"usage": {"input_tokens": 5, "output_tokens": 1}
		}`)
	}))
	defer srv.Close()

	client := infrastructure.NewAnthropicClient(
		infrastructure.ProviderConfig{Name: "test-anthropic", BaseURL: srv.URL, APIKey: "test-api-key"},
		zap.NewNop(),
	)
	_, err := client.Complete(context.Background(), &infrastructure.CompletionRequest{
		Model:    "claude-sonnet-4-20250514",
		Messages: []infrastructure.Message{{Role: "user", Content: "Weather?"}},
		Tools: []infrastructure.Tool{{
			Type: "function",
			Function: infrastructure.ToolFunction{
				Name:        "get_weather",
				Description: "Get weather for a city",
				Parameters:  map[string]any{"type": "object"},
			},
		}},
	})
	require.NoError(t, err)
}
