package infrastructure

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newPerModelServer 按路径 + 请求体中的 model 名分派：model-a 恒返回 500
// （触发熔断），其余模型按路径返回合法的 chat/embed 响应。
func newPerModelServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"model-a"`)) {
			http.Error(w, `{"error":{"message":"boom"}}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/embeddings":
			fmt.Fprint(w, `{"data":[{"embedding":[0.1,0.2]},{"embedding":[0.3,0.4]}]}`)
		default:
			fmt.Fprint(w, `{"model":"ok","choices":[{"message":{"content":"hi","tool_calls":[]}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

// TestPerModelBreaker_IsolatesSameProviderModels 验证 M3：熔断器 key 含 model
// 维度后，同一 provider 下一个模型连续失败熔断，同 provider 其它模型不受影响；
// 业务调用同步驱动 HealthRegistry（model-a degraded、model-b healthy）。
func TestPerModelBreaker_IsolatesSameProviderModels(t *testing.T) {
	ts := newPerModelServer(t)
	health := NewHealthRegistry(nil)
	proto := NewOpenAICompatProtocol(NewOpenAICompatClient(
		ProviderConfig{Name: "test", BaseURL: ts.URL, APIKey: "k", HealthModel: "model-b"},
		zap.NewNop(),
	)).WithHealth(health)
	cfg := ProviderConfig{Name: "test", BaseURL: ts.URL, APIKey: "k"}
	ctx := context.Background()
	msg := []Message{{Role: "user", Content: "hi"}}

	// model-a 连续失败达熔断阈值（cbFailureThreshold=5）
	for i := 0; i < 5; i++ {
		_, err := proto.Complete(ctx, cfg, &CompletionRequest{Model: "model-a", Messages: msg})
		require.Error(t, err)
	}

	// model-a 已熔断：下一次调用直接被 breaker 拒绝，不再发请求
	_, err := proto.Complete(ctx, cfg, &CompletionRequest{Model: "model-a", Messages: msg})
	require.Error(t, err)
	require.Contains(t, err.Error(), "circuit breaker open")

	// 同 provider 的 model-b 不受熔断影响，正常返回
	resp, err := proto.Complete(ctx, cfg, &CompletionRequest{Model: "model-b", Messages: msg})
	require.NoError(t, err)
	require.Equal(t, "hi", resp.Content)

	// health 同步：model-a degraded（连续失败达阈值）、model-b healthy
	require.Equal(t, ModelHealthDegraded, health.Get("model-a").Status)
	require.Equal(t, ModelHealthHealthy, health.Get("model-b").Status)
}

// TestPerModelEmbedding_IsolatesModels 验证嵌入模型同样按 model 维度隔离熔断：
// embedding model-a 失败不影响同 provider 的 embedding model-b。
func TestPerModelEmbedding_IsolatesModels(t *testing.T) {
	ts := newPerModelServer(t)
	health := NewHealthRegistry(nil)
	proto := NewOpenAICompatProtocol(NewOpenAICompatClient(
		ProviderConfig{Name: "test", BaseURL: ts.URL, APIKey: "k"},
		zap.NewNop(),
	)).WithHealth(health)
	cfg := ProviderConfig{Name: "test", BaseURL: ts.URL, APIKey: "k"}
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, err := proto.CreateEmbeddings(ctx, cfg, &EmbeddingRequest{Model: "model-a", Input: []string{"x"}})
		require.Error(t, err)
	}
	_, err := proto.CreateEmbeddings(ctx, cfg, &EmbeddingRequest{Model: "model-a", Input: []string{"x"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "circuit breaker open")

	resp, err := proto.CreateEmbeddings(ctx, cfg, &EmbeddingRequest{Model: "model-b", Input: []string{"x"}})
	require.NoError(t, err)
	require.Len(t, resp.Embeddings, 2)

	require.Equal(t, ModelHealthDegraded, health.Get("model-a").Status)
	require.Equal(t, ModelHealthHealthy, health.Get("model-b").Status)
}
