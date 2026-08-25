package main

import (
	"context"
	"crypto/sha256"
	"fmt"

	knowledgeapp "github.com/byteBuilderX/stratum/internal/knowledge/application"
)

// knowledge WARN rule ids, distinct from the unified warn.go rules. The
// regression rules (recall/mrr/ndcg) live in the unified compareBaseline via
// warnRegression; embedding_drift and provider_not_declared are kept for the
// knowledge-specific drift vocabulary (Task 7 wiring may surface them).
const (
	warnRecallZero          = "recall_zero"
	warnEmptyRetrieval      = "empty_retrieval"
	warnNoAnswerRegression  = "no_answer_regression"
	warnEmbeddingDrift      = "embedding_drift"
	warnProviderNotDeclared = "provider_not_declared"
)

// knowledgeCaseResult is the per-case knowledge outcome: the deterministic
// evaluation booleans plus the ordering metrics computed from the shared
// knowledgeapp metrics. It is the intermediate used to build the unified
// caseOutcome and aggregate.
type knowledgeCaseResult struct {
	CaseID               string
	ExpectNoAnswer       bool
	RelevantDocuments    []string
	CitationDocuments    []string
	RetrievedCount       int
	RetrievedDocumentIDs []string
	Recall               float64
	MRR                  float64
	NDCG                 float64
	Relevant             bool
	NoAnswer             bool
	NoAnswerPass         bool
	CitationCorrect      bool
}

// evaluateCases runs every golden case through the shared RetrievalEvaluator
// over the real HTTP retriever and computes the ordering metrics. A case-level
// error aborts the run: a failing case is a defect (or infra, if classified).
func evaluateCases(ctx context.Context, retriever knowledgeapp.EvaluationRetriever, golden goldenSet,
	docIDs map[string]string, workspaceName, workspaceID string) ([]knowledgeCaseResult, error) {
	evaluator := knowledgeapp.NewRetrievalEvaluator(retriever)
	results := make([]knowledgeCaseResult, 0, len(golden.Cases))
	for _, tc := range golden.Cases {
		result, err := evaluateCase(ctx, evaluator, tc, golden.Snapshot, docIDs, workspaceName, workspaceID)
		if err != nil {
			return nil, fmt.Errorf("evaluate case %s: %w", tc.ID, err)
		}
		results = append(results, result)
	}
	return results, nil
}

func evaluateCase(ctx context.Context, evaluator *knowledgeapp.RetrievalEvaluator, tc goldenCase, snap goldenSnapshot,
	docIDs map[string]string, workspaceName, workspaceID string) (knowledgeCaseResult, error) {
	// QueryMode is a per-case HTTP query mode (vector/keyword/hybrid). The
	// workspace creation config uses snap.QueryMode (a valid workspace mode);
	// the snapshot here carries the per-case mode, which the domain only
	// requires to be non-empty and the HTTP path serves for all three.
	snapshot := knowledgeapp.RetrievalSnapshot{
		WorkspaceID:    workspaceID,
		WorkspaceName:  workspaceName,
		EmbeddingModel: snap.EmbeddingModel,
		QueryMode:      tc.Mode,
		TopK:           snap.TopK,
		ScoreThreshold: snap.ScoreThreshold,
		Reranking:      snap.Reranking,
		QueryRewrite:   snap.QueryRewrite,
	}
	eval, err := evaluator.EvaluateRetrieval(ctx, snapshot, knowledgeapp.RetrievalCase{
		Query:               tc.Query,
		RelevantDocumentIDs: toDocumentIDs(tc.RelevantDocuments, docIDs),
		CitationDocumentIDs: toDocumentIDs(tc.CitationDocuments, docIDs),
		ExpectNoAnswer:      tc.ExpectNoAnswer,
	})
	if err != nil {
		return knowledgeCaseResult{}, err
	}
	retrieved, relevant := eval.RetrievedDocumentIDs, toDocumentIDs(tc.RelevantDocuments, docIDs)
	k := snap.TopK
	noAnswerPass := !tc.ExpectNoAnswer || eval.NoAnswer
	return knowledgeCaseResult{
		CaseID:               tc.ID,
		ExpectNoAnswer:       tc.ExpectNoAnswer,
		RelevantDocuments:    tc.RelevantDocuments,
		CitationDocuments:    tc.CitationDocuments,
		RetrievedCount:       eval.RetrievedCount,
		RetrievedDocumentIDs: retrieved,
		Recall:               knowledgeapp.RecallAtK(retrieved, relevant, k),
		MRR:                  knowledgeapp.MRR(retrieved, relevant),
		NDCG:                 knowledgeapp.NDCGAtK(retrieved, relevant, k),
		Relevant:             eval.Relevant,
		NoAnswer:             eval.NoAnswer,
		NoAnswerPass:         noAnswerPass,
		CitationCorrect:      eval.CitationCorrect,
	}, nil
}

func toDocumentIDs(sources []string, docIDs map[string]string) []string {
	ids := make([]string, 0, len(sources))
	for _, source := range sources {
		if id, ok := docIDs[source]; ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// citationAnnotatedCount counts cases that declared citation documents; the
// citation pass rate is evaluated only over annotated cases.
func citationAnnotatedCount(cases []knowledgeCaseResult) int {
	count := 0
	for _, c := range cases {
		if len(c.CitationDocuments) > 0 {
			count++
		}
	}
	return count
}

// buildCaseWarnings converts per-case evaluation defects into WARN entries.
func buildCaseWarnings(cases []knowledgeCaseResult) []warning {
	var warnings []warning
	for _, c := range cases {
		switch {
		case !c.ExpectNoAnswer && c.RetrievedCount == 0:
			warnings = append(warnings, warning{
				ID: warnEmptyRetrieval, Level: warnStrong, CaseID: c.CaseID,
				Message: fmt.Sprintf("case %s: empty retrieval for an answerable question (retrieved 0, expected %d)",
					c.CaseID, len(c.RelevantDocuments)),
			})
		case !c.ExpectNoAnswer && c.Recall == 0:
			warnings = append(warnings, warning{
				ID: warnRecallZero, Level: warnStrong, CaseID: c.CaseID,
				Message: fmt.Sprintf("case %s: recall 0 — no relevant document retrieved among %d expected",
					c.CaseID, len(c.RelevantDocuments)),
			})
		case c.ExpectNoAnswer && !c.NoAnswer:
			warnings = append(warnings, warning{
				ID: warnNoAnswerRegression, Level: warnWarn, CaseID: c.CaseID,
				Message: fmt.Sprintf("case %s: expected no answer but retrieval produced %d results", c.CaseID, c.RetrievedCount),
			})
		}
	}
	return warnings
}

// shortHash is a compact sha256 hex prefix used for fingerprinting.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum[:8])
}
