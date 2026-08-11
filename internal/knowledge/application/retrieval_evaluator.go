package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

var ErrRetrievalDependency = ErrRAGDependency

const (
	MinimumEvaluationTopK = 1
	MaximumEvaluationTopK = 100

	// RerankingNone is the identity form for "no reranking" (""). Snapshots
	// written today persist identity values; the legacy values below were
	// persisted by earlier revisions and are mapped on read.
	RerankingNone         = ""
	RerankingScoreDesc    = "score_desc" // legacy snapshot value, mapped to RerankIdentityBuiltin
	RerankIdentityBuiltin = "builtin-score-v1"

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
	if err := validateScoreThreshold(s.ScoreThreshold); err != nil {
		return err
	}
	if err := validateRerankIdentity(s.Reranking); err != nil {
		return err
	}
	if s.QueryRewrite != QueryRewriteNone && s.QueryRewrite != QueryRewriteLowercaseTrim {
		return fmt.Errorf("knowledge retrieval snapshot: unsupported query rewrite %q", s.QueryRewrite)
	}
	return nil
}

func validateScoreThreshold(threshold float64) error {
	if threshold < 0 || threshold > 1 {
		return errors.New("knowledge retrieval snapshot: score_threshold must be between 0 and 1")
	}
	return nil
}

func validateRerankIdentity(identity string) error {
	provider, model := domain.SplitRerankIdentity(identity)
	if !domain.AllowedRerankIdentities[provider] ||
		(model == "" && provider != "" && provider != RerankIdentityBuiltin) {
		return fmt.Errorf("knowledge retrieval snapshot: unsupported reranking %q", identity)
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
	// The evaluation worker runs as a tenant-admin privileged path (no end-user
	// viewer identity): SkipAccessCheck is the explicit bypass equivalent to
	// the admin-owner exemption in the D1 matrix, never an implicit full scan.
	sources, err := e.retrieveSources(ctx, snapshot, testCase.Query, "")
	if err != nil {
		return RetrievalEvaluation{}, err
	}
	documentIDs := make([]string, 0, min(snapshot.TopK, len(sources)))
	for _, source := range sources {
		documentIDs = append(documentIDs, source.DocumentID)
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

func (e *RetrievalEvaluator) RetrieveContext(
	ctx context.Context, snapshot RetrievalSnapshot, query, viewerID string,
) (string, error) {
	// D3 gate: this path resolves no visible set itself (the snapshot carries
	// only workspace identity), so an empty viewer identity fails closed.
	if viewerID == "" {
		return "", errors.New("knowledge retrieval: viewer identity required")
	}
	sources, err := e.retrieveSources(ctx, snapshot, query, viewerID)
	if err != nil {
		return "", err
	}
	var content strings.Builder
	for _, source := range sources {
		content.WriteString(source.Content)
		content.WriteString("\n---\n")
	}
	return content.String(), nil
}

func (e *RetrievalEvaluator) retrieveSources(
	ctx context.Context, snapshot RetrievalSnapshot, rawQuery, viewerID string,
) ([]Source, error) {
	if e == nil || e.retriever == nil {
		return nil, errors.New("knowledge retrieval evaluator unavailable")
	}
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	query := rewriteEvaluationQuery(rawQuery, snapshot.QueryRewrite)
	if query == "" {
		return nil, errors.New("knowledge retrieval evaluator: query required")
	}
	result, err := e.retriever.Query(ctx, RAGQueryRequest{
		Question: query, Workspace: snapshot.WorkspaceName, WorkspaceID: snapshot.WorkspaceID,
		TenantID: tenantIDFromContext(ctx), Mode: snapshot.QueryMode, TopK: snapshot.TopK,
		EmbeddingModel: snapshot.EmbeddingModel,
		Reranking:      snapshot.Reranking,
		ScoreThreshold: float32(snapshot.ScoreThreshold),
		RerankTopK:     snapshot.TopK,
		// The internal worker path (EvaluateRetrieval) has no end-user viewer
		// identity and explicitly bypasses the D2 gate — the D1 admin-owner
		// equivalent. System-actor contexts bypass too (privileged wiring).
		ViewerID:        viewerID,
		SkipAccessCheck: viewerID == "" || reqctx.SystemActorFromContext(ctx) != "",
	})
	if err != nil {
		return nil, ErrRetrievalDependency
	}
	if result == nil {
		return nil, errors.New("knowledge retrieval evaluator: empty retrieval result")
	}
	// Rerank, threshold filtering, and TopK narrowing are all performed inside
	// RAGService.Query; the evaluator must not re-implement them locally.
	return result.Sources, nil
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
