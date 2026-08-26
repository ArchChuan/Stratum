package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// TestRunPipelineInfraReportWritten pins I2: when Execute fails, run() must
// still serialize a minimal report (status infra_failed) carrying the executor's
// residuals, evidence, snapshot and any partial cases — a failed run must never
// silently drop its report.
// TestRunPipelineJudgeInfraExit2FirstRun is the C1 integration test: through
// the real llmExecutor, a judge service outage (HTTP 500) on a first run with
// no baseline must exit 2 (infra_failed) and write a report with
// status=infra_failed — never exit 0/passed. This guards the regression where
// infra errors were flattened into case errors and the whole run stayed green.
func TestRunPipelineJudgeInfraExit2FirstRun(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	t.Setenv("JWT_PRIVATE_KEY_PEM", string(pemBytes))

	agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/agents/a1/execute" {
			_, _ = w.Write([]byte(`{"output":"planning output"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer agentServer.Close()
	judgeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "judge down", http.StatusInternalServerError)
	}))
	defer judgeServer.Close()

	dir := t.TempDir()
	t.Chdir(dir)
	pointBody := fmt.Sprintf(`kind: agent
point: p1
snapshot:
  id: a1
  model: m
judge:
  base_url: %s
  model: m
golden: ../golden/cases.yaml
baseline: ../baselines/p1.json
`, judgeServer.URL)
	writeTestFile(t, filepath.Join("test", "e2e", "agent", "points", "p1.yaml"), pointBody)
	writeTestFile(t, filepath.Join("test", "e2e", "agent", "golden", "cases.yaml"), `
version: 1
cases:
  - id: j1
    query: "plan tomorrow"
    mode: judge
    judge_spec:
      criteria: "output must contain a schedule"
`)

	reportPath := filepath.Join(dir, "report.json")
	var out, errOut bytes.Buffer
	code, err := run(context.Background(), options{
		kind: "agent", point: "p1", output: reportPath, warnDelta: DefaultWarnDelta,
		baseURL: agentServer.URL, tenantID: "tenant-1", userID: "user-1",
	}, &out, &errOut)
	if err == nil {
		t.Fatal("judge infra failure must propagate as error")
	}
	if code != exitInfraFailed {
		t.Fatalf("judge 500 on first run must exit %d (infra_failed), got %d\nout:\n%s\nerr:\n%s",
			exitInfraFailed, code, out.String(), errOut.String())
	}
	data, rerr := os.ReadFile(reportPath)
	if rerr != nil {
		t.Fatalf("report not written on judge infra failure: %v", rerr)
	}
	var r report
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if r.Status != statusInfraFail {
		t.Fatalf("report status = %q, want %q", r.Status, statusInfraFail)
	}
}

func TestRunPipelineInfraReportWritten(t *testing.T) {
	registerStubKnowledgeExecutor(t)
	dir := t.TempDir()
	t.Chdir(dir)
	writeTestFile(t, filepath.Join("test", "e2e", "knowledge", "points", "p1.yaml"),
		"kind: knowledge\npoint: p1\ngolden: golden/cases.yaml\nbaseline: baselines/p1.json\n")
	stubExecResult = execResult{
		Cases:     []caseOutcome{{CaseID: "c1", Passed: false, Error: "partial before infra"}},
		Residuals: []string{"ws-1"},
		Evidence:  []evidence{{Kind: "workspace", Ref: "ws-1"}},
	}
	stubExecErr = &infraError{err: errors.New("server down")}

	reportPath := filepath.Join(dir, "report.json")
	var out, errOut bytes.Buffer
	code, err := run(context.Background(), options{
		kind: "knowledge", point: "p1", output: reportPath, warnDelta: DefaultWarnDelta,
	}, &out, &errOut)
	if err == nil || code != exitInfraFailed {
		t.Fatalf("infra error = (%d, %v), want (%d, error)", code, err, exitInfraFailed)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("report not written on infra error path: %v", err)
	}
	var r report
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if r.Status != statusInfraFail {
		t.Fatalf("report status = %q, want %q", r.Status, statusInfraFail)
	}
	if len(r.ResidualEntities) != 1 || r.ResidualEntities[0] != "ws-1" {
		t.Fatalf("report must carry residuals, got %v", r.ResidualEntities)
	}
	if len(r.Evidence) != 1 || r.Evidence[0].Ref != "ws-1" {
		t.Fatalf("report must carry evidence, got %v", r.Evidence)
	}
	if len(r.Cases) != 1 || r.Cases[0].Error == "" {
		t.Fatalf("report must carry partial cases, got %+v", r.Cases)
	}
	if r.GeneratedAt == "" {
		t.Fatal("report generated_at must be set on the error path")
	}
	if r.Kind != "knowledge" || r.Point != "p1" {
		t.Fatalf("report identity mismatch: kind=%q point=%q", r.Kind, r.Point)
	}
}
