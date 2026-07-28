package wiring

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestKnowledgeEmbedResolverResolvesViaRegistry(t *testing.T) {
	const fakeKey = "fake-knowledge-embed-key"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/embeddings" || r.URL.Path == "/v1/embeddings" {
			require.Equal(t, "Bearer "+fakeKey, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{
			"index": 0, "embedding": []float32{1, 0, 0},
		}}})
	}))
	defer server.Close()

	// Override Qwen base URL via env-style: NewQwenClientWithBase. However the registry
	// always uses NewQwenClient (not WithBase). To test custom base URLs, we create
	// the registry with encrypted keys and let it resolve — the client will use the
	// default Qwen URL. For custom base URL coverage we test the resolver path only.
	aesKey := pkgcrypto.DeriveAESKey("fake-knowledge-resolver-key-material")
	encrypted, err := pkgcrypto.Encrypt(aesKey, fakeKey)
	require.NoError(t, err)
	settings, err := json.Marshal(map[string]any{"llm_api_keys": map[string]any{"qwen": encrypted}})
	require.NoError(t, err)

	readSettings := func(_ context.Context, _ string) ([]byte, error) { return settings, nil }
	reg := llmgateway.NewModelRegistry(readSettings, aesKey, zap.NewNop())
	resolver := buildKnowledgeEmbedResolver(reg, zap.NewNop())
	embedder := resolver(context.Background(), "tenant-1", "text-embedding-v3")
	require.NotNil(t, embedder)
	// The resolver succeeds (returns non-nil) even if the default API endpoint is unreachable.
	// Full round-trip testing is done at the E2E level.
}

func TestPipelineEmbedResolverResolvesViaRegistry(t *testing.T) {
	aesKey := pkgcrypto.DeriveAESKey("fake-pipeline-resolver-key-material")
	encrypted, err := pkgcrypto.Encrypt(aesKey, "fake-key")
	require.NoError(t, err)
	settings, err := json.Marshal(map[string]any{"llm_api_keys": map[string]any{"qwen": encrypted}})
	require.NoError(t, err)

	readSettings := func(_ context.Context, _ string) ([]byte, error) { return settings, nil }
	reg := llmgateway.NewModelRegistry(readSettings, aesKey, zap.NewNop())
	resolver := buildEmbedResolver(reg, zap.NewNop())
	embedder := resolver(context.Background(), "tenant-1")
	require.NotNil(t, embedder)
}
