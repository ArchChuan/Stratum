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
	retriever := &fakeEvaluationRetriever{result: &RAGQueryResult{Sources: []Source{
		{DocumentID: "doc-low", Content: "private low content", Score: 0.40},
		{DocumentID: "doc-high", Content: "private high content", Score: 0.95},
	}}}
	evaluator := NewRetrievalEvaluator(retriever)
	result, err := evaluator.EvaluateRetrieval(reqctx.WithTenantID(context.Background(), "tenant-1"), RetrievalSnapshot{
		WorkspaceID: "workspace-1", WorkspaceName: "support", EmbeddingModel: "embedding-3",
		QueryMode: "vector", TopK: 1, ScoreThreshold: 0.80, Reranking: "score_desc",
		QueryRewrite: "lowercase_trim",
	}, RetrievalCase{Query: "  REFUND POLICY  ", RelevantDocumentIDs: []string{"doc-high"},
		CitationDocumentIDs: []string{"doc-high"}})
	if err != nil {
		t.Fatal(err)
	}
	if retriever.request.Question != "refund policy" || retriever.request.TenantID != "tenant-1" ||
		retriever.request.WorkspaceID != "workspace-1" ||
		retriever.request.EmbeddingModel != "embedding-3" || retriever.request.TopK != 1 {
		t.Fatalf("exact snapshot was not executed: %+v", retriever.request)
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

func TestEvaluateRetrievalCoversNoAnswerAndDependencyFailure(t *testing.T) {
	evaluator := NewRetrievalEvaluator(&fakeEvaluationRetriever{result: &RAGQueryResult{Sources: []Source{
		{DocumentID: "doc-low", Content: "private", Score: 0.2},
	}}})
	result, err := evaluator.EvaluateRetrieval(context.Background(), RetrievalSnapshot{
		WorkspaceID: "workspace-1", WorkspaceName: "support", EmbeddingModel: "embedding-3",
		QueryMode: "vector", TopK: 5, ScoreThreshold: 0.9, Reranking: "none", QueryRewrite: "none",
	}, RetrievalCase{Query: "unknown", ExpectNoAnswer: true})
	if err != nil || !result.NoAnswer || !result.Relevant || !result.CitationCorrect {
		t.Fatalf("result=%+v err=%v", result, err)
	}

	wantErr := errors.New("milvus unavailable")
	_, err = NewRetrievalEvaluator(&fakeEvaluationRetriever{err: wantErr}).EvaluateRetrieval(
		context.Background(), RetrievalSnapshot{WorkspaceID: "workspace-1", WorkspaceName: "support",
			EmbeddingModel: "embedding-3", QueryMode: "vector", TopK: 5, Reranking: "none", QueryRewrite: "none"},
		RetrievalCase{Query: "query"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("dependency failure must propagate, got %v", err)
	}
}

func TestRetrievalSnapshotValidationRejectsUnsupportedRuntime(t *testing.T) {
	base := RetrievalSnapshot{WorkspaceID: "workspace-1", WorkspaceName: "support", EmbeddingModel: "embedding-3",
		QueryMode: domain.DefaultQueryMode, TopK: 5, Reranking: "none", QueryRewrite: "none"}
	for _, mutate := range []func(*RetrievalSnapshot){
		func(s *RetrievalSnapshot) { s.TopK = 0 },
		func(s *RetrievalSnapshot) { s.TopK = 101 },
		func(s *RetrievalSnapshot) { s.ScoreThreshold = -0.1 },
		func(s *RetrievalSnapshot) { s.ScoreThreshold = 1.1 },
		func(s *RetrievalSnapshot) { s.Reranking = "external-provider" },
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
