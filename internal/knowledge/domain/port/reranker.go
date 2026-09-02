package port

import "context"

// RerankRequest is the input for an external reranking call. Documents are
// the candidate texts in retrieval order; the reranker re-scores them against
// Query. Sending Documents to an external service shares tenant data with a
// third party — the platform config that enables a reranker backend is the
// approval gate for that data flow.
type RerankRequest struct {
	Query     string
	Documents []string
	Model     string
	TopN      int
	// ScoringInstructions 是内置语义重排（builtin-score-v1）的评分指令附加段；
	// 外部 reranker（Cohere）不消费该字段，构造时为零值即可。
	ScoringInstructions string
}

// RerankResult is one reranked candidate: Index references the caller's
// original Documents slice, Score is the reranker's relevance score.
type RerankResult struct {
	Index int
	Score float32
}

// Reranker re-scores retrieval candidates with an external model.
// A nil Reranker means the backend is not configured: callers must fail
// closed rather than silently skip reranking.
type Reranker interface {
	Rerank(ctx context.Context, req RerankRequest) ([]RerankResult, error)
}
