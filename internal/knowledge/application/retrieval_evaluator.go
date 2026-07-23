package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

var ErrRetrievalDependency = ErrRAGDependency

const (
	MinimumEvaluationTopK = 1
	MaximumEvaluationTopK = 100

	RerankingNone      = "none"
	RerankingScoreDesc = "score_desc"

	QueryRewriteNone          = "none"
	QueryRewriteLowercaseTrim = "lowercase_trim"
)

type EvaluationRetriever interface {
	Query(context.Context, RAGQueryRequest) (*RAGQueryResult, error)
}

type RetrievalSnapshot struct {
	WorkspaceID    string
	WorkspaceName  string
	EmbeddingModel string
	QueryMode      string
	TopK           int
	ScoreThreshold float64
	Reranking      string
	QueryRewrite   string
}

func (s RetrievalSnapshot) Validate() error {
	if strings.TrimSpace(s.WorkspaceID) == "" || strings.TrimSpace(s.WorkspaceName) == "" {
		return errors.New("knowledge retrieval snapshot: workspace identity required")
	}
	if strings.TrimSpace(s.EmbeddingModel) == "" || strings.TrimSpace(s.QueryMode) == "" {
		return errors.New("knowledge retrieval snapshot: embedding and query mode required")
	}
	if s.TopK < MinimumEvaluationTopK || s.TopK > MaximumEvaluationTopK {
		return fmt.Errorf("knowledge retrieval snapshot: top_k must be between %d and %d",
			MinimumEvaluationTopK, MaximumEvaluationTopK)
	}
	if s.ScoreThreshold < 0 || s.ScoreThreshold > 1 {
		return errors.New("knowledge retrieval snapshot: score_threshold must be between 0 and 1")
	}
	if s.Reranking != RerankingNone && s.Reranking != RerankingScoreDesc {
		return fmt.Errorf("knowledge retrieval snapshot: unsupported reranking %q", s.Reranking)
	}
	if s.QueryRewrite != QueryRewriteNone && s.QueryRewrite != QueryRewriteLowercaseTrim {
		return fmt.Errorf("knowledge retrieval snapshot: unsupported query rewrite %q", s.QueryRewrite)
	}
	return nil
}

type RetrievalCase struct {
	Query               string
	RelevantDocumentIDs []string
	CitationDocumentIDs []string
	ExpectNoAnswer      bool
}

type RetrievalEvaluation struct {
	Relevant             bool     `json:"relevant"`
	CitationCorrect      bool     `json:"citation_correct"`
	NoAnswer             bool     `json:"no_answer"`
	RetrievedCount       int      `json:"retrieved_count"`
	RetrievedDocumentIDs []string `json:"retrieved_document_ids,omitempty"`
}

type RetrievalEvaluator struct {
	retriever EvaluationRetriever
}

func NewRetrievalEvaluator(retriever EvaluationRetriever) *RetrievalEvaluator {
	return &RetrievalEvaluator{retriever: retriever}
}

func (e *RetrievalEvaluator) EvaluateRetrieval(
	ctx context.Context, snapshot RetrievalSnapshot, testCase RetrievalCase,
) (RetrievalEvaluation, error) {
	if e == nil || e.retriever == nil {
		return RetrievalEvaluation{}, errors.New("knowledge retrieval evaluator unavailable")
	}
	if err := snapshot.Validate(); err != nil {
		return RetrievalEvaluation{}, err
	}
	query := rewriteEvaluationQuery(testCase.Query, snapshot.QueryRewrite)
	if query == "" {
		return RetrievalEvaluation{}, errors.New("knowledge retrieval evaluator: query required")
	}
	result, err := e.retriever.Query(ctx, RAGQueryRequest{
		Question: query, Workspace: snapshot.WorkspaceName, WorkspaceID: snapshot.WorkspaceID,
		TenantID: tenantIDFromContext(ctx), Mode: snapshot.QueryMode, TopK: snapshot.TopK,
		EmbeddingModel: snapshot.EmbeddingModel,
	})
	if err != nil {
		return RetrievalEvaluation{}, ErrRetrievalDependency
	}
	if result == nil {
		return RetrievalEvaluation{}, errors.New("knowledge retrieval evaluator: empty retrieval result")
	}
	sources := append([]Source(nil), result.Sources...)
	if snapshot.Reranking == RerankingScoreDesc {
		sort.SliceStable(sources, func(i, j int) bool { return sources[i].Score > sources[j].Score })
	}
	documentIDs := make([]string, 0, min(snapshot.TopK, len(sources)))
	for _, source := range sources {
		if float64(source.Score) < snapshot.ScoreThreshold {
			continue
		}
		documentIDs = append(documentIDs, source.DocumentID)
		if len(documentIDs) == snapshot.TopK {
			break
		}
	}
	noAnswer := len(documentIDs) == 0
	relevant := containsExpectedID(documentIDs, testCase.RelevantDocumentIDs)
	if len(testCase.RelevantDocumentIDs) == 0 {
		relevant = !testCase.ExpectNoAnswer || noAnswer
	}
	citationCorrect := containsAllIDs(documentIDs, testCase.CitationDocumentIDs)
	return RetrievalEvaluation{Relevant: relevant, CitationCorrect: citationCorrect,
		NoAnswer: noAnswer, RetrievedCount: len(documentIDs), RetrievedDocumentIDs: documentIDs}, nil
}

func rewriteEvaluationQuery(query, mode string) string {
	switch mode {
	case QueryRewriteLowercaseTrim:
		return strings.ToLower(strings.TrimSpace(query))
	default:
		return strings.TrimSpace(query)
	}
}

func containsExpectedID(actual, expected []string) bool {
	if len(expected) == 0 {
		return true
	}
	actualSet := make(map[string]struct{}, len(actual))
	for _, id := range actual {
		actualSet[id] = struct{}{}
	}
	for _, id := range expected {
		if _, ok := actualSet[id]; ok {
			return true
		}
	}
	return false
}

func containsAllIDs(actual, expected []string) bool {
	if len(expected) == 0 {
		return true
	}
	actualSet := make(map[string]struct{}, len(actual))
	for _, id := range actual {
		actualSet[id] = struct{}{}
	}
	for _, id := range expected {
		if _, ok := actualSet[id]; !ok {
			return false
		}
	}
	return true
}

// tenantIDFromContext is intentionally resolved by the wiring adapter through
// the existing PostgreSQL tenant context. The evaluator never accepts a second,
// potentially conflicting tenant value from an evaluation case.
func tenantIDFromContext(ctx context.Context) string {
	return reqctx.TenantIDFromContext(ctx)
}
