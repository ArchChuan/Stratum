package wiring

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestKnowledgeEmbedResolverUsesConfiguredQwenBaseURL(t *testing.T) {
	const fakeKey = "fake-knowledge-embed-key"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "/embeddings", r.URL.Path)
		require.Equal(t, "Bearer "+fakeKey, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{
			"index": 0, "embedding": []float32{1, 0, 0},
		}}})
	}))
	defer server.Close()

	aesKey := pkgcrypto.DeriveAESKey("fake-knowledge-resolver-key-material")
	encrypted, err := pkgcrypto.Encrypt(aesKey, fakeKey)
	require.NoError(t, err)
	settings, err := json.Marshal(map[string]any{"llm_api_keys": map[string]any{"qwen": encrypted}})
	require.NoError(t, err)
	db := tenantSettingsQueryFunc(func(context.Context, string, ...any) pgx.Row {
		return tenantSettingsRow{settings: settings}
	})
	resolver := buildKnowledgeEmbedResolver(db, llmgateway.NewTenantGatewayCache(), aesKey,
		server.URL, "", zap.NewNop())
	embedder := resolver(context.Background(), "tenant-1", "text-embedding-v3")
	require.NotNil(t, embedder)
	vector, err := embedder.EmbedVector(context.Background(), "bounded knowledge")
	require.NoError(t, err)
	require.Equal(t, []float32{1, 0, 0}, vector)
	require.Equal(t, 1, requests)
}

func TestKnowledgeEmbedResolverKeepsConfiguredBaseURLAfterPipelineCacheLoad(t *testing.T) {
	const fakeKey = "fake-shared-cache-key"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "/embeddings", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{
			"index": 0, "embedding": []float32{0, 1, 0},
		}}})
	}))
	defer server.Close()

	aesKey := pkgcrypto.DeriveAESKey("fake-shared-cache-key-material")
	encrypted, err := pkgcrypto.Encrypt(aesKey, fakeKey)
	require.NoError(t, err)
	settings, err := json.Marshal(map[string]any{
		"embed_model":  "text-embedding-v3",
		"llm_api_keys": map[string]any{"qwen": encrypted},
	})
	require.NoError(t, err)
	db := tenantSettingsQueryFunc(func(context.Context, string, ...any) pgx.Row {
		return tenantSettingsRow{settings: settings}
	})
	cache := llmgateway.NewTenantGatewayCache()
	pipelineResolver := buildEmbedResolver(db, cache, aesKey, server.URL, "", zap.NewNop())
	pipelineEmbedder := pipelineResolver(context.Background(), "tenant-1")
	require.NotNil(t, pipelineEmbedder)
	_, err = pipelineEmbedder.EmbedVector(context.Background(), "pipeline cache load")
	require.NoError(t, err)

	knowledgeResolver := buildKnowledgeEmbedResolver(db, cache, aesKey, server.URL, "", zap.NewNop())
	knowledgeEmbedder := knowledgeResolver(context.Background(), "tenant-1", "text-embedding-v3")
	require.NotNil(t, knowledgeEmbedder)
	_, err = knowledgeEmbedder.EmbedVector(context.Background(), "knowledge cache reuse")
	require.NoError(t, err)
	require.Equal(t, 2, requests)
}
