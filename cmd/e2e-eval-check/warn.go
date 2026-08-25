package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	warnStrong = "strong"
	warnWarn   = "warn"
)

// WARN rule ids.
const (
	warnRegression       = "regression"
	warnConfigDrift      = "config_drift"
	warnFingerprintDrift = "fingerprint_drift"
	warnEmptyResult      = "empty_result"
	warnProviderDrift    = "provider_drift"
)

type warning struct {
	ID      string `json:"id"`
	Level   string `json:"level"`
	Message string `json:"message"`
	CaseID  string `json:"case_id,omitempty"`
}

func (w warning) isStrong() bool { return w.Level == warnStrong }

func hasStrongWarn(warnings []warning) bool {
	for _, w := range warnings {
		if w.isStrong() {
			return true
		}
	}
	return false
}

func strongWarnIDs(warnings []warning) []string {
	var ids []string
	for _, w := range warnings {
		if w.isStrong() {
			ids = append(ids, w.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// regressionMetrics returns the ordered metric extractors for a kind. pass_rate
// is universal; judge_mean applies to skill/agent; the RAG ordering metrics
// apply to knowledge.
func regressionMetrics(kind string) []metricExtractor {
	base := []metricExtractor{{name: "pass_rate", run: func(a aggregate) float64 { return a.PassRate }}}
	switch kind {
	case "skill", "agent":
		return append(base, metricExtractor{name: "judge_mean", run: func(a aggregate) float64 { return a.JudgeMean }})
	case "knowledge":
		return append(base,
			metricExtractor{name: "recall", run: func(a aggregate) float64 { return a.Recall }},
			metricExtractor{name: "mrr", run: func(a aggregate) float64 { return a.MRR }},
			metricExtractor{name: "ndcg", run: func(a aggregate) float64 { return a.NDCG }},
		)
	default:
		return base
	}
}

type metricExtractor struct {
	name string
	run  func(a aggregate) float64
}

// compareBaseline produces regression warnings against a recorded baseline.
// A changed fingerprint marks the run non-comparable and suppresses delta
// regressions: a false regression signal is worse than none. Regressions
// already explicitly accepted (accepted_regressions) are not re-reported.
func compareBaseline(kind string, fp fingerprint, agg aggregate, base *baseline, accepted []acceptedRegression, runCommit string, delta float64) ([]warning, bool) {
	if base == nil {
		return nil, false
	}
	var warnings []warning
	nonComparable := false
	if fp.Hash != base.Fingerprint.Hash {
		warnings = append(warnings, warning{
			ID: warnFingerprintDrift, Level: warnWarn,
			Message: fmt.Sprintf("config fingerprint drifted: baseline %q, run %q", base.Fingerprint.Hash, fp.Hash),
		})
		nonComparable = true
	}
	if fp.ProviderHash != "" && base.Fingerprint.ProviderHash != "" && fp.ProviderHash != base.Fingerprint.ProviderHash {
		warnings = append(warnings, warning{
			ID: warnProviderDrift, Level: warnWarn,
			Message: "provider declaration drifted between baseline and run",
		})
		nonComparable = true
	}
	if nonComparable {
		return warnings, true
	}
	for _, m := range regressionMetrics(kind) {
		runValue := m.run(agg)
		baseValue := m.run(base.Aggregate)
		if runValue >= baseValue-delta {
			continue
		}
		if acceptedCovers(accepted, m.name, runValue, runCommit) {
			continue
		}
		warnings = append(warnings, warning{
			ID: warnRegression, Level: warnStrong,
			Message: fmt.Sprintf("%s regressed: baseline %.4f, run %.4f (delta %.4f > warn-delta %.2f)",
				m.name, baseValue, runValue, baseValue-runValue, delta),
		})
	}
	return warnings, false
}

// acceptedCovers reports whether an explicit accepted_regressions entry exists
// for the metric on the same commit and the run has not degraded further.
func acceptedCovers(accepted []acceptedRegression, metric string, run float64, commit string) bool {
	for _, acc := range accepted {
		if acc.Metric != metric || acc.Commit != commit {
			continue
		}
		if run >= acc.Run-0.0001 {
			return true
		}
	}
	return false
}

// recordBaselineGate is the fail-closed check before persisting a baseline.
func recordBaselineGate(warnings []warning, confirm bool) error {
	if !confirm {
		return errors.New("--record-baseline requires --confirm-record: refusing to write without explicit confirmation")
	}
	if hasStrongWarn(warnings) {
		return fmt.Errorf("refusing to record baseline: strong warnings present (%s); fix or explicitly accept before recording",
			strings.Join(strongWarnIDs(warnings), ", "))
	}
	return nil
}
