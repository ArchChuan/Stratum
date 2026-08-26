package infrastructure

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

// openAICompatServer 用 httptest 模拟 OpenAI-compatible 端点，计数调用。
type openAICompatServer struct {
	mu         sync.Mutex
	chatCalls  int
	embedCalls int
	modelCalls int
}

func newOpenAICompatServer(t *testing.T) (*openAICompatServer, *httptest.Server) {
	s := &openAICompatServer{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			s.mu.Lock()
			s.chatCalls++
			s.mu.Unlock()
			if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"model\":\"m1\",\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n\n")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"t1\",\"type\":\"function\",\"function\":{\"name\":\"f\",\"arguments\":\"{\\\"a\\\":\"}}]}}]}\n\n")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"1}\"}}]}}]}\n\n")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"llo\"}}]}\n\n")
				fmt.Fprint(w, "data: [DONE]\n\n")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"model":"m1","choices":[{"message":{"content":"hi","tool_calls":[]}}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
		case strings.HasSuffix(r.URL.Path, "/embeddings"):
			s.mu.Lock()
			s.embedCalls++
			s.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"embedding":[0.1,0.2]},{"embedding":[0.3,0.4]}]}`)
		case strings.HasSuffix(r.URL.Path, "/models"):
			s.mu.Lock()
			s.modelCalls++
			s.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"qwen-turbo"},{"id":"text-embed"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return s, ts
}

func openAICompatProtocolFixture(t *testing.T) (*OpenAICompatProtocol, *openAICompatServer, string) {
	srv, ts := newOpenAICompatServer(t)
	client := NewOpenAICompatClient(ProviderConfig{
		Name: "test-openai", BaseURL: ts.URL, APIKey: "sk-test", HealthModel: "qwen-turbo",
	}, zap.NewNop())
	return NewOpenAICompatProtocol(client), srv, ts.URL
}

func TestOpenAICompatProtocol_Complete(t *testing.T) {
	proto, srv, baseURL := openAICompatProtocolFixture(t)

	resp, err := proto.Complete(context.Background(), ProviderConfig{Name: "x", BaseURL: baseURL},
		&CompletionRequest{Model: "m1", Messages: []Message{{Role: "user", Content: "hi"}}})
	require.NoError(t, err)
	require.Equal(t, "hi", resp.Content)
	require.Equal(t, "m1", resp.Model)
	require.Equal(t, 3, resp.Usage.PromptTokens)
	require.Equal(t, 5, resp.Usage.TotalTokens)
	require.GreaterOrEqual(t, srv.chatCalls, 1)
}

func TestOpenAICompatProtocol_CompleteStream(t *testing.T) {
	proto, _, baseURL := openAICompatProtocolFixture(t)

	var tokens []string
	resp, err := proto.CompleteStream(context.Background(), ProviderConfig{Name: "x", BaseURL: baseURL},
		&CompletionRequest{Model: "m1"}, func(tok string) { tokens = append(tokens, tok) })
	require.NoError(t, err)
	require.Equal(t, "hello", resp.Content)
	require.Equal(t, []string{"he", "llo"}, tokens)
	require.Len(t, resp.ToolCalls, 1)
	require.Equal(t, "t1", resp.ToolCalls[0].ID)
	require.Equal(t, "f", resp.ToolCalls[0].Function.Name)
	require.Equal(t, `{"a":1}`, resp.ToolCalls[0].Function.Arguments)
}

// TestOpenAICompatCompleteStream_termination 覆盖 SSE 流三种收尾：
// 正常终止（[DONE] 或 finish_reason）成功；内容已输出但连接中断返回
// ErrStreamTruncated；空响应返回普通错误（不是截断，不是成功）。
func TestOpenAICompatCompleteStream_termination(t *testing.T) {
	cases := []struct {
		name    string
		write   func(w http.ResponseWriter)
		want    string // 成功时期望的内容
		wantErr error  // 失败时 errors.Is 断言目标
	}{
		{
			name: "normal termination via [DONE]",
			write: func(w http.ResponseWriter) {
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
				fmt.Fprint(w, "data: [DONE]\n\n")
			},
			want: "hi",
		},
		{
			name: "finish_reason chunk without [DONE] terminates",
			write: func(w http.ResponseWriter) {
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			},
			want: "hi",
		},
		{
			name: "mid-stream disconnect is truncated",
			write: func(w http.ResponseWriter) {
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
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

			client := NewOpenAICompatClient(ProviderConfig{
				Name: "test-openai", BaseURL: srv.URL, APIKey: "sk-test",
			}, zap.NewNop())
			resp, err := client.CompleteStream(context.Background(),
				&CompletionRequest{Model: "m1"}, func(string) {})
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, resp.Content)
		})
	}
}

func TestOpenAICompatProtocol_Health(t *testing.T) {
	proto, _, baseURL := openAICompatProtocolFixture(t)

	require.NoError(t, proto.Health(context.Background(), ProviderConfig{Name: "x", BaseURL: baseURL}))
}

func TestOpenAICompatProtocol_ListModels(t *testing.T) {
	proto, srv, baseURL := openAICompatProtocolFixture(t)

	models, err := proto.ListModels(context.Background(), ProviderConfig{Name: "x", BaseURL: baseURL})
	require.NoError(t, err)
	require.Len(t, models, 2)
	require.Equal(t, "qwen-turbo", models[0].Name)
	require.GreaterOrEqual(t, srv.modelCalls, 1)
}

func TestOpenAICompatProtocol_CreateEmbeddings(t *testing.T) {
	proto, srv, baseURL := openAICompatProtocolFixture(t)

	resp, err := proto.CreateEmbeddings(context.Background(), ProviderConfig{Name: "x", BaseURL: baseURL},
		&EmbeddingRequest{Model: "text-embed", Input: []string{"a"}})
	require.NoError(t, err)
	require.Len(t, resp.Embeddings, 2)
	require.Equal(t, []float32{0.1, 0.2}, resp.Embeddings[0])
	require.GreaterOrEqual(t, srv.embedCalls, 1)
}

func TestOpenAICompatProtocol_BatchSize(t *testing.T) {
	proto, _, _ := openAICompatProtocolFixture(t)

	// 默认批次与智谱 embedding-3 单请求 ≤64 条上限对齐。
	require.Equal(t, defaultEmbedBatchSize, proto.BatchSize())

	client := NewOpenAICompatClient(ProviderConfig{Name: "x", EmbedBatchSize: 7}, zap.NewNop())
	require.Equal(t, 7, NewOpenAICompatProtocol(client).BatchSize())

	// 显式配置超过平台上限也必须被 clamp，保证单请求不超上游限制。
	big := NewOpenAICompatClient(ProviderConfig{Name: "x", EmbedBatchSize: 100}, zap.NewNop())
	require.Equal(t, defaultEmbedBatchSize, NewOpenAICompatProtocol(big).BatchSize())
}

func TestOpenAICompatProtocol_clientFor_isolatesBreakers(t *testing.T) {
	proto, _, _ := openAICompatProtocolFixture(t)

	cfgA := ProviderConfig{Name: "a", BaseURL: "http://one", APIKey: "k1"}
	cfgB := ProviderConfig{Name: "b", BaseURL: "http://two", APIKey: "k2"}
	clientA1 := proto.clientFor(cfgA, "model-a")
	clientA2 := proto.clientFor(cfgA, "model-a")
	clientAOther := proto.clientFor(cfgA, "model-b")
	clientB := proto.clientFor(cfgB, "model-a")

	require.Same(t, clientA1.breaker, clientA2.breaker)
	require.NotSame(t, clientA1.breaker, clientAOther.breaker)
	require.NotSame(t, clientA1.breaker, clientB.breaker)
}

func TestOpenAICompatClient_ChatDelegates(t *testing.T) {
	_, ts := newOpenAICompatServer(t)
	client := NewOpenAICompatClient(ProviderConfig{
		Name: "test-openai", BaseURL: ts.URL, APIKey: "sk-test", HealthModel: "qwen-turbo",
	}, zap.NewNop())

	t.Run("ChatComplete delegates", func(t *testing.T) {
		resp, err := client.ChatComplete(context.Background(), ProviderConfig{},
			&CompletionRequest{Model: "m1", Messages: []Message{{Role: "user", Content: "hi"}}})
		require.NoError(t, err)
		require.Equal(t, "hi", resp.Content)
	})

	t.Run("ChatCompleteStream delegates", func(t *testing.T) {
		resp, err := client.ChatCompleteStream(context.Background(), ProviderConfig{},
			&CompletionRequest{Model: "m1"}, func(string) {})
		require.NoError(t, err)
		require.Equal(t, "hello", resp.Content)
	})

	t.Run("ChatHealth delegates", func(t *testing.T) {
		require.NoError(t, client.ChatHealth(context.Background(), ProviderConfig{}))
	})

	t.Run("ChatListModels delegates", func(t *testing.T) {
		models, err := client.ChatListModels(context.Background(), ProviderConfig{})
		require.NoError(t, err)
		require.Len(t, models, 2)
	})

	t.Run("EmbedCreateEmbeddings delegates", func(t *testing.T) {
		resp, err := client.EmbedCreateEmbeddings(context.Background(), ProviderConfig{},
			&EmbeddingRequest{Model: "text-embed", Input: []string{"a"}})
		require.NoError(t, err)
		require.Len(t, resp.Embeddings, 2)
	})

	t.Run("EmbedBatchSize delegates", func(t *testing.T) {
		require.Equal(t, defaultEmbedBatchSize, client.EmbedBatchSize())
		client.cfg.EmbedBatchSize = 3
		require.Equal(t, 3, client.EmbedBatchSize())
		// 超过平台上限的配置被 clamp，代码层面保证分批安全。
		client.cfg.EmbedBatchSize = 200
		require.Equal(t, defaultEmbedBatchSize, client.EmbedBatchSize())
	})
}
