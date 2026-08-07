package infrastructure_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
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

func TestAnthropicClient_Health_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/messages", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"model":"claude-3-haiku","content":[{"type":"text","text":"ok"}],"usage":{}}`)
	}))
	defer srv.Close()

	client := infrastructure.NewAnthropicClient(
		infrastructure.ProviderConfig{Name: "test-anthropic", BaseURL: srv.URL, APIKey: "test-api-key", HealthModel: "claude-3-haiku"},
		zap.NewNop(),
	)
	require.NoError(t, client.Health(context.Background()))
}

func TestAnthropicClient_Health_upstreamFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := infrastructure.NewAnthropicClient(
		infrastructure.ProviderConfig{Name: "test-anthropic", BaseURL: srv.URL, APIKey: "test-api-key", HealthModel: "claude-3-haiku"},
		zap.NewNop(),
	)
	err := client.Health(context.Background())
	require.Error(t, err)
	require.NotContains(t, err.Error(), srv.URL, "upstream error must not leak internal BaseURL")
}

func TestAnthropicClient_ListModels_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/models", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":[
			{"id":"claude-sonnet-4","context_window":200000},
			{"id":"claude-haiku-4","context_window":100000}
		]}`)
	}))
	defer srv.Close()

	client := infrastructure.NewAnthropicClient(
		infrastructure.ProviderConfig{Name: "test-anthropic", BaseURL: srv.URL, APIKey: "test-api-key"},
		zap.NewNop(),
	)
	models, err := client.ListModels(context.Background())
	require.NoError(t, err)
	require.Equal(t, []infrastructure.DiscoveredModel{
		{Name: "claude-sonnet-4", ContextWindow: 200000},
		{Name: "claude-haiku-4", ContextWindow: 100000},
	}, models)
}

func TestAnthropicClient_ListModels_nonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := infrastructure.NewAnthropicClient(
		infrastructure.ProviderConfig{Name: "test-anthropic", BaseURL: srv.URL, APIKey: "test-api-key"},
		zap.NewNop(),
	)
	_, err := client.ListModels(context.Background())
	require.ErrorContains(t, err, "请检查 provider kind")
	require.ErrorIs(t, err, domain.ErrUpstreamRequestFailed)
	require.NotContains(t, err.Error(), srv.URL, "upstream error must not leak internal BaseURL")
}

func TestAnthropicClient_ListModels_badJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data": [broken`)
	}))
	defer srv.Close()

	client := infrastructure.NewAnthropicClient(
		infrastructure.ProviderConfig{Name: "test-anthropic", BaseURL: srv.URL, APIKey: "test-api-key"},
		zap.NewNop(),
	)
	_, err := client.ListModels(context.Background())
	require.ErrorContains(t, err, "decode models")
}

func TestAnthropicProtocol_Health_delegates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"model":"claude-3-haiku","content":[{"type":"text","text":"ok"}],"usage":{}}`)
	}))
	defer srv.Close()

	client := infrastructure.NewAnthropicClient(
		infrastructure.ProviderConfig{Name: "template", BaseURL: "http://template.test", APIKey: "k"},
		zap.NewNop(),
	)
	protocol := infrastructure.NewAnthropicProtocol(client)
	err := protocol.Health(context.Background(), infrastructure.ProviderConfig{
		Name: "tenant-anthropic", BaseURL: srv.URL, APIKey: "tenant-key",
	})
	require.NoError(t, err)
}

func TestAnthropicProtocol_ListModels_delegates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/models", r.URL.Path)
		require.Equal(t, "tenant-key", r.Header.Get("x-api-key"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":[{"id":"claude-sonnet-4"}]}`)
	}))
	defer srv.Close()

	client := infrastructure.NewAnthropicClient(
		infrastructure.ProviderConfig{Name: "template", BaseURL: "http://template.test", APIKey: "k"},
		zap.NewNop(),
	)
	protocol := infrastructure.NewAnthropicProtocol(client)
	models, err := protocol.ListModels(context.Background(), infrastructure.ProviderConfig{
		Name: "tenant-anthropic", BaseURL: srv.URL, APIKey: "tenant-key",
	})
	require.NoError(t, err)
	require.Equal(t, []infrastructure.DiscoveredModel{{Name: "claude-sonnet-4"}}, models)
}

func TestAnthropicProtocol_CompleteStream_delegates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}
		_, _ = fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_001\",\"model\":\"claude-haiku\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	client := infrastructure.NewAnthropicClient(
		infrastructure.ProviderConfig{Name: "template", BaseURL: "http://template.test", APIKey: "k"},
		zap.NewNop(),
	)
	protocol := infrastructure.NewAnthropicProtocol(client)

	var tokens []string
	resp, err := protocol.CompleteStream(context.Background(), infrastructure.ProviderConfig{
		Name: "tenant-anthropic", BaseURL: srv.URL, APIKey: "tenant-key",
	}, &infrastructure.CompletionRequest{
		Model:    "claude-haiku",
		Messages: []infrastructure.Message{{Role: "user", Content: "hi"}},
	}, func(tok string) { tokens = append(tokens, tok) })
	require.NoError(t, err)
	require.Equal(t, "hi", resp.Content)
	require.Equal(t, []string{"hi"}, tokens)
}

// TestAnthropicCompleteStream_termination 覆盖 SSE 流三种收尾：
// message_stop 正常终止成功；内容已输出但连接中断返回 ErrStreamTruncated；
// 空响应返回普通错误（不是截断，不是成功）。
func TestAnthropicCompleteStream_termination(t *testing.T) {
	cases := []struct {
		name    string
		write   func(w http.ResponseWriter)
		want    string // 成功时期望的内容
		wantErr error  // 失败时 errors.Is 断言目标
	}{
		{
			name: "message_stop terminates",
			write: func(w http.ResponseWriter) {
				fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")
				fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			},
			want: "hi",
		},
		{
			name: "mid-stream disconnect is truncated",
			write: func(w http.ResponseWriter) {
				fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")
			},
			wantErr: domain.ErrStreamTruncated,
		},
		{
			name:    "empty response is an error",
			write:   func(w http.ResponseWriter) {},
			wantErr: io.EOF,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				tc.write(w)
				w.(http.Flusher).Flush()
			}))
			defer srv.Close()

			client := infrastructure.NewAnthropicClient(
				infrastructure.ProviderConfig{Name: "test-anthropic", BaseURL: srv.URL, APIKey: "test-api-key"},
				zap.NewNop(),
			)
			resp, err := client.CompleteStream(context.Background(),
				&infrastructure.CompletionRequest{Model: "claude-haiku"},
				func(string) {})
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, resp.Content)
		})
	}
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
