package infrastructure_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"go.uber.org/zap"
)

func TestQwenClient_Complete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing auth header")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{ //nolint:gosec
			"choices": []map[string]any{
				{"message": map[string]string{"content": "hello"}},
			},
			"model": "qwen-turbo",
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 1, "total_tokens": 6},
		})
	}))
	defer srv.Close()

	client := llmgateway.NewQwenClientWithBase("test-key", srv.URL, zap.NewNop())
	resp, err := client.Complete(context.Background(), &llmgateway.CompletionRequest{
		Model:    "qwen-turbo",
		Messages: []llmgateway.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "hello" {
		t.Errorf("want 'hello', got %q", resp.Content)
	}
}

func TestQwenClient_CreateEmbeddings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{ //nolint:gosec
			"data": []map[string]any{
				{"embedding": []float32{0.1, 0.2, 0.3}},
			},
		})
	}))
	defer srv.Close()

	client := llmgateway.NewQwenClientWithBase("test-key", srv.URL, zap.NewNop())
	resp, err := client.CreateEmbeddings(context.Background(), &llmgateway.EmbeddingRequest{
		Model: "text-embedding-v3",
		Input: []string{"hello"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Embeddings) != 1 || len(resp.Embeddings[0]) != 3 {
		t.Errorf("unexpected embeddings: %v", resp.Embeddings)
	}
}

func TestQwenClient_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`)) //nolint:gosec
	}))
	defer srv.Close()

	client := llmgateway.NewQwenClientWithBase("bad-key", srv.URL, zap.NewNop())
	_, err := client.Complete(context.Background(), &llmgateway.CompletionRequest{
		Model:    "qwen-turbo",
		Messages: []llmgateway.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestQwenClient_ErrorStatusExcludesUpstreamBody(t *testing.T) {
	const upstreamMarker = "raw-provider-secret-marker"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(upstreamMarker))
	}))
	defer srv.Close()

	client := llmgateway.NewQwenClientWithBase("bad-key", srv.URL, zap.NewNop())
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "complete", run: func() error {
			_, err := client.Complete(context.Background(), &llmgateway.CompletionRequest{Model: "qwen-turbo"})
			return err
		}},
		{name: "stream", run: func() error {
			_, err := client.CompleteStream(context.Background(), &llmgateway.CompletionRequest{Model: "qwen-turbo"}, func(string) {})
			return err
		}},
		{name: "embedding", run: func() error {
			_, err := client.CreateEmbeddings(context.Background(), &llmgateway.EmbeddingRequest{
				Model: "text-embedding-v3", Input: []string{"hello"},
			})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil {
				t.Fatal("expected upstream status error")
			}
			if strings.Contains(err.Error(), upstreamMarker) {
				t.Fatalf("error exposed upstream response body: %q", err)
			}
			if !strings.Contains(err.Error(), "qwen") || !strings.Contains(err.Error(), "401") {
				t.Fatalf("error omitted provider or status context: %q", err)
			}
		})
	}
}
