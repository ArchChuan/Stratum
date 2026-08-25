package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
)

// status strings match the exit-code contract.
const (
	statusPassed    = "passed"
	statusFailed    = "failed"
	statusInfraFail = "infra_failed"
	statusNotRun    = "not_run"
)

// caseOutcome is the per-case result shared by all kinds. knowledge-specific
// retrieval fields are filled only by the knowledge executor (omitempty).
type caseOutcome struct {
	CaseID        string  `json:"case_id"`
	Passed        bool    `json:"passed"`
	AssertionMode string  `json:"assertion_mode,omitempty"`
	JudgeScore    float64 `json:"judge_score,omitempty"`
	JudgeReason   string  `json:"judge_reason,omitempty"`
	LatencyMS     int64   `json:"latency_ms,omitempty"`
	CostUSD       float64 `json:"cost_usd,omitempty"`
	Error         string  `json:"error,omitempty"`
	// knowledge ordering metrics (filled only for kind=knowledge)
	Recall          float64  `json:"recall,omitempty"`
	MRR             float64  `json:"mrr,omitempty"`
	NDCG            float64  `json:"ndcg,omitempty"`
	RetrievedCount  int      `json:"retrieved_count,omitempty"`
	RetrievedDocIDs []string `json:"retrieved_document_ids,omitempty"`
	NoAnswerPass    *bool    `json:"no_answer_pass,omitempty"`
	CitationCorrect *bool    `json:"citation_correct,omitempty"`
}

// aggregate is the rolled-up signal. pass_rate is the primary regression
// signal for every kind; judge_mean and the knowledge metrics are kind-scoped.
type aggregate struct {
	CaseCount    int     `json:"case_count"`
	PassRate     float64 `json:"pass_rate"`
	JudgeMean    float64 `json:"judge_mean,omitempty"`
	AvgLatencyMS int64   `json:"avg_latency_ms,omitempty"`
	AvgCostUSD   float64 `json:"avg_cost_usd,omitempty"`
	// knowledge case-partition counts (filled only for kind=knowledge)
	NonNoAnswerCount int `json:"non_no_answer_count,omitempty"`
	NoAnswerCount    int `json:"no_answer_count,omitempty"`
	// knowledge metrics
	Recall           float64 `json:"recall,omitempty"`
	MRR              float64 `json:"mrr,omitempty"`
	NDCG             float64 `json:"ndcg,omitempty"`
	RelevantRate     float64 `json:"relevant_rate,omitempty"`
	NoAnswerPassRate float64 `json:"no_answer_pass_rate,omitempty"`
	CitationPassRate float64 `json:"citation_pass_rate,omitempty"`
}

// evidence records one runtime artifact that supports the run (e.g. a created
// workspace id, a MCP server endpoint, an executed agent id).
type evidence struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

// report is the unified JSON output shared by all kinds.
type report struct {
	Version             int                  `json:"version"`
	Kind                string               `json:"kind"`
	Point               string               `json:"point"`
	Status              string               `json:"status"`
	GeneratedAt         string               `json:"generated_at"`
	Snapshot            map[string]any       `json:"snapshot"`
	Cases               []caseOutcome        `json:"cases"`
	Aggregate           aggregate            `json:"aggregate"`
	Baseline            *baseline            `json:"baseline,omitempty"`
	BaselineDelta       *baselineDelta       `json:"baseline_delta,omitempty"`
	Warnings            []warning            `json:"warnings"`
	NonComparable       bool                 `json:"non_comparable"`
	SkipReason          string               `json:"skip_reason,omitempty"`
	ResidualEntities    []string             `json:"residual_entities"`
	Evidence            []evidence           `json:"evidence"`
	AcceptedRegressions []acceptedRegression `json:"accepted_regressions"`
}

func statusOf(code int) string {
	switch code {
	case exitPassed:
		return statusPassed
	case exitInfraFailed:
		return statusInfraFail
	default:
		return statusFailed
	}
}

// writeReport serializes the report with schema constraints: array fields are
// normalized to empty slices, never null.
func writeReport(path string, r report) error {
	if r.Cases == nil {
		r.Cases = []caseOutcome{}
	}
	if r.Warnings == nil {
		r.Warnings = []warning{}
	}
	if r.ResidualEntities == nil {
		r.ResidualEntities = []string{}
	}
	if r.Evidence == nil {
		r.Evidence = []evidence{}
	}
	if r.AcceptedRegressions == nil {
		r.AcceptedRegressions = []acceptedRegression{}
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write report %s: %w", path, err)
	}
	return nil
}

// printSummary renders a compact human summary to stdout.
func printSummary(w io.Writer, r report) {
	_, _ = fmt.Fprintf(w, "kind=%s point=%s status=%s\n", r.Kind, r.Point, r.Status)
	_, _ = fmt.Fprintf(w, "cases=%d pass_rate=%.4f\n", r.Aggregate.CaseCount, r.Aggregate.PassRate)
	if r.NonComparable {
		_, _ = fmt.Fprintf(w, "non_comparable=true (config/fingerprint drift)\n")
	}
	for _, warn := range r.Warnings {
		_, _ = fmt.Fprintf(w, "warn[%s] %s: %s\n", warn.Level, warn.ID, warn.Message)
	}
	if len(r.AcceptedRegressions) > 0 {
		sort.Slice(r.AcceptedRegressions, func(i, j int) bool {
			return r.AcceptedRegressions[i].Metric < r.AcceptedRegressions[j].Metric
		})
	}
	for _, acc := range r.AcceptedRegressions {
		_, _ = fmt.Fprintf(w, "accepted_regression %s baseline=%.4f run=%.4f reason=%s\n", acc.Metric, acc.Baseline, acc.Run, acc.Reason)
	}
}
