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
	require.Equal(t, []string{"llama3.2:latest", "mistral:7b"}, models)
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
