package llmgateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"go.uber.org/zap"
)

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
