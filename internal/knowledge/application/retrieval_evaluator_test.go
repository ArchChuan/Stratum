package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

func TestEvaluateRetrievalAppliesExactRevisionConfigWithoutLeakingContent(t *testing.T) {
	// The fake retriever stands in for RAGService.Query, which applies rerank,
	// threshold, and narrowing; it returns the strategy-applied result.
	retriever := &fakeEvaluationRetriever{result: &RAGQueryResult{Sources: []Source{
		{DocumentID: "doc-high", Content: "private high content", Score: 0.95},
	}}}
	evaluator := NewRetrievalEvaluator(retriever)
	result, err := evaluator.EvaluateRetrieval(reqctx.WithTenantID(context.Background(), "tenant-1"), RetrievalSnapshot{
		WorkspaceID: "workspace-1", WorkspaceName: "support", EmbeddingModel: "embedding-3",
		QueryMode: "vector", TopK: 1, ScoreThreshold: 0.80, Reranking: RerankIdentityBuiltin,
		QueryRewrite: "lowercase_trim",
	}, RetrievalCase{Query: "  REFUND POLICY  ", RelevantDocumentIDs: []string{"doc-high"},
		CitationDocumentIDs: []string{"doc-high"}})
	if err != nil {
		t.Fatal(err)
	}
	if retriever.request.Question != "refund policy" || retriever.request.TenantID != "tenant-1" ||
		retriever.request.WorkspaceID != "workspace-1" ||
		retriever.request.EmbeddingModel != "embedding-3" || retriever.request.TopK != 1 ||
		retriever.request.Reranking != RerankIdentityBuiltin || retriever.request.ScoreThreshold != 0.80 ||
		retriever.request.RerankTopK != 1 {
		t.Fatalf("exact snapshot was not delegated: %+v", retriever.request)
	}
	if !result.Relevant || !result.CitationCorrect || result.NoAnswer || result.RetrievedCount != 1 {
		t.Fatalf("unexpected evaluation: %+v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if result.RetrievedDocumentIDs[0] != "doc-high" || strings.Contains(string(encoded), "private") {
		t.Fatalf("unsafe result: %+v", result)
	}
}

func TestRetrieveContextDelegatesSnapshotToRetriever(t *testing.T) {
	retriever := &fakeEvaluationRetriever{result: &RAGQueryResult{Sources: []Source{
		{DocumentID: "first", Content: "first", Score: 0.9},
		{DocumentID: "second", Content: "second", Score: 0.8},
	}}}
	snapshot := RetrievalSnapshot{
		WorkspaceID: "workspace-1", WorkspaceName: "support", EmbeddingModel: "embedding-3",
		QueryMode: "hybrid", TopK: 2, ScoreThreshold: 0.7,
		Reranking: RerankIdentityBuiltin, QueryRewrite: QueryRewriteLowercaseTrim,
	}

	content, err := NewRetrievalEvaluator(retriever).RetrieveContext(
		reqctx.WithTenantID(context.Background(), "tenant-1"), snapshot, "  QUERY  ", "viewer-1",
	)

	if err != nil || content != "first\n---\nsecond\n---\n" || retriever.request.Question != "query" ||
		retriever.request.TopK != 2 || retriever.request.Mode != "hybrid" ||
		retriever.request.Reranking != RerankIdentityBuiltin || retriever.request.ScoreThreshold != 0.7 ||
		retriever.request.RerankTopK != 2 {
		t.Fatalf("content=%q request=%+v err=%v", content, retriever.request, err)
	}
}

func TestEvaluateRetrievalCoversNoAnswerAndDependencyFailure(t *testing.T) {
	// The fake retriever applies the threshold (0.9) and returns nothing, so
	// the evaluator observes an empty result set -> no_answer.
	evaluator := NewRetrievalEvaluator(&fakeEvaluationRetriever{result: &RAGQueryResult{Sources: nil}})
	result, err := evaluator.EvaluateRetrieval(context.Background(), RetrievalSnapshot{
		WorkspaceID: "workspace-1", WorkspaceName: "support", EmbeddingModel: "embedding-3",
		QueryMode: "vector", TopK: 5, ScoreThreshold: 0.9, Reranking: RerankingNone, QueryRewrite: "none",
	}, RetrievalCase{Query: "unknown", ExpectNoAnswer: true})
	if err != nil || !result.NoAnswer || !result.Relevant || !result.CitationCorrect {
		t.Fatalf("result=%+v err=%v", result, err)
	}

	wantErr := errors.New("milvus unavailable")
	_, err = NewRetrievalEvaluator(&fakeEvaluationRetriever{err: wantErr}).EvaluateRetrieval(
		context.Background(), RetrievalSnapshot{WorkspaceID: "workspace-1", WorkspaceName: "support",
			EmbeddingModel: "embedding-3", QueryMode: "vector", TopK: 5, Reranking: RerankingNone, QueryRewrite: "none"},
		RetrievalCase{Query: "query"})
	if !errors.Is(err, ErrRetrievalDependency) || errors.Is(err, wantErr) {
		t.Fatalf("dependency failure must be safely classified, got %v", err)
	}
}

func TestEvaluateRetrievalDedupesChunkLevelDocumentIDs(t *testing.T) {
	// Chunk-level sources repeat one document across ranks; the evaluation must
	// expose distinct document IDs so document-level metrics stay at most 1.
	retriever := &fakeEvaluationRetriever{result: &RAGQueryResult{Sources: []Source{
		{DocumentID: "doc-a", Content: "chunk 1", Score: 0.95},
		{DocumentID: "doc-a", Content: "chunk 2", Score: 0.94},
		{DocumentID: "doc-b", Content: "chunk 3", Score: 0.93},
		{DocumentID: "doc-a", Content: "chunk 4", Score: 0.92},
	}}}
	result, err := NewRetrievalEvaluator(retriever).EvaluateRetrieval(
		context.Background(), RetrievalSnapshot{
			WorkspaceID: "workspace-1", WorkspaceName: "support", EmbeddingModel: "embedding-3",
			QueryMode: "hybrid", TopK: 5, Reranking: RerankingNone, QueryRewrite: "none",
		}, RetrievalCase{Query: "query", RelevantDocumentIDs: []string{"doc-a", "doc-b"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(result.RetrievedDocumentIDs, ","); got != "doc-a,doc-b" {
		t.Fatalf("dedup failed, got %q", got)
	}
	if result.RetrievedCount != 2 {
		t.Fatalf("RetrievedCount = %d, want 2", result.RetrievedCount)
	}
	if got := RecallAtK(result.RetrievedDocumentIDs, []string{"doc-a", "doc-b"}, 5); got != 1 {
		t.Fatalf("Recall@5 = %v, want 1", got)
	}
	if got := NDCGAtK(result.RetrievedDocumentIDs, []string{"doc-a", "doc-b"}, 5); got != 1 {
		t.Fatalf("NDCG@5 = %v, want 1", got)
	}
}

func TestEvaluateRetrievalSanitizesSensitiveDependencyFailure(t *testing.T) {
	sensitive := errors.New("POST https://user:password@example.test/search?api_key=secret-token: " +
		"response body contains private document content")
	_, err := NewRetrievalEvaluator(&fakeEvaluationRetriever{err: sensitive}).EvaluateRetrieval(
		reqctx.WithTenantID(context.Background(), "tenant-1"), RetrievalSnapshot{
			WorkspaceID: "workspace-1", WorkspaceName: "support", EmbeddingModel: "embedding-3",
			QueryMode: "vector", TopK: 5, Reranking: RerankingNone, QueryRewrite: "none",
		}, RetrievalCase{Query: "query"})
	if !errors.Is(err, ErrRetrievalDependency) || errors.Is(err, sensitive) {
		t.Fatalf("dependency error classification/cause exposure mismatch: %v", err)
	}
	message := err.Error()
	for _, leaked := range []string{"example.test", "password", "api_key", "secret-token", "private document", "response body"} {
		if strings.Contains(message, leaked) {
			t.Fatalf("dependency error leaked %q: %s", leaked, message)
		}
	}
}

func TestRetrievalSnapshotValidationRejectsUnsupportedRuntime(t *testing.T) {
	base := RetrievalSnapshot{WorkspaceID: "workspace-1", WorkspaceName: "support", EmbeddingModel: "embedding-3",
		QueryMode: domain.DefaultQueryMode, TopK: 5, Reranking: RerankingNone, QueryRewrite: "none"}
	for _, mutate := range []func(*RetrievalSnapshot){
		func(s *RetrievalSnapshot) { s.TopK = 0 },
		func(s *RetrievalSnapshot) { s.TopK = 101 },
		func(s *RetrievalSnapshot) { s.ScoreThreshold = -0.1 },
		func(s *RetrievalSnapshot) { s.ScoreThreshold = 1.1 },
		func(s *RetrievalSnapshot) { s.Reranking = "external-provider" },
		func(s *RetrievalSnapshot) { s.Reranking = "cohere" },
		func(s *RetrievalSnapshot) { s.Reranking = "none" },
		func(s *RetrievalSnapshot) { s.Reranking = "score_desc" },
		func(s *RetrievalSnapshot) { s.QueryRewrite = "llm" },
	} {
		snapshot := base
		mutate(&snapshot)
		if err := snapshot.Validate(); err == nil {
			t.Fatalf("accepted invalid snapshot: %+v", snapshot)
		}
	}
}

type fakeEvaluationRetriever struct {
	request RAGQueryRequest
	result  *RAGQueryResult
	err     error
}

func (f *fakeEvaluationRetriever) Query(_ context.Context, request RAGQueryRequest) (*RAGQueryResult, error) {
	f.request = request
	return f.result, f.err
}
