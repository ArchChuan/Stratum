package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

func TestRAGServiceSetEmbedResolver(t *testing.T) {
	rs := NewRAGService(&mockEmbedder{dim: 4}, NewMockVectorStore(), zap.NewNop())
	if rs.SetEmbedResolver(nil); rs.embedResolver != nil {
		t.Fatal("nil resolver must be stored as-is")
	}
	resolver := func(context.Context, string, string) knowledgeport.Embedder { return nil }
	rs.SetEmbedResolver(resolver)
	if rs.embedResolver == nil {
		t.Fatal("resolver must be injected")
	}
}

func TestResolveEmbedderPrecedence(t *testing.T) {
	fallback := &mockEmbedder{dim: 4}
	rs := NewRAGService(fallback, NewMockVectorStore(), zap.NewNop())

	// 无 resolver → fallback。
	if got := rs.resolveEmbedder(context.Background(), RAGQueryRequest{TenantID: "t1"}); got != fallback {
		t.Fatal("must fall back to embedding service")
	}
	// 极端情况：resolver 返回 nil → 仍 fallback。
	rs.SetEmbedResolver(func(context.Context, string, string) knowledgeport.Embedder { return nil })
	if got := rs.resolveEmbedder(context.Background(), RAGQueryRequest{TenantID: "t1"}); got != fallback {
		t.Fatal("nil resolver result must fall back")
	}
	// resolver 生效。
	resolved := &mockEmbedder{dim: 8}
	rs.SetEmbedResolver(func(_ context.Context, _, _ string) knowledgeport.Embedder { return resolved })
	if got := rs.resolveEmbedder(context.Background(), RAGQueryRequest{TenantID: "t1"}); got != resolved {
		t.Fatal("resolver must win")
	}
	// 极端情况：无 tenantID → 不走 resolver。
	rs2 := NewRAGService(fallback, NewMockVectorStore(), zap.NewNop())
	rs2.SetEmbedResolver(func(_ context.Context, _, _ string) knowledgeport.Embedder { return resolved })
	if got := rs2.resolveEmbedder(context.Background(), RAGQueryRequest{}); got != fallback {
		t.Fatal("empty tenant must skip resolver")
	}
}

func TestRetrieveRelevantChunks(t *testing.T) {
	store := NewMockVectorStore()
	store.SetSearchResults([]knowledgeport.VectorSearchResult{
		{Content: "chunk-a"}, {Content: "chunk-b"},
	})
	rs := NewRAGService(&mockEmbedder{dim: 4}, store, zap.NewNop())

	chunks, err := rs.RetrieveRelevantChunks(context.Background(), "t1", "q", "ws1", "text-embedding-v3", 3, "viewer-1")
	if err != nil || len(chunks) != 2 || chunks[0] != "chunk-a" {
		t.Fatalf("chunks = %+v, %v", chunks, err)
	}
	// 极端情况：空 tenant → 明确错误。
	if _, err := rs.RetrieveRelevantChunks(context.Background(), "", "q", "ws1", "text-embedding-v3", 3, "viewer-1"); err == nil {
		t.Fatal("empty tenant must error")
	}
	// 极端情况：EmbedVector 失败 → ErrRAGDependency。
	rsBad := NewRAGService(&mockEmbedder{err: errors.New("embed down")}, store, zap.NewNop())
	if _, err := rsBad.RetrieveRelevantChunks(context.Background(), "t1", "q", "ws1", "text-embedding-v3", 3, "viewer-1"); !errors.Is(err, ErrRAGDependency) {
		t.Fatalf("embed err = %v", err)
	}
	// 极端情况：Search 失败 → ErrRAGDependency。
	store.SetSearchError(errors.New("milvus down"))
	if _, err := rs.RetrieveRelevantChunks(context.Background(), "t1", "q", "ws1", "text-embedding-v3", 3, "viewer-1"); !errors.Is(err, ErrRAGDependency) {
		t.Fatalf("search err = %v", err)
	}
	// 极端情况：collection 不存在 → 降级为空结果而非错误。
	store.SetSearchError(errors.New("collection not found: x"))
	chunks, err = rs.RetrieveRelevantChunks(context.Background(), "t1", "q", "ws1", "text-embedding-v3", 3, "viewer-1")
	if err != nil || len(chunks) != 0 {
		t.Fatalf("missing collection = %+v, %v", chunks, err)
	}
	// 极端情况：embedder 未配置 → 明确错误。
	rsNil := NewRAGService(nil, store, zap.NewNop())
	if _, err := rsNil.RetrieveRelevantChunks(context.Background(), "t1", "q", "ws1", "text-embedding-v3", 3, "viewer-1"); err == nil {
		t.Fatal("nil embedder must error")
	}
}

func TestRAGQueryDefaultsAndCollectionName(t *testing.T) {
	// 极端情况：TopK <= 0 默认 5；WorkspaceID 空时 collection 用 Workspace。
	rs := NewRAGService(&mockEmbedder{dim: 4}, NewMockVectorStore(), zap.NewNop())
	res, err := rs.Query(context.Background(), RAGQueryRequest{Question: "q", Mode: "vector", ViewerID: "test-user"})
	if err != nil || res.Mode != "vector" || len(res.Sources) != 0 {
		t.Fatalf("query = %+v, %v", res, err)
	}
	if got := constants.CollectionName("t1", "ws1", "text-embedding-v3"); !strings.Contains(got, "ws1") {
		t.Fatalf("collection = %q", got)
	}
}
