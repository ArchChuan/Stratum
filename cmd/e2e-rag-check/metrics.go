package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	knowledgeapp "github.com/byteBuilderX/stratum/internal/knowledge/application"
)

// Warning levels. Strong warnings must be fixed or explicitly accepted (the
// SKILL completion gate); weak warnings are advisory and non-blocking.
const (
	warnStrong = "strong"
	warnWarn   = "warn"
)

// WARN rule ids, matching the SKILL presentation table.
const (
	warnRecallZero          = "recall_zero"
	warnEmptyRetrieval      = "empty_retrieval"
	warnNoAnswerRegression  = "no_answer_regression"
	warnNDCGRegression      = "ndcg_regression"
	warnRecallRegression    = "recall_regression"
	warnMRRRegression       = "mrr_regression"
	warnEmbeddingDrift      = "embedding_drift"
	warnConfigDrift         = "config_drift"
	warnProviderDrift       = "provider_drift"
	warnProviderNotDeclared = "provider_not_declared"
)

type warning struct {
	ID      string `json:"id"`
	Level   string `json:"level"`
	Message string `json:"message"`
	CaseID  string `json:"case_id,omitempty"`
}

func (w warning) isStrong() bool { return w.Level == warnStrong }

// caseResult is the per-case outcome: the deterministic evaluation booleans
// plus the ordering metrics computed from the shared metrics.go functions.
type caseResult struct {
	CaseID               string   `json:"case_id"`
	Query                string   `json:"query"`
	Mode                 string   `json:"mode"`
	ExpectNoAnswer       bool     `json:"expect_no_answer"`
	RelevantDocuments    []string `json:"relevant_documents,omitempty"`
	CitationDocuments    []string `json:"citation_documents,omitempty"`
	RetrievedCount       int      `json:"retrieved_count"`
	RetrievedDocumentIDs []string `json:"retrieved_document_ids,omitempty"`
	BestScore            float32  `json:"best_score,omitempty"`
	Recall               float64  `json:"recall"`
	Precision            float64  `json:"precision"`
	MRR                  float64  `json:"mrr"`
	NDCG                 float64  `json:"ndcg"`
	Relevant             bool     `json:"relevant"`
	NoAnswer             bool     `json:"no_answer"`
	NoAnswerPass         bool     `json:"no_answer_pass"`
	CitationCorrect      bool     `json:"citation_correct"`
}

// aggregate is the rolled-up signal: ordering metrics averaged over
// non-no-answer cases, plus per-concern pass rates.
type aggregate struct {
	CaseCount        int     `json:"case_count"`
	NonNoAnswerCount int     `json:"non_no_answer_count"`
	NoAnswerCount    int     `json:"no_answer_count"`
	Recall           float64 `json:"recall"`
	Precision        float64 `json:"precision"`
	MRR              float64 `json:"mrr"`
	NDCG             float64 `json:"ndcg"`
	RelevantRate     float64 `json:"relevant_rate"`
	NoAnswerPassRate float64 `json:"no_answer_pass_rate"`
	CitationPassRate float64 `json:"citation_pass_rate"`
}

// evaluateCases runs every golden case through the shared RetrievalEvaluator
// over the real HTTP retriever and computes the ordering metrics. A case-level
// error aborts the run: a failing case is a defect (or infra, if classified).
func evaluateCases(ctx context.Context, retriever knowledgeapp.EvaluationRetriever, golden goldenSet,
	docIDs map[string]string, workspaceName, workspaceID string) ([]caseResult, error) {
	evaluator := knowledgeapp.NewRetrievalEvaluator(retriever)
	results := make([]caseResult, 0, len(golden.Cases))
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
	docIDs map[string]string, workspaceName, workspaceID string) (caseResult, error) {
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
		return caseResult{}, err
	}
	retrieved, relevant := eval.RetrievedDocumentIDs, toDocumentIDs(tc.RelevantDocuments, docIDs)
	k := snap.TopK
	noAnswerPass := !tc.ExpectNoAnswer || eval.NoAnswer
	return caseResult{
		CaseID:               tc.ID,
		Query:                tc.Query,
		Mode:                 tc.Mode,
		ExpectNoAnswer:       tc.ExpectNoAnswer,
		RelevantDocuments:    tc.RelevantDocuments,
		CitationDocuments:    tc.CitationDocuments,
		RetrievedCount:       eval.RetrievedCount,
		RetrievedDocumentIDs: retrieved,
		BestScore:            eval.BestScore,
		Recall:               knowledgeapp.RecallAtK(retrieved, relevant, k),
		Precision:            knowledgeapp.PrecisionAtK(retrieved, relevant, k),
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

// computeAggregate rolls up per-case results per the phase-1 aggregation
// contract: ordering metrics averaged over non-no-answer cases, no-answer pass
// rate over expect-no-answer cases, citation pass rate over annotated cases.
func computeAggregate(cases []caseResult) aggregate {
	agg := aggregate{CaseCount: len(cases)}
	var recallSum, precisionSum, mrrSum, ndcgSum, relevantSum float64
	for _, c := range cases {
		if c.ExpectNoAnswer {
			agg.NoAnswerCount++
			if c.NoAnswerPass {
				agg.NoAnswerPassRate++
			}
			continue
		}
		agg.NonNoAnswerCount++
		recallSum += c.Recall
		precisionSum += c.Precision
		mrrSum += c.MRR
		ndcgSum += c.NDCG
		if c.Relevant {
			relevantSum++
		}
		if len(c.CitationDocuments) > 0 && c.CitationCorrect {
			agg.CitationPassRate++
		}
	}
	n := agg.NonNoAnswerCount
	if n > 0 {
		agg.Recall = recallSum / float64(n)
		agg.Precision = precisionSum / float64(n)
		agg.MRR = mrrSum / float64(n)
		agg.NDCG = ndcgSum / float64(n)
		agg.RelevantRate = relevantSum / float64(n)
	}
	if agg.NoAnswerCount > 0 {
		agg.NoAnswerPassRate /= float64(agg.NoAnswerCount)
	}
	annotated := citationAnnotatedCount(cases)
	if annotated > 0 {
		agg.CitationPassRate /= float64(annotated)
	}
	return agg
}

func citationAnnotatedCount(cases []caseResult) int {
	count := 0
	for _, c := range cases {
		if len(c.CitationDocuments) > 0 {
			count++
		}
	}
	return count
}

// buildCaseWarnings converts per-case evaluation defects into WARN entries.
func buildCaseWarnings(cases []caseResult) []warning {
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

// effectiveConfig is the config the server actually applied (echoed at
// workspace creation). It is the source of truth for config drift.
type effectiveConfig struct {
	EmbeddingModel string  `json:"embedding_model"`
	TopK           int     `json:"top_k"`
	ScoreThreshold float64 `json:"score_threshold"`
}

// providerFingerprint identifies the embedding backend the run measured
// against. ProviderBaseURLHash hashes the --provider declaration (a test
// tenant identifier the operator ties to the embedding backend); an empty
// declaration disables provider-drift detection and is recorded fail-closed.
type providerFingerprint struct {
	EmbeddingModel      string `json:"embedding_model"`
	ProviderBaseURLHash string `json:"provider_base_url_hash,omitempty"`
	ProviderDeclared    bool   `json:"provider_declared"`
}

func fingerprintOf(model, provider string) providerFingerprint {
	return providerFingerprint{
		EmbeddingModel:      model,
		ProviderBaseURLHash: shortHash(strings.TrimSpace(provider)),
		ProviderDeclared:    strings.TrimSpace(provider) != "",
	}
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum[:8])
}

// baseline is the fixed committed snapshot the run compares against. Never the
// previous run: drift anchors to a reviewable, recorded reference.
type baseline struct {
	RecordedCommit string              `json:"recorded_commit"`
	RecordedAt     string              `json:"recorded_at"`
	Provider       providerFingerprint `json:"provider"`
	Config         effectiveConfig     `json:"config"`
	Aggregate      aggregate           `json:"aggregate"`
}

// compareBaseline produces drift/regression warnings against a recorded
// baseline. Any drift marks the run non-comparable and suppresses delta
// regressions: a changed embedding model, provider, or effective config makes
// the delta meaningless, and a false regression signal is worse than none.
func compareBaseline(run providerFingerprint, config effectiveConfig, agg aggregate, base *baseline, warnDelta float64) ([]warning, bool) {
	if base == nil {
		return nil, false
	}
	var warnings []warning
	nonComparable := false

	if run.EmbeddingModel != base.Provider.EmbeddingModel {
		warnings = append(warnings, warning{
			ID: warnEmbeddingDrift, Level: warnWarn,
			Message: fmt.Sprintf("embedding model drifted: baseline %q, run %q", base.Provider.EmbeddingModel, run.EmbeddingModel),
		})
		nonComparable = true
	}
	if config.TopK != base.Config.TopK || config.ScoreThreshold != base.Config.ScoreThreshold {
		warnings = append(warnings, warning{
			ID: warnConfigDrift, Level: warnWarn,
			Message: fmt.Sprintf("effective config drifted: baseline top_k=%d threshold=%g, run top_k=%d threshold=%g",
				base.Config.TopK, base.Config.ScoreThreshold, config.TopK, config.ScoreThreshold),
		})
		nonComparable = true
	}
	if run.ProviderDeclared != base.Provider.ProviderDeclared ||
		(run.ProviderDeclared && run.ProviderBaseURLHash != base.Provider.ProviderBaseURLHash) {
		warnings = append(warnings, warning{
			ID: warnProviderDrift, Level: warnWarn,
			Message: "provider declaration drifted between baseline and run (base URL hash mismatch)",
		})
		nonComparable = true
	}
	if nonComparable {
		return warnings, true
	}

	regression := func(id, name string, runValue, baseValue float64) {
		if runValue < baseValue-warnDelta {
			warnings = append(warnings, warning{
				ID: id, Level: warnWarn,
				Message: fmt.Sprintf("%s regressed: baseline %.4f, run %.4f (delta %.4f > warn-delta %.2f)",
					name, baseValue, runValue, baseValue-runValue, warnDelta),
			})
		}
	}
	regression(warnRecallRegression, "recall", agg.Recall, base.Aggregate.Recall)
	regression(warnMRRRegression, "mrr", agg.MRR, base.Aggregate.MRR)
	regression(warnNDCGRegression, "ndcg", agg.NDCG, base.Aggregate.NDCG)
	return warnings, false
}

// hasStrongWarn reports whether any warning requires action (used by the
// record gate and --fail-on-warn).
func hasStrongWarn(warnings []warning) bool {
	for _, w := range warnings {
		if w.isStrong() {
			return true
		}
	}
	return false
}

// recordBaselineGate validates that a run is safe to persist as a baseline.
// Fail-closed (B2): a baseline must not enshrine a broken retrieval state.
func recordBaselineGate(cases []caseResult, warnings []warning, provider string, confirm bool) error {
	if !confirm {
		return errors.New("--record-baseline requires --confirm-record: refusing to write without explicit confirmation")
	}
	if strings.TrimSpace(provider) == "" {
		return errors.New("--record-baseline requires --provider: provider-drift detection is disabled without a declared embedding backend")
	}
	if hasStrongWarn(warnings) {
		return fmt.Errorf("refusing to record baseline: strong warnings present (%s); fix or explicitly accept before recording",
			strings.Join(strongWarnIDs(warnings), ", "))
	}
	return nil
}

func strongWarnIDs(warnings []warning) []string {
	var ids []string
	for _, w := range warnings {
		if w.isStrong() {
			ids = append(ids, w.ID)
		}
	}
	return ids
}
