package infrastructure_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestZhipuModelCatalog 验证智谱 baseURL 命中返回全系目录，非智谱 baseURL
// 返回 nil（行为不变）；目录排除实测 400 的 glm-4.1v。
func TestZhipuModelCatalog(t *testing.T) {
	catalog := llmgateway.ZhipuModelCatalog("https://open.bigmodel.cn/api/paas/v4")
	require.NotEmpty(t, catalog)
	require.Contains(t, catalog, "glm-4.6v")
	require.Contains(t, catalog, "embedding-3")
	require.Contains(t, catalog, "glm-z1-air")
	require.NotContains(t, catalog, "glm-4.1v")

	require.Nil(t, llmgateway.ZhipuModelCatalog("https://api.example.com/v1"))
	require.Nil(t, llmgateway.ZhipuModelCatalog(""))
}

func TestZhipuClient_ModelsCoverStaticCatalog(t *testing.T) {
	client := llmgateway.NewZhipuClient("test-key", zap.NewNop())
	registered := make(map[string]struct{}, len(client.Models()))
	for _, model := range client.Models() {
		registered[model] = struct{}{}
	}

	for _, model := range (llmgateway.StaticModelCatalog{}).ListChatModels() {
		if !strings.HasPrefix(model, "glm-") {
			continue
		}
		if _, ok := registered[model]; !ok {
			t.Errorf("Zhipu client does not register catalog model %q", model)
		}
	}
}

func TestZhipuClient_Complete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{ //nolint:gosec
			"choices": []map[string]any{
				{"message": map[string]string{"content": "world"}},
			},
			"model": "glm-4-flash",
			"usage": map[string]int{"prompt_tokens": 3, "completion_tokens": 1, "total_tokens": 4},
		})
	}))
	defer srv.Close()

	client := llmgateway.NewZhipuClientWithBase("test-key", srv.URL, zap.NewNop())
	resp, err := client.Complete(context.Background(), &llmgateway.CompletionRequest{
		Model:    "glm-4-flash",
		Messages: []llmgateway.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "world" {
		t.Errorf("want 'world', got %q", resp.Content)
	}
}

func TestZhipuClient_CreateEmbeddings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{ //nolint:gosec
			"data": []map[string]any{
				{"embedding": []float32{0.4, 0.5, 0.6}},
			},
		})
	}))
	defer srv.Close()

	client := llmgateway.NewZhipuClientWithBase("test-key", srv.URL, zap.NewNop())
	resp, err := client.CreateEmbeddings(context.Background(), &llmgateway.EmbeddingRequest{
		Model: "embedding-3",
		Input: []string{"hello"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Embeddings) != 1 || len(resp.Embeddings[0]) != 3 {
		t.Errorf("unexpected embeddings: %v", resp.Embeddings)
	}
}

func TestZhipuClient_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden"}`)) //nolint:gosec
	}))
	defer srv.Close()

	client := llmgateway.NewZhipuClientWithBase("bad-key", srv.URL, zap.NewNop())
	_, err := client.Complete(context.Background(), &llmgateway.CompletionRequest{
		Model:    "glm-4-flash",
		Messages: []llmgateway.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Error("expected error, got nil")
	}
}
