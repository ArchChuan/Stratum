package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubExec is a configurable fake executor used to drive run() end-to-end
// without touching the network. The real per-kind executors arrive in Tasks 2-5.
type stubExec struct{}

func (stubExec) Execute(ctx context.Context, o options, p point) (execResult, error) {
	return stubExecResult, stubExecErr
}

var (
	stubExecResult execResult
	stubExecErr    error
)

// registerStubKnowledgeExecutor wires the fake into the registry for the kind
// the point file declares. Task 1 has no real executors, so this is safe.
func registerStubKnowledgeExecutor(t *testing.T) {
	t.Helper()
	registerExecutor("knowledge", func() executor { return stubExec{} })
}

func TestRunSkipped(t *testing.T) {
	var out, errOut bytes.Buffer
	o := options{kind: "mcp", point: "p1", skip: "no real infra in CI", warnDelta: DefaultWarnDelta}
	code, err := run(context.Background(), o, &out, &errOut)
	if err != nil {
		t.Fatalf("run skipped: %v", err)
	}
	if code != exitPassed {
		t.Fatalf("skipped run must exit passed, got %d", code)
	}
	if !strings.Contains(out.String(), "status=not_run") || !strings.Contains(out.String(), "reason=no real infra in CI") {
		t.Fatalf("skipped summary missing status/reason:\n%s", out.String())
	}
}

func TestRunSkippedWritesReport(t *testing.T) {
	reportPath := filepath.Join(t.TempDir(), "report.json")
	var out, errOut bytes.Buffer
	o := options{kind: "agent", point: "p2", skip: "needs real LLM", output: reportPath, warnDelta: DefaultWarnDelta}
	code, err := run(context.Background(), o, &out, &errOut)
	if err != nil || code != exitPassed {
		t.Fatalf("run skipped = (%d, %v)", code, err)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("report not written: %v", err)
	}
	var r report
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if r.Status != statusNotRun || r.SkipReason != "needs real LLM" || r.Kind != "agent" {
		t.Fatalf("skipped report mismatch: %+v", r)
	}
	if r.Warnings == nil || r.Cases == nil || r.ResidualEntities == nil || r.Evidence == nil || r.AcceptedRegressions == nil {
		t.Fatal("skipped report arrays must be normalized to empty slices")
	}
}

func TestClassifyError(t *testing.T) {
	if got := classifyError(&infraError{err: errors.New("server down")}); got != exitInfraFailed {
		t.Fatalf("infra error must map to exit %d, got %d", exitInfraFailed, got)
	}
	if got := classifyError(errors.New("point failed")); got != exitFailed {
		t.Fatalf("defect error must map to exit %d, got %d", exitFailed, got)
	}
}

func TestRunPipelinePassing(t *testing.T) {
	registerStubKnowledgeExecutor(t)
	dir := t.TempDir()
	t.Chdir(dir)
	writeTestFile(t, filepath.Join("test", "e2e", "knowledge", "points", "p1.yaml"),
		"kind: knowledge\npoint: p1\ngolden: golden/cases.yaml\nbaseline: baselines/p1.json\n")
	stubExecResult = execResult{
		Cases:     []caseOutcome{{CaseID: "c1", Passed: true}},
		Aggregate: aggregate{CaseCount: 1, PassRate: 1.0},
	}
	stubExecErr = nil

	var out, errOut bytes.Buffer
	code, err := run(context.Background(), options{kind: "knowledge", point: "p1", warnDelta: DefaultWarnDelta}, &out, &errOut)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != exitPassed {
		t.Fatalf("passing run must exit %d, got %d\nout:\n%s\nerr:\n%s", exitPassed, code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "status=passed") || !strings.Contains(out.String(), "cases=1 pass_rate=1.0000") {
		t.Fatalf("passing summary mismatch:\n%s", out.String())
	}
}

func TestRunPipelineFailOnWarn(t *testing.T) {
	registerStubKnowledgeExecutor(t)
	dir := t.TempDir()
	t.Chdir(dir)
	writeTestFile(t, filepath.Join("test", "e2e", "knowledge", "points", "p1.yaml"),
		"kind: knowledge\npoint: p1\ngolden: golden/cases.yaml\nbaseline: baselines/p1.json\n")
	stubExecResult = execResult{
		Cases:     []caseOutcome{{CaseID: "c1", Passed: true}},
		Aggregate: aggregate{CaseCount: 1, PassRate: 1.0},
		Warnings:  []warning{{ID: warnRegression, Level: warnStrong, Message: "regressed"}},
	}
	stubExecErr = nil

	var out, errOut bytes.Buffer
	code, err := run(context.Background(), options{kind: "knowledge", point: "p1", warnDelta: DefaultWarnDelta, failOnWarn: true}, &out, &errOut)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != exitFailed {
		t.Fatalf("fail-on-warn with a strong warning must exit %d, got %d", exitFailed, code)
	}
	if !strings.Contains(out.String(), "status=failed") {
		t.Fatalf("failed summary mismatch:\n%s", out.String())
	}
}

func TestRunPipelineRecordBaseline(t *testing.T) {
	registerStubKnowledgeExecutor(t)
	dir := t.TempDir()
	t.Chdir(dir)
	writeTestFile(t, filepath.Join("test", "e2e", "knowledge", "points", "p1.yaml"),
		"kind: knowledge\npoint: p1\ngolden: golden/cases.yaml\nbaseline: baselines/p1.json\n")
	stubExecResult = execResult{
		Cases:     []caseOutcome{{CaseID: "c1", Passed: true}},
		Aggregate: aggregate{CaseCount: 1, PassRate: 1.0},
	}
	stubExecErr = nil

	var out, errOut bytes.Buffer
	o := options{kind: "knowledge", point: "p1", warnDelta: DefaultWarnDelta, recordBaseline: true, confirmRecord: true}
	code, err := run(context.Background(), o, &out, &errOut)
	if err != nil || code != exitPassed {
		t.Fatalf("record run = (%d, %v)", code, err)
	}
	basePath := filepath.Join("test", "e2e", "knowledge", "points", "baselines", "p1.json")
	if _, err := os.Stat(basePath); err != nil {
		t.Fatalf("baseline not persisted: %v", err)
	}
	got, err := loadBaseline(basePath)
	if err != nil {
		t.Fatalf("load persisted baseline: %v", err)
	}
	if got == nil || got.Point != "p1" || got.Aggregate.PassRate != 1.0 {
		t.Fatalf("persisted baseline mismatch: %+v", got)
	}
}

func TestRunPipelineInfraError(t *testing.T) {
	registerStubKnowledgeExecutor(t)
	dir := t.TempDir()
	t.Chdir(dir)
	writeTestFile(t, filepath.Join("test", "e2e", "knowledge", "points", "p1.yaml"),
		"kind: knowledge\npoint: p1\ngolden: golden/cases.yaml\nbaseline: baselines/p1.json\n")
	stubExecResult = execResult{}
	stubExecErr = &infraError{err: errors.New("server down")}

	var out, errOut bytes.Buffer
	code, err := run(context.Background(), options{kind: "knowledge", point: "p1", warnDelta: DefaultWarnDelta}, &out, &errOut)
	if err == nil {
		t.Fatal("infra error must propagate as error")
	}
	if code != exitInfraFailed {
		t.Fatalf("infra error must map to exit %d, got %d", exitInfraFailed, code)
	}
}
