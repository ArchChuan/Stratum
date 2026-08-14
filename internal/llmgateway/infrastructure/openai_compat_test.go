package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestComplete_400ContextLengthExceeded 验证协议层对 400 响应体的
// context_length_exceeded 语义化：识别为 ErrContextLengthExceeded（永久、
// 不重试，可被 agent 层降级探测）；其他 400 保持普通 status 错误不变。
func TestComplete_400ContextLengthExceeded(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantCLE bool // 期望被识别为上下文超限
	}{
		{
			name:    "error.code is context_length_exceeded",
			body:    `{"error":{"code":"context_length_exceeded","message":"maximum context length reached"}}`,
			wantCLE: true,
		},
		{
			name:    "message contains maximum context length",
			body:    `{"error":{"code":"invalid_request_error","message":"This model's maximum context length is 32768 tokens"}}`,
			wantCLE: true,
		},
		{
			name:    "message contains context_length_exceeded",
			body:    `{"error":{"message":"request exceeds context_length_exceeded"}}`,
			wantCLE: true,
		},
		{
			name:    "other 400 stays plain status error",
			body:    `{"error":{"code":"invalid_request_error","message":"schema mismatch"}}`,
			wantCLE: false,
		},
		{
			name:    "non-JSON 400 body stays plain",
			body:    "plain text error page",
			wantCLE: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			client := NewOpenAICompatClient(ProviderConfig{
				Name: "test-openai", BaseURL: srv.URL, APIKey: "sk-test",
			}, zap.NewNop())
			resp, err := client.Complete(context.Background(),
				&CompletionRequest{Model: "m1", Messages: []Message{{Role: "user", Content: "hi"}}})
			require.Nil(t, resp)
			require.Error(t, err)
			if tc.wantCLE {
				require.True(t, IsContextLengthExceeded(err), "err = %v", err)
				require.False(t, isTransient(err), "context length exceeded must be permanent")
			} else {
				require.False(t, IsContextLengthExceeded(err), "err = %v", err)
				require.Contains(t, err.Error(), "status 400")
			}
		})
	}
}

var maxTokensFallbackCases = []struct {
	name      string
	maxTokens int
	want      int // 期望出现在请求体 max_tokens 中的值
}{
	{
		name:      "zero falls back to DefaultOutputReserveTokens",
		maxTokens: 0,
		want:      constants.DefaultOutputReserveTokens,
	},
	{
		name:      "positive value preserved",
		maxTokens: 2048,
		want:      2048,
	},
}

// maxTokensEchoServer 返回一个 httptest server：解码请求体把 max_tokens
// 写入 seen channel，然后返回一次成功的 chat/completions 响应（stream
// 模式返回 SSE 直至 [DONE]）。断言在测试 goroutine 中进行，不在 handler 内。
func maxTokensEchoServer(t *testing.T, stream bool) (*httptest.Server, <-chan int) {
	t.Helper()
	seen := make(chan int, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MaxTokens int `json:"max_tokens"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		seen <- body.MaxTokens
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}],"model":"m1"}`)
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

// TestComplete_MaxTokensFallback 验证 Complete 对调用方传 MaxTokens<=0 的
// 请求在 marshal 前兜底为 constants.DefaultOutputReserveTokens（供应商
// 要求 minimum:1），且不就地修改调用方的 req 对象。
func TestComplete_MaxTokensFallback(t *testing.T) {
	for _, tc := range maxTokensFallbackCases {
		t.Run(tc.name, func(t *testing.T) {
			srv, seen := maxTokensEchoServer(t, false)
			client := NewOpenAICompatClient(ProviderConfig{
				Name: "test-openai", BaseURL: srv.URL, APIKey: "sk-test",
			}, zap.NewNop())
			callerReq := &CompletionRequest{
				Model: "m1", Messages: []Message{{Role: "user", Content: "hi"}},
				MaxTokens: tc.maxTokens,
			}
			_, err := client.Complete(context.Background(), callerReq)
			require.NoError(t, err)
			require.Equal(t, tc.want, <-seen)
			require.Equal(t, tc.maxTokens, callerReq.MaxTokens, "caller req must not be mutated")
		})
	}
}

// TestCompleteStream_MaxTokensFallback 与 Complete 相同的网关层防御断言，
// 覆盖流式路径。
func TestCompleteStream_MaxTokensFallback(t *testing.T) {
	for _, tc := range maxTokensFallbackCases {
		t.Run(tc.name, func(t *testing.T) {
			srv, seen := maxTokensEchoServer(t, true)
			client := NewOpenAICompatClient(ProviderConfig{
				Name: "test-openai", BaseURL: srv.URL, APIKey: "sk-test",
			}, zap.NewNop())
			callerReq := &CompletionRequest{
				Model: "m1", Messages: []Message{{Role: "user", Content: "hi"}},
				MaxTokens: tc.maxTokens,
			}
			resp, err := client.CompleteStream(context.Background(), callerReq, func(string) {})
			require.NoError(t, err)
			require.Equal(t, "hi", resp.Content)
			require.Equal(t, tc.want, <-seen)
			require.Equal(t, tc.maxTokens, callerReq.MaxTokens, "caller req must not be mutated")
		})
	}
}

// TestOpenAICompatClient_ListModels_paginates 验证 has_more/last_id 翻页：
// 第一页不带查询参数（最大兼容），后续页带 limit=100&after=<last_id>，
// 聚合所有页并去分页终止。
func TestOpenAICompatClient_ListModels_paginates(t *testing.T) {
	var rawQueries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/models", r.URL.Path)
		rawQueries = append(rawQueries, r.URL.RawQuery)
		switch r.URL.Query().Get("after") {
		case "":
			_, _ = fmt.Fprint(w, `{"data":[{"id":"gpt-4o"},{"id":"gpt-4.1"}],"has_more":true,"last_id":"gpt-4.1"}`)
		case "gpt-4.1":
			_, _ = fmt.Fprint(w, `{"data":[{"id":"text-embedding-3-small"},{"id":"bge-m3"}],"has_more":false,"last_id":""}`)
		default:
			t.Errorf("unexpected after=%q", r.URL.Query().Get("after"))
		}
	}))
	defer srv.Close()

	client := NewOpenAICompatClient(ProviderConfig{
		Name: "test-openai", BaseURL: srv.URL, APIKey: "sk-test",
	}, zap.NewNop())
	models, err := client.ListModels(context.Background())
	require.NoError(t, err)
	require.Equal(t, []DiscoveredModel{
		{Name: "gpt-4o", ContextWindow: 128_000, MaxOutputTokens: 16_384},
		{Name: "gpt-4.1", ContextWindow: 1_047_576, MaxOutputTokens: 32_768},
		{Name: "text-embedding-3-small", ContextWindow: 8_191, MaxOutputTokens: 0},
		{Name: "bge-m3"},
	}, models)
	require.Len(t, rawQueries, 2)
	require.Equal(t, "", rawQueries[0], "first page must omit query params")
	require.Equal(t, "limit=100&after=gpt-4.1", rawQueries[1])
}

// TestOpenAICompatClient_ListModels_singlePageWithoutPagination 验证不返回
// 分页字段的兼容网关只请求一次，行为与分页改造前一致。
func TestOpenAICompatClient_ListModels_singlePageWithoutPagination(t *testing.T) {
	var reqCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		_, _ = fmt.Fprint(w, `{"data":[{"id":"gpt-4o"}]}`)
	}))
	defer srv.Close()

	client := NewOpenAICompatClient(ProviderConfig{
		Name: "test-openai", BaseURL: srv.URL, APIKey: "sk-test",
	}, zap.NewNop())
	models, err := client.ListModels(context.Background())
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, 1, reqCount)
}
