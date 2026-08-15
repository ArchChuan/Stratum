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

func TestOllamaComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/chat", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)

		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Stream bool `json:"stream"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "llama3.2", req.Model)
		require.False(t, req.Stream)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"model": "llama3.2",
			"created_at": "2024-01-01T00:00:00Z",
			"message": {"role": "assistant", "content": "Hello from Ollama!"},
			"done": true,
			"total_duration": 1234567890,
			"eval_count": 5,
			"prompt_eval_count": 10
		}`)
	}))
	defer srv.Close()

	client := infrastructure.NewOllamaClient(
		infrastructure.ProviderConfig{Name: "test-ollama", BaseURL: srv.URL},
		zap.NewNop(),
	)
	resp, err := client.Complete(context.Background(), &infrastructure.CompletionRequest{
		Model:    "llama3.2",
		Messages: []infrastructure.Message{{Role: "user", Content: "Hello"}},
	})
	require.NoError(t, err)
	require.Equal(t, "Hello from Ollama!", resp.Content)
	require.Equal(t, "llama3.2", resp.Model)
	require.Equal(t, 5, resp.Usage.CompletionTokens)
	require.Equal(t, 10, resp.Usage.PromptTokens)
	require.Equal(t, 15, resp.Usage.TotalTokens)
}

func TestOllamaCompleteStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/chat", r.URL.Path)

		var req struct {
			Stream bool `json:"stream"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.True(t, req.Stream)

		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = fmt.Fprint(w,
			`{"model":"llama3.2","message":{"role":"assistant","content":"Hello"},"done":false}`+"\n"+
				`{"model":"llama3.2","message":{"role":"assistant","content":" world"},"done":false}`+"\n"+
				`{"model":"llama3.2","message":{"role":"assistant","content":"!"},"done":true,"eval_count":3,"prompt_eval_count":10}`+"\n",
		)
	}))
	defer srv.Close()

	client := infrastructure.NewOllamaClient(
		infrastructure.ProviderConfig{Name: "test-ollama", BaseURL: srv.URL},
		zap.NewNop(),
	)

	var tokens []string
	resp, err := client.CompleteStream(context.Background(), &infrastructure.CompletionRequest{
		Model:    "llama3.2",
		Messages: []infrastructure.Message{{Role: "user", Content: "Hello"}},
	}, func(token string) {
		tokens = append(tokens, token)
	})
	require.NoError(t, err)
	require.Equal(t, "Hello world!", resp.Content)
	require.Equal(t, []string{"Hello", " world", "!"}, tokens)
	require.Equal(t, 3, resp.Usage.CompletionTokens)
	require.Equal(t, 10, resp.Usage.PromptTokens)
	require.Equal(t, 13, resp.Usage.TotalTokens)
}

// TestOllamaCompleteStream_termination 覆盖 NDJSON 流三种收尾：
// done:true 正常终止成功；内容已输出但连接中断返回 ErrStreamTruncated；
// 空响应返回普通错误（不是截断，不是成功）。
func TestOllamaCompleteStream_termination(t *testing.T) {
	cases := []struct {
		name    string
		write   func(w http.ResponseWriter)
		want    string // 成功时期望的内容
		wantErr error  // 失败时 errors.Is 断言目标
	}{
		{
			name: "done true terminates",
			write: func(w http.ResponseWriter) {
				fmt.Fprint(w, `{"model":"llama3.2","message":{"role":"assistant","content":"hi"},"done":false}`+"\n")
				fmt.Fprint(w, `{"model":"llama3.2","message":{"role":"assistant","content":"!"},"done":true}`+"\n")
			},
			want: "hi!",
		},
		{
			name: "mid-stream disconnect is truncated",
			write: func(w http.ResponseWriter) {
				fmt.Fprint(w, `{"model":"llama3.2","message":{"role":"assistant","content":"hi"},"done":false}`+"\n")
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
				w.Header().Set("Content-Type", "application/x-ndjson")
				tc.write(w)
				w.(http.Flusher).Flush()
			}))
			defer srv.Close()

			client := infrastructure.NewOllamaClient(
				infrastructure.ProviderConfig{Name: "test-ollama", BaseURL: srv.URL},
				zap.NewNop(),
			)
			resp, err := client.CompleteStream(context.Background(),
				&infrastructure.CompletionRequest{Model: "llama3.2"},
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

func TestOllamaHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/tags", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"models":[]}`)
	}))
	defer srv.Close()

	client := infrastructure.NewOllamaClient(
		infrastructure.ProviderConfig{Name: "test-ollama", BaseURL: srv.URL},
		zap.NewNop(),
	)
	require.NoError(t, client.Health(context.Background()))
}

func TestOllamaListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/tags", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"models":[{"name":"llama3.2:latest"},{"name":"mistral:7b"}]}`)
	}))
	defer srv.Close()

	client := infrastructure.NewOllamaClient(
		infrastructure.ProviderConfig{Name: "test-ollama", BaseURL: srv.URL},
		zap.NewNop(),
	)
	models, err := client.ListModels(context.Background())
	require.NoError(t, err)
	require.Equal(t, []infrastructure.DiscoveredModel{
		{Name: "llama3.2:latest"},
		{Name: "mistral:7b"},
	}, models)
}

func TestOllamaCreateEmbeddings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/embeddings", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"embeddings":[[0.1,0.2,0.3],[0.4,0.5,0.6]]}`)
	}))
	defer srv.Close()

	client := infrastructure.NewOllamaClient(
		infrastructure.ProviderConfig{Name: "test-ollama", BaseURL: srv.URL},
		zap.NewNop(),
	)
	resp, err := client.CreateEmbeddings(context.Background(), &infrastructure.EmbeddingRequest{
		Model: "nomic-embed-text",
		Input: []string{"hello", "world"},
	})
	require.NoError(t, err)
	require.Len(t, resp.Embeddings, 2)
	require.Equal(t, []float32{0.1, 0.2, 0.3}, resp.Embeddings[0])
}

func TestOllamaProtocolUsesResolvedProviderConfig(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/chat", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"model": "llama3.2",
			"message": {"role": "assistant", "content": "ok"},
			"done": true
		}`)
	}))
	defer srv.Close()

	template := infrastructure.NewOllamaClient(
		infrastructure.ProviderConfig{Name: "template"},
		zap.NewNop(),
	)
	protocol := infrastructure.NewOllamaProtocol(template)
	resp, err := protocol.Complete(context.Background(),
		infrastructure.ProviderConfig{Name: "tenant-ollama", BaseURL: srv.URL},
		&infrastructure.CompletionRequest{
			Model:    "llama3.2",
			Messages: []infrastructure.Message{{Role: "user", Content: "hello"}},
		},
	)
	require.NoError(t, err)
	require.Equal(t, "ok", resp.Content)
}

func TestOllamaComplete_toolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// ollama 原生 /api/chat 响应:tool_calls 在 message.tool_calls,arguments 为对象
		_, _ = fmt.Fprint(w, `{
			"model": "qwen3",
			"message": {
				"role": "assistant",
				"content": "",
				"tool_calls": [
					{"id": "call_1", "function": {"name": "get_time", "arguments": {"fmt": "HH:MM:SS"}}}
				]
			},
			"done": true
		}`)
	}))
	defer srv.Close()

	client := infrastructure.NewOllamaClient(
		infrastructure.ProviderConfig{Name: "test-ollama", BaseURL: srv.URL},
		zap.NewNop(),
	)
	resp, err := client.Complete(context.Background(), &infrastructure.CompletionRequest{Model: "qwen3"})
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	tc := resp.ToolCalls[0]
	require.Equal(t, "call_1", tc.ID)
	require.Equal(t, "function", tc.Type)
	require.Equal(t, "get_time", tc.Function.Name)
	// arguments 对象 -> JSON 字符串
	require.JSONEq(t, `{"fmt":"HH:MM:SS"}`, tc.Function.Arguments)
}

func TestOllamaCompleteStream_toolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		// ollama 原生流式:tool_calls 单 chunk 全量,后续 chunk 不再重复
		_, _ = fmt.Fprint(w,
			`{"model":"qwen3","message":{"role":"assistant","content":""},"done":false}`+"\n"+
				`{"model":"qwen3","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_9","function":{"index":0,"name":"get_time","arguments":{}}}]},"done":false}`+"\n"+
				`{"model":"qwen3","message":{"role":"assistant","content":""},"done":true,"eval_count":10,"prompt_eval_count":5}`+"\n",
		)
	}))
	defer srv.Close()

	client := infrastructure.NewOllamaClient(
		infrastructure.ProviderConfig{Name: "test-ollama", BaseURL: srv.URL},
		zap.NewNop(),
	)
	resp, err := client.CompleteStream(context.Background(),
		&infrastructure.CompletionRequest{Model: "qwen3"}, func(string) {})
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	tc := resp.ToolCalls[0]
	require.Equal(t, "call_9", tc.ID)
	require.Equal(t, "get_time", tc.Function.Name)
	require.JSONEq(t, `{}`, tc.Function.Arguments)
}

func TestOllamaComplete_preservesToolCallsInRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string         `json:"name"`
						Arguments map[string]any `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		// 多轮恢复:assistant 消息必须带上一轮 tool_calls,arguments 转对象
		require.Len(t, req.Messages, 1)
		require.Equal(t, "assistant", req.Messages[0].Role)
		require.Len(t, req.Messages[0].ToolCalls, 1)
		tc := req.Messages[0].ToolCalls[0]
		require.Equal(t, "call_1", tc.ID)
		require.Equal(t, "get_time", tc.Function.Name)
		require.Equal(t, map[string]any{"fmt": "HH:MM:SS"}, tc.Function.Arguments)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"model":"qwen3","message":{"role":"assistant","content":"ok"},"done":true}`)
	}))
	defer srv.Close()

	client := infrastructure.NewOllamaClient(
		infrastructure.ProviderConfig{Name: "test-ollama", BaseURL: srv.URL},
		zap.NewNop(),
	)
	// 构造带 tool_calls 的 assistant 消息(模拟多轮历史)
	argsMsg := `{"fmt":"HH:MM:SS"}`
	msg := infrastructure.Message{
		Role:    "assistant",
		Content: "",
		ToolCalls: []infrastructure.ToolCall{
			{ID: "call_1", Type: "function", Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "get_time", Arguments: argsMsg}},
		},
	}
	resp, err := client.Complete(context.Background(), &infrastructure.CompletionRequest{
		Model:    "qwen3",
		Messages: []infrastructure.Message{msg},
	})
	require.NoError(t, err)
	require.Equal(t, "ok", resp.Content)
}
