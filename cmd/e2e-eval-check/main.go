package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// run is the top-level pipeline shared by every kind.
func run(ctx context.Context, o options, stdout, stderr io.Writer) (int, error) {
	if o.skip != "" {
		return runSkipped(o, stdout)
	}
	pointPath, err := resolvePointPath(o.kind, o.point)
	if err != nil {
		return exitInfraFailed, err
	}
	p, err := loadPoint(pointPath)
	if err != nil {
		return exitFailed, err
	}
	ex, err := newExecutor(p.Kind)
	if err != nil {
		return exitFailed, err
	}
	result, err := ex.Execute(ctx, o, p)
	if err != nil {
		// Executor errors are classified as infra vs defect; a failed run
		// still surfaces residuals and a report via the caller.
		return classifyError(err), err
	}
	return finalizeRun(o, p, result, stdout)
}

// finalizeRun applies the post-execution pipeline: baseline comparison,
// optional baseline recording, the warn/fail gate, report serialization and
// the human-readable summary.
func finalizeRun(o options, p point, result execResult, stdout io.Writer) (int, error) {
	code := exitPassed
	base, err := loadBaselineFromPoint(p)
	if err != nil {
		return exitFailed, err
	}
	warnings := append([]warning(nil), result.Warnings...)
	w, nonComparable, bd := compareRunToBaseline(o, p, result, base)
	warnings = append(warnings, w...)
	base, err = recordBaselineIfRequested(o, p, result, warnings, base)
	if err != nil {
		return exitFailed, err
	}
	if o.failOnWarn && hasStrongWarn(warnings) {
		code = exitFailed
	}
	r := report{
		Version:             reportSchemaVersion,
		Kind:                p.Kind,
		Point:               p.Key,
		Status:              statusOf(code),
		GeneratedAt:         nowUTC(),
		Snapshot:            p.Snapshot,
		Cases:               result.Cases,
		Aggregate:           result.Aggregate,
		Baseline:            base,
		BaselineDelta:       bd,
		Warnings:            warnings,
		NonComparable:       nonComparable,
		ResidualEntities:    result.Residuals,
		Evidence:            result.Evidence,
		AcceptedRegressions: acceptedRegressionsFrom(base),
	}
	if err := writeReportIfRequested(o.output, r); err != nil {
		return exitFailed, err
	}
	printSummary(stdout, r)
	return code, nil
}

// compareRunToBaseline produces regression warnings and the run-vs-baseline
// delta summary. A nil baseline (first recording) yields no warnings and full
// comparability.
func compareRunToBaseline(o options, p point, result execResult, base *baseline) ([]warning, bool, *baselineDelta) {
	if base == nil {
		return nil, false, nil
	}
	fp := fingerprintOfPoint(o, p)
	w, nc := compareBaseline(p.Kind, fp, result.Aggregate, base, base.AcceptedRegressions, gitCommit(), o.warnDelta)
	bd := &baselineDelta{
		RunPassRate:   result.Aggregate.PassRate,
		BasePassRate:  base.Aggregate.PassRate,
		PassRateDelta: result.Aggregate.PassRate - base.Aggregate.PassRate,
	}
	return w, nc, bd
}

// recordBaselineIfRequested persists the run as the new baseline once the
// fail-closed gate passes. It is a no-op when recording was not requested.
func recordBaselineIfRequested(o options, p point, result execResult, warnings []warning, base *baseline) (*baseline, error) {
	if !o.recordBaseline {
		return base, nil
	}
	if err := recordBaselineGate(warnings, o.confirmRecord); err != nil {
		return nil, err
	}
	fp := fingerprintOfPoint(o, p)
	b := baseline{
		RecordedCommit:      gitCommit(),
		RecordedAt:          nowUTC(),
		Kind:                p.Kind,
		Point:               p.Key,
		Fingerprint:         fp,
		Aggregate:           result.Aggregate,
		AcceptedRegressions: baseAcceptedRegressions(base),
	}
	bp, err := baselinePathFromPoint(p)
	if err != nil {
		return nil, err
	}
	if err := writeBaseline(bp, b); err != nil {
		return nil, err
	}
	return &b, nil
}

// writeReportIfRequested serializes the report when an output path is given.
func writeReportIfRequested(output string, r report) error {
	if output == "" {
		return nil
	}
	return writeReport(output, r)
}

func runSkipped(o options, stdout io.Writer) (int, error) {
	r := report{
		Version:             reportSchemaVersion,
		Kind:                o.kind,
		Point:               o.point,
		Status:              statusNotRun,
		GeneratedAt:         nowUTC(),
		Snapshot:            map[string]any{},
		Cases:               []caseOutcome{},
		Warnings:            []warning{},
		ResidualEntities:    []string{},
		Evidence:            []evidence{},
		AcceptedRegressions: []acceptedRegression{},
		SkipReason:          o.skip,
	}
	if o.output != "" {
		if err := writeReport(o.output, r); err != nil {
			return exitFailed, err
		}
	}
	_, _ = fmt.Fprintf(stdout, "kind=%s point=%s status=%s reason=%s\n", o.kind, o.point, statusNotRun, o.skip)
	return exitPassed, nil
}

func main() {
	o, err := parseOptions(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e-eval-check: %v\n", err)
		os.Exit(exitFailed)
	}
	if o.selfCheck {
		code, err := selfCheck(o, os.Stdout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "e2e-eval-check: %v\n", err)
		}
		os.Exit(code)
	}
	code, err := run(context.Background(), o, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e-eval-check: %v\n", err)
	}
	os.Exit(code)
}

// helper stubs (implemented or replaced by later tasks):
func resolvePointPath(kind, key string) (string, error) {
	return filepath.Join("test", "e2e", kind, "points", key+".yaml"), nil
}
func classifyError(err error) int {
	if isInfra(err) {
		return exitInfraFailed
	}
	return exitFailed
}
func loadBaselineFromPoint(p point) (*baseline, error) {
	path, err := resolveRelative(p.Dir, p.Baseline)
	if err != nil {
		return nil, err
	}
	return loadBaseline(path)
}
func baselinePathFromPoint(p point) (string, error) {
	return resolveRelative(p.Dir, p.Baseline)
}
func fingerprintOfPoint(o options, p point) fingerprint {
	switch p.Kind {
	case "knowledge":
		return knowledgeFingerprint(p.Snapshot, o.provider)
	case "mcp":
		return mcpFingerprint(p.Snapshot)
	default: // skill / agent
		return llmFingerprint(p.Snapshot)
	}
}
func baseAcceptedRegressions(base *baseline) []acceptedRegression {
	if base == nil {
		return []acceptedRegression{}
	}
	return base.AcceptedRegressions
}
func acceptedRegressionsFrom(base *baseline) []acceptedRegression {
	return baseAcceptedRegressions(base)
}
func gitCommit() string { return "unknown" }
func nowUTC() string    { return time.Now().UTC().Format(time.RFC3339) }
