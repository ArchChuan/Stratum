package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Report is the machine-readable artifact the SKILL gates on. status aligns
// with the local-verification status enum so the skill can route it uniformly.
type Report struct {
	Version       int                 `json:"version"`
	Status        string              `json:"status"` // passed|failed|not_run|infra_failed
	GeneratedAt   time.Time           `json:"generated_at"`
	DatasetDir    string              `json:"dataset_dir"`
	Workspace     string              `json:"workspace"`
	Snapshot      goldenSnapshot      `json:"snapshot"`
	Config        effectiveConfig     `json:"config"`
	Provider      providerFingerprint `json:"provider"`
	Cases         []caseResult        `json:"cases"`
	Aggregate     aggregate           `json:"aggregate"`
	Baseline      *baseline           `json:"baseline"`
	BaselineDelta *baselineDelta      `json:"baseline_delta,omitempty"`
	Warnings      []warning           `json:"warnings"`
	NonComparable bool                `json:"non_comparable"`
	SkipReason    string              `json:"skip_reason,omitempty"`
	Residuals     []string            `json:"residual_entities,omitempty"`
	Evidence      []string            `json:"evidence,omitempty"`
}

// baselineDelta is the run-vs-baseline comparison for the human summary.
// Baseline Delta is only meaningful when the run is comparable.
type baselineDelta struct {
	Recall float64 `json:"recall_delta"`
	MRR    float64 `json:"mrr_delta"`
	NDCG   float64 `json:"ndcg_delta"`
}

func newReport(status, datasetDir, workspace string, snap goldenSnapshot, cfg effectiveConfig,
	provider providerFingerprint, cases []caseResult, agg aggregate, base *baseline, warnings []warning,
	nonComparable bool, skipReason string, residuals []string) Report {
	// Normalize nil slices to empty arrays so the JSON report never emits
	// "warnings": null — the schema requires arrays.
	if cases == nil {
		cases = []caseResult{}
	}
	if warnings == nil {
		warnings = []warning{}
	}
	if residuals == nil {
		residuals = []string{}
	}
	report := Report{
		Version:       1,
		Status:        status,
		GeneratedAt:   time.Now().UTC(),
		DatasetDir:    datasetDir,
		Workspace:     workspace,
		Snapshot:      snap,
		Config:        cfg,
		Provider:      provider,
		Cases:         cases,
		Aggregate:     agg,
		Baseline:      base,
		Warnings:      warnings,
		NonComparable: nonComparable,
		SkipReason:    skipReason,
		Residuals:     residuals,
	}
	if base != nil && !nonComparable && agg.NonNoAnswerCount > 0 && base.Aggregate.NonNoAnswerCount > 0 {
		report.BaselineDelta = &baselineDelta{
			Recall: agg.Recall - base.Aggregate.Recall,
			MRR:    agg.MRR - base.Aggregate.MRR,
			NDCG:   agg.NDCG - base.Aggregate.NDCG,
		}
	}
	return report
}

// writeReport persists the JSON report, creating the parent directory. A
// failed write must surface: the skill treats report.json as the completion
// evidence.
func writeReport(path string, report Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil { // #nosec G703 -- report path is a developer CLI flag, not remote input
			return fmt.Errorf("create report dir: %w", err)
		}
	}
	if err := os.WriteFile(path, data, 0o600); err != nil { // #nosec G703 -- report path is a developer CLI flag, not remote input
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// printSummary renders the human-readable table the developer reviews. It is
// advisory: the JSON report and exit code are the contract.
func printSummary(out io.Writer, report Report) {
	w := summaryWriter{out: out}
	printSummaryHeader(&w, report)
	printSummaryAggregate(&w, report)
	printSummaryCases(&w, report.Cases)
	printSummaryWarnings(&w, report)
	w.f("\n")
}

// summaryWriter makes the advisory markdown printing errcheck-clean: every
// Fprintf result is captured and short-circuits on the first error. Output to
// a terminal buffer never errors, so this stays purely advisory.
type summaryWriter struct {
	out io.Writer
	err error
}

func (w *summaryWriter) f(format string, args ...any) {
	if w.err != nil {
		return
	}
	_, w.err = fmt.Fprintf(w.out, format, args...)
}

func printSummaryHeader(w *summaryWriter, report Report) {
	w.f("RAG retrieval check — status %s\n", report.Status)
	w.f("  workspace      : %s\n", report.Workspace)
	w.f("  embedding model: %s\n", report.Snapshot.EmbeddingModel)
	w.f("  query mode     : %s (top_k=%d threshold=%g)\n",
		report.Snapshot.QueryMode, report.Snapshot.TopK, report.Snapshot.ScoreThreshold)
	if report.SkipReason != "" {
		w.f("  skip reason    : %s\n", report.SkipReason)
	}
	if report.Baseline != nil {
		w.f("  baseline       : recorded %s @ %s (provider hash %s)\n",
			report.Baseline.RecordedAt, report.Baseline.RecordedCommit, report.Baseline.Provider.ProviderBaseURLHash)
	} else {
		w.f("  baseline       : not present (delta suppressed)\n")
	}
}

func printSummaryAggregate(w *summaryWriter, report Report) {
	agg := report.Aggregate
	w.f("\n  aggregate (%d cases, %d non-no-answer, %d no-answer):\n",
		agg.CaseCount, agg.NonNoAnswerCount, agg.NoAnswerCount)
	w.f("    recall %.4f  precision %.4f  mrr %.4f  ndcg %.4f\n",
		agg.Recall, agg.Precision, agg.MRR, agg.NDCG)
	w.f("    relevant_rate %.4f  no_answer_pass %.4f  citation_pass %.4f\n",
		agg.RelevantRate, agg.NoAnswerPassRate, agg.CitationPassRate)
	if report.BaselineDelta != nil {
		w.f("    vs baseline: recall %+.4f  mrr %+.4f  ndcg %+.4f\n",
			report.BaselineDelta.Recall, report.BaselineDelta.MRR, report.BaselineDelta.NDCG)
	}
}

func printSummaryCases(w *summaryWriter, cases []caseResult) {
	w.f("\n  per-case:\n")
	for _, c := range cases {
		mark := " "
		if c.ExpectNoAnswer {
			mark = "N"
		} else if c.Relevant {
			mark = "R"
		}
		w.f("    [%s] %-24s mode=%-8s recall=%.2f mrr=%.2f ndcg=%.2f retrieved=%d\n",
			mark, c.CaseID, c.Mode, c.Recall, c.MRR, c.NDCG, c.RetrievedCount)
	}
}

func printSummaryWarnings(w *summaryWriter, report Report) {
	if len(report.Warnings) > 0 {
		sort.SliceStable(report.Warnings, func(i, j int) bool {
			if report.Warnings[i].Level != report.Warnings[j].Level {
				return report.Warnings[i].Level == warnStrong
			}
			return report.Warnings[i].ID < report.Warnings[j].ID
		})
		w.f("\n  warnings (%d):\n", len(report.Warnings))
		for _, item := range report.Warnings {
			level := strings.ToUpper(item.Level)
			if item.CaseID != "" {
				w.f("    [%s] %s (%s): %s\n", level, item.ID, item.CaseID, item.Message)
			} else {
				w.f("    [%s] %s: %s\n", level, item.ID, item.Message)
			}
		}
	} else {
		w.f("\n  warnings: none\n")
	}
	if report.NonComparable {
		w.f("  non_comparable: true (delta suppressed; fix drift or re-record baseline)\n")
	}
	if len(report.Residuals) > 0 {
		w.f("  residual entities: %s\n", strings.Join(report.Residuals, ", "))
	}
}
