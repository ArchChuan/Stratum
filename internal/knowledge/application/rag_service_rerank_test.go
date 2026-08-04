package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// fakeReranker records invocations and returns canned results.
type fakeReranker struct {
	calls   int
	lastReq knowledgeport.RerankRequest
	results []knowledgeport.RerankResult
	err     error
}

func (f *fakeReranker) Rerank(_ context.Context, req knowledgeport.RerankRequest) ([]knowledgeport.RerankResult, error) {
	f.calls++
	f.lastReq = req
	return f.results, f.err
}

// rerankMetrics records rerank metric calls alongside NoopMetrics.
type rerankMetrics struct {
	observability.NoopMetrics
	requests []string // tenant:model:status
}

func (m *rerankMetrics) IncRerankRequest(tenantID, model, status string) {
	m.requests = append(m.requests, tenantID+":"+model+":"+status)
}

// countingChunkRepo lets tests configure the chunk count used by
// handleMissingCollection's drift classification.
type countingChunkRepo struct {
	recordingChunkRepo
	count    int64
	countErr error
}

func (c *countingChunkRepo) CountByWorkspace(context.Context, string, string) (int64, error) {
	return c.count, c.countErr
}

func vectorRAGService(vectors *MockVectorStore) *RAGService {
	return NewRAGService(&mockEmbedder{dim: 3}, vectors, zap.NewNop())
}

func TestRAGQueryExternalRerankWidensRecallAndNarrows(t *testing.T) {
	vectors := NewMockVectorStore()
	results := make([]knowledgeport.VectorSearchResult, 0, 8)
	for i := 0; i < 8; i++ {
		results = append(results, knowledgeport.VectorSearchResult{
			ID: "chunk-" + string(rune('a'+i)), SourceDocument: "doc-" + string(rune('a'+i)),
			Content: "content " + string(rune('a'+i)), Score: float32(i + 1),
		})
	}
	vectors.SetSearchResults(results)
	reranker := &fakeReranker{results: []knowledgeport.RerankResult{
		{Index: 7, Score: 0.99}, {Index: 0, Score: 0.5},
	}}
	service := vectorRAGService(vectors)
	service.SetReranker(reranker)

	got, err := service.Query(context.Background(), RAGQueryRequest{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "query", Mode: "vector",
		TopK: 2, EmbeddingModel: "embedding-3", Reranking: "cohere:rerank-v3.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Recall was widened to TopK x RerankWidenFactor before the rerank call.
	if reranker.calls != 1 || len(reranker.lastReq.Documents) != 2*constants.RerankWidenFactor ||
		reranker.lastReq.TopN != 2 {
		t.Fatalf("reranker invocation=%+v want widened pool and TopN=2", reranker.lastReq)
	}
	// The final list is narrowed back to TopK in reranker order.
	if len(got.Sources) != 2 || got.Sources[0].ChunkID != "chunk-h" || got.Sources[1].ChunkID != "chunk-a" ||
		got.Sources[0].Score != 0.99 {
		t.Fatalf("sources=%+v", got.Sources)
	}
}

func TestRAGQueryExternalRerankAppliesThresholdAfterRescore(t *testing.T) {
	vectors := NewMockVectorStore()
	vectors.SetSearchResults([]knowledgeport.VectorSearchResult{
		{ID: "chunk-a", SourceDocument: "doc-a", Content: "a", Score: 0.1},
		{ID: "chunk-b", SourceDocument: "doc-b", Content: "b", Score: 0.2},
		{ID: "chunk-c", SourceDocument: "doc-c", Content: "c", Score: 0.3},
	})
	service := vectorRAGService(vectors)
	service.SetReranker(&fakeReranker{results: []knowledgeport.RerankResult{
		{Index: 2, Score: 0.9}, {Index: 0, Score: 0.1},
	}})

	got, err := service.Query(context.Background(), RAGQueryRequest{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "query", Mode: "vector",
		TopK: 2, EmbeddingModel: "embedding-3", Reranking: "cohere:rerank-v3.0",
		ScoreThreshold: 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Sources) != 1 || got.Sources[0].ChunkID != "chunk-c" {
		t.Fatalf("threshold must keep only the reranker-confirmed result: %+v", got.Sources)
	}
}

func TestRAGQueryBuiltinRerankStableScoreDesc(t *testing.T) {
	vectors := NewMockVectorStore()
	// L2 distances 0.9/0.1/0.5 -> similarities 0.526/0.909/0.667.
	vectors.SetSearchResults([]knowledgeport.VectorSearchResult{
		{ID: "chunk-a", SourceDocument: "doc-a", Content: "a", Score: 0.9},
		{ID: "chunk-b", SourceDocument: "doc-b", Content: "b", Score: 0.1},
		{ID: "chunk-c", SourceDocument: "doc-c", Content: "c", Score: 0.5},
	})
	service := vectorRAGService(vectors)

	got, err := service.Query(context.Background(), RAGQueryRequest{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "query", Mode: "vector",
		TopK: 3, EmbeddingModel: "embedding-3", Reranking: "builtin-score-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Sources) != 3 || got.Sources[0].ChunkID != "chunk-b" ||
		got.Sources[1].ChunkID != "chunk-c" || got.Sources[2].ChunkID != "chunk-a" {
		t.Fatalf("builtin rerank must order by normalized score desc: %+v", got.Sources)
	}
	if got.Sources[0].Score != l2ToSim(0.1) || got.Sources[2].Score != l2ToSim(0.9) {
		t.Fatalf("scores must be L2-normalized similarities: %+v", got.Sources)
	}
}

func TestRAGQueryExternalRerankSkipsTinyPoolWithMetric(t *testing.T) {
	vectors := NewMockVectorStore()
	vectors.SetSearchResults([]knowledgeport.VectorSearchResult{
		{ID: "chunk-a", SourceDocument: "doc-a", Content: "a", Score: 0.5},
	})
	reranker := &fakeReranker{}
	metrics := &rerankMetrics{}
	service := vectorRAGService(vectors)
	service.SetReranker(reranker)
	service.SetMetrics(metrics)

	got, err := service.Query(context.Background(), RAGQueryRequest{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "query", Mode: "vector",
		TopK: 1, EmbeddingModel: "embedding-3", Reranking: "cohere:rerank-v3.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reranker.calls != 0 {
		t.Fatal("tiny pool must skip the external rerank call")
	}
	if len(got.Sources) != 1 || got.Sources[0].ChunkID != "chunk-a" {
		t.Fatalf("skipped rerank must keep retrieval order: %+v", got.Sources)
	}
	if len(metrics.requests) != 1 || metrics.requests[0] != "tenant-1:rerank-v3.0:skipped" {
		t.Fatalf("metrics=%v", metrics.requests)
	}
}

func TestRAGQueryExternalRerankFailsClosedWithoutBackend(t *testing.T) {
	vectors := NewMockVectorStore()
	vectors.SetSearchResults([]knowledgeport.VectorSearchResult{
		{ID: "chunk-a", SourceDocument: "doc-a", Content: "a", Score: 0.5},
		{ID: "chunk-b", SourceDocument: "doc-b", Content: "b", Score: 0.5},
		{ID: "chunk-c", SourceDocument: "doc-c", Content: "c", Score: 0.5},
	})
	service := vectorRAGService(vectors)

	_, err := service.Query(context.Background(), RAGQueryRequest{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "query", Mode: "vector",
		TopK: 2, EmbeddingModel: "embedding-3", Reranking: "cohere:rerank-v3.0",
	})
	if err == nil || !strings.Contains(err.Error(), "no external reranker configured") {
		t.Fatalf("external identity without backend must fail closed, got %v", err)
	}
}

func TestRAGQueryKeywordExemptFromScoreThreshold(t *testing.T) {
	service := NewRAGService(nil, nil, zap.NewNop())
	service.SetChunkRepo(&recordingChunkRepo{chunks: []domain.Chunk{
		{ID: "chunk-a", DocID: "doc-a", Text: "a"},
		{ID: "chunk-b", DocID: "doc-b", Text: "b"},
	}})

	got, err := service.Query(context.Background(), RAGQueryRequest{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "query", Mode: "keyword",
		TopK: 5, ScoreThreshold: 0.9,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Sources) != 2 {
		t.Fatalf("keyword results carry no scores and must never be dropped by the threshold: %+v", got.Sources)
	}
}

func TestRAGQueryMissingCollectionClassifiesDrift(t *testing.T) {
	notFound := errors.New("collection not found: knowledge_tenant-1_workspace-1")
	for _, tc := range []struct {
		name      string
		count     int64
		wantErr   bool
		wantEmpty bool
	}{
		{name: "empty workspace returns empty result", count: 0, wantEmpty: true},
		{name: "chunk drift fails closed", count: 3, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vectors := NewMockVectorStore()
			vectors.SetSearchError(notFound)
			service := vectorRAGService(vectors)
			service.SetChunkRepo(&countingChunkRepo{count: tc.count})

			got, err := service.Query(context.Background(), RAGQueryRequest{
				TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "query", Mode: "vector",
				TopK: 3, EmbeddingModel: "embedding-3",
			})
			if tc.wantErr {
				if !errors.Is(err, ErrRAGDependency) {
					t.Fatalf("drift must fail closed, got %v", err)
				}
				return
			}
			if err != nil || !tc.wantEmpty || len(got.Sources) != 0 {
				t.Fatalf("empty workspace must yield empty result, got %+v err=%v", got, err)
			}
		})
	}
}

func TestRAGQueryDimensionMismatchFailsClosed(t *testing.T) {
	vectors := NewMockVectorStore()
	vectors.SetCollectionInfo(knowledgeport.CollectionInfo{Dim: 3, HasUserID: true})
	vectors.SetSearchResults([]knowledgeport.VectorSearchResult{
		{ID: "chunk-a", SourceDocument: "doc-a", Content: "a", Score: 0.5},
	})
	service := vectorRAGService(vectors)

	_, err := service.Query(context.Background(), RAGQueryRequest{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "query", Mode: "vector",
		TopK: 3, EmbeddingModel: "embedding-3", // vectorDim("embedding-3") = 2048 != 3
	})
	if !errors.Is(err, ErrRAGDependency) {
		t.Fatalf("dimension mismatch must fail closed, got %v", err)
	}
}

func TestRAGQueryMissingUserIDColumnTolerated(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	vectors := NewMockVectorStore()
	vectors.SetCollectionInfo(knowledgeport.CollectionInfo{Dim: 2048, HasUserID: false})
	vectors.SetSearchResults([]knowledgeport.VectorSearchResult{
		{ID: "chunk-a", SourceDocument: "doc-a", Content: "a", Score: 0.5},
	})
	service := NewRAGService(&mockEmbedder{dim: 3}, vectors, zap.New(core))

	got, err := service.Query(context.Background(), RAGQueryRequest{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "query", Mode: "vector",
		TopK: 3, EmbeddingModel: "embedding-3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Sources) != 1 {
		t.Fatalf("legacy collection without user_id column must still return results: %+v", got.Sources)
	}
	warned := false
	for _, entry := range logs.All() {
		if strings.Contains(entry.Message, "lacks user_id column") {
			warned = true
		}
	}
	if !warned {
		t.Fatal("missing user_id column must be logged as a warning")
	}
}

func TestRAGQueryHybridExternalRerankWidensBothLegs(t *testing.T) {
	vectors := NewMockVectorStore()
	vectorResults := make([]knowledgeport.VectorSearchResult, 0, 8)
	for i := 0; i < 8; i++ {
		vectorResults = append(vectorResults, knowledgeport.VectorSearchResult{
			ID: "chunk-" + string(rune('a'+i)), SourceDocument: "doc-" + string(rune('a'+i)),
			Content: "content", Score: float32(i + 1),
		})
	}
	vectors.SetSearchResults(vectorResults)
	reranker := &fakeReranker{results: []knowledgeport.RerankResult{{Index: 0, Score: 1.0}}}
	chunks := &recordingChunkRepo{}
	service := vectorRAGService(vectors)
	service.SetChunkRepo(chunks)
	service.SetReranker(reranker)

	got, err := service.Query(context.Background(), RAGQueryRequest{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "query", Mode: "hybrid",
		TopK: 2, EmbeddingModel: "embedding-3", Reranking: "cohere:rerank-v3.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if chunks.topK != 2*constants.RerankWidenFactor {
		t.Fatalf("keyword leg must widen to %d, got %d", 2*constants.RerankWidenFactor, chunks.topK)
	}
	if len(reranker.lastReq.Documents) != 8 {
		t.Fatalf("RRF pool must reflect the widened vector leg, got %d", len(reranker.lastReq.Documents))
	}
	if len(got.Sources) != 1 {
		t.Fatalf("hybrid rerank must narrow to TopN: %+v", got.Sources)
	}
}
