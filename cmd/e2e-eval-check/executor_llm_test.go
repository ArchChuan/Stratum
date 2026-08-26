package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLLMExecutorJudgePath(t *testing.T) {
	// fake Stratum agent execute endpoint
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/agents/a1/execute" {
			_, _ = w.Write([]byte(`{"output":"planning output"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	// fake judge LLM endpoint
	judge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"passed\":true,\"score\":88,\"reason\":\"ok\"}"}}]}`))
	}))
	defer judge.Close()

	ex := &llmExecutor{
		agents:  &agentClient{client: newHTTPClient(server.URL, "test-token")},
		agentID: "a1",
		judge:   newJudgeClient(&judgeConfig{BaseURL: judge.URL, Model: "m"}, ""),
	}
	p := point{Kind: "agent", Snapshot: map[string]any{"id": "a1", "model": "m"},
		Dir: t.TempDir()}
	goldenPath := p.Dir + "/cases.yaml"
	writeTestFile(t, goldenPath, `
version: 1
cases:
  - id: j1
    query: "plan tomorrow"
    mode: judge
    judge_spec:
      criteria: "output must contain a schedule"
`)
	p.Golden = "cases.yaml"

	res, err := ex.runCases(context.Background(), mustLoadLLMSet(t, p))
	if err != nil {
		t.Fatalf("runCases: %v", err)
	}
	if len(res.Cases) != 1 || !res.Cases[0].Passed {
		t.Fatalf("judge case should pass: %+v", res.Cases)
	}
	if res.Aggregate.JudgeMean != 88 {
		t.Fatalf("judge_mean = %f, want 88", res.Aggregate.JudgeMean)
	}
}

func TestLLMExecutorAssertionPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":"hello world"}`))
	}))
	defer server.Close()
	ex := &llmExecutor{
		agents:  &agentClient{client: newHTTPClient(server.URL, "test-token")},
		agentID: "a1",
	}
	p := point{Kind: "agent", Dir: t.TempDir()}
	goldenPath := p.Dir + "/cases.yaml"
	writeTestFile(t, goldenPath, `
version: 1
cases:
  - id: c1
    query: "say hi"
    mode: contains
    expected_output: "hello"
`)
	p.Golden = "cases.yaml"
	res, err := ex.runCases(context.Background(), mustLoadLLMSet(t, p))
	if err != nil {
		t.Fatalf("runCases: %v", err)
	}
	if len(res.Cases) != 1 || !res.Cases[0].Passed {
		t.Fatalf("assertion case should pass: %+v", res.Cases)
	}
	if res.Aggregate.JudgeMean != 0 {
		t.Fatalf("assertion run must leave judge_mean zero, got %f", res.Aggregate.JudgeMean)
	}
}

func TestLLMExecutorMissingJudgeConfigFailsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":"output"}`))
	}))
	defer server.Close()
	// no judge configured: a judge case must fail closed, not silently pass.
	ex := &llmExecutor{
		agents:  &agentClient{client: newHTTPClient(server.URL, "test-token")},
		agentID: "a1",
	}
	p := point{Kind: "agent", Dir: t.TempDir()}
	goldenPath := p.Dir + "/cases.yaml"
	writeTestFile(t, goldenPath, `
version: 1
cases:
  - id: j1
    query: "q"
    mode: judge
    judge_spec:
      criteria: "c"
`)
	p.Golden = "cases.yaml"
	res, err := ex.runCases(context.Background(), mustLoadLLMSet(t, p))
	if err != nil {
		t.Fatalf("runCases: %v", err)
	}
	if res.Cases[0].Passed || res.Cases[0].Error == "" {
		t.Fatalf("judge case without point.judge must fail closed: %+v", res.Cases[0])
	}
}

func TestLLMExecutorAgentErrorFailsClosed(t *testing.T) {
	// A 4xx execute failure is a defect the dataset should never produce, not an
	// infrastructure break: it stays a case error and the run continues.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "agent boom", http.StatusBadRequest)
	}))
	defer server.Close()
	ex := &llmExecutor{
		agents:  &agentClient{client: newHTTPClient(server.URL, "test-token")},
		agentID: "a1",
	}
	p := point{Kind: "agent", Dir: t.TempDir()}
	goldenPath := p.Dir + "/cases.yaml"
	writeTestFile(t, goldenPath, `
version: 1
cases:
  - id: c1
    query: "q"
    mode: contains
    expected_output: "x"
`)
	p.Golden = "cases.yaml"
	res, err := ex.runCases(context.Background(), mustLoadLLMSet(t, p))
	if err != nil {
		t.Fatalf("runCases: %v", err)
	}
	if res.Cases[0].Passed || res.Cases[0].Error == "" {
		t.Fatalf("agent execute failure must fail the case: %+v", res.Cases[0])
	}
}

// TestLLMExecutorAgentInfraPropagates pins C1: an agent endpoint infrastructure
// failure (5xx / auth / network / decode) must abort the whole run with an
// infraError, never be flattened into a case error that would let a first-run
// without baseline silently pass green.
func TestLLMExecutorAgentInfraPropagates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "agent boom", http.StatusBadGateway)
	}))
	defer server.Close()
	ex := &llmExecutor{
		agents:  &agentClient{client: newHTTPClient(server.URL, "test-token")},
		agentID: "a1",
	}
	p := point{Kind: "agent", Dir: t.TempDir()}
	goldenPath := p.Dir + "/cases.yaml"
	writeTestFile(t, goldenPath, `
version: 1
cases:
  - id: c1
    query: "q"
    mode: contains
    expected_output: "x"
`)
	p.Golden = "cases.yaml"
	_, err := ex.runCases(context.Background(), mustLoadLLMSet(t, p))
	if err == nil || !isInfra(err) {
		t.Fatalf("agent 5xx must propagate as infraError, got %v", err)
	}
}

// TestLLMExecutorAgentResourceNotFoundAborts pins the system-acceptance gap:
// when a point references an agent that does not exist, the execute endpoint
// 404s. That is a broken setup — no case can pass — so the run aborts (fatal,
// exit 1), matching skill's fail-closed provisioning, instead of recording
// case errors that produce a silent 0%-pass green (exit 0) on a first run with
// no baseline. Partial cases before the abort are preserved for the report.
func TestLLMExecutorAgentResourceNotFoundAborts(t *testing.T) {
	var n int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"output":"ok"}`))
			return
		}
		http.Error(w, "agent not found", http.StatusNotFound)
	}))
	defer server.Close()
	ex := &llmExecutor{
		agents:  &agentClient{client: newHTTPClient(server.URL, "test-token")},
		agentID: "missing-agent",
	}
	p := point{Kind: "agent", Dir: t.TempDir()}
	goldenPath := p.Dir + "/cases.yaml"
	writeTestFile(t, goldenPath, `
version: 1
cases:
  - id: c1
    query: "q1"
    mode: contains
    expected_output: "ok"
  - id: c2
    query: "q2"
    mode: contains
    expected_output: "x"
`)
	p.Golden = "cases.yaml"
	res, err := ex.runCases(context.Background(), mustLoadLLMSet(t, p))
	if err == nil {
		t.Fatal("runCases must abort when the point's agent does not exist")
	}
	if !isResourceNotFound(err) {
		t.Fatalf("missing agent is a resource-not-found defect, got %v", err)
	}
	if isInfra(err) {
		t.Fatalf("missing agent is a dataset defect, not infrastructure: %v", err)
	}
	if got := classifyError(err); got != exitFailed {
		t.Fatalf("classifyError = %d, want exitFailed (%d)", got, exitFailed)
	}
	// The passing case before the 404 is preserved in the error report.
	if len(res.Cases) != 1 || !res.Cases[0].Passed {
		t.Fatalf("expected the passing case preserved, got %+v", res.Cases)
	}
}

// TestLLMExecutorJudgeInfraPropagates pins C1 for the judge path: a judge
// service outage (HTTP 500) aborts the run with an infraError. A judge "no"
// verdict is still a case outcome (covered by TestLLMExecutorJudgePath-style
// tests) — only infra failures abort.
func TestLLMExecutorJudgeInfraPropagates(t *testing.T) {
	agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":"output"}`))
	}))
	defer agentServer.Close()
	judgeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "judge down", http.StatusInternalServerError)
	}))
	defer judgeServer.Close()

	ex := &llmExecutor{
		agents:  &agentClient{client: newHTTPClient(agentServer.URL, "test-token")},
		agentID: "a1",
		judge:   newJudgeClient(&judgeConfig{BaseURL: judgeServer.URL, Model: "m"}, ""),
	}
	p := point{Kind: "agent", Dir: t.TempDir()}
	goldenPath := p.Dir + "/cases.yaml"
	writeTestFile(t, goldenPath, `
version: 1
cases:
  - id: j1
    query: "q"
    mode: judge
    judge_spec:
      criteria: "c"
`)
	p.Golden = "cases.yaml"
	_, err := ex.runCases(context.Background(), mustLoadLLMSet(t, p))
	if err == nil || !isInfra(err) {
		t.Fatalf("judge 5xx must propagate as infraError, got %v", err)
	}
}

func TestResolveAgentID(t *testing.T) {
	ex := &llmExecutor{}
	if id, err := ex.resolveAgentID(context.Background(), point{Snapshot: map[string]any{"id": "a1"}}); err != nil || id != "a1" {
		t.Fatalf("resolveAgentID = %q, %v; want a1", id, err)
	}
	if _, err := ex.resolveAgentID(context.Background(), point{Snapshot: map[string]any{}}); err == nil {
		t.Fatal("expected error when snapshot.id is missing")
	}
}

func TestLoadLLMSetValidation(t *testing.T) {
	dir := t.TempDir()
	// judge case without criteria is a dataset defect.
	writeTestFile(t, dir+"/bad.yaml", `
version: 1
cases:
  - id: j1
    query: "q"
    mode: judge
`)
	p := point{Dir: dir, Golden: "bad.yaml"}
	if _, err := loadLLMSet(p); err == nil || !strings.Contains(err.Error(), "judge_spec") {
		t.Fatalf("expected judge_spec.criteria error, got %v", err)
	}
	// assertion case without expected value is a dataset defect.
	writeTestFile(t, dir+"/bad2.yaml", `
version: 1
cases:
  - id: c1
    query: "q"
    mode: exact
`)
	p.Golden = "bad2.yaml"
	if _, err := loadLLMSet(p); err == nil || !strings.Contains(err.Error(), "expected") {
		t.Fatalf("expected expected-value error, got %v", err)
	}
	// unsupported mode is a dataset defect.
	writeTestFile(t, dir+"/bad3.yaml", `
version: 1
cases:
  - id: c1
    query: "q"
    mode: bogus
`)
	p.Golden = "bad3.yaml"
	if _, err := loadLLMSet(p); err == nil || !strings.Contains(err.Error(), "unsupported mode") {
		t.Fatalf("expected unsupported-mode error, got %v", err)
	}
}

func TestLLMFingerprintDeterministic(t *testing.T) {
	snapshot := map[string]any{"id": "a1", "model": "qwen-max", "tools": []any{"x", "y"}}
	first := llmFingerprint(snapshot)
	for i := 0; i < 5; i++ {
		if got := llmFingerprint(snapshot); got.Hash != first.Hash {
			t.Fatalf("fingerprint not deterministic: %s != %s", got.Hash, first.Hash)
		}
	}
	if first.Hash == "" {
		t.Fatal("fingerprint hash must not be empty")
	}
}

// TestLLMFingerprintSkillContentSensitive pins I3: the skill fingerprint must
// cover the skill's declared name/content plus the carrier agent model and
// system prompt, so changing any of them forces a re-decision instead of
// staying fingerprint-invisible.
func TestLLMFingerprintSkillContentSensitive(t *testing.T) {
	base := map[string]any{
		"skill": map[string]any{"name": "s", "content": "do A"},
		"agent": map[string]any{"model": "m", "system_prompt": "p"},
	}
	baseHash := llmFingerprint(base).Hash

	changedContent := map[string]any{
		"skill": map[string]any{"name": "s", "content": "do B"},
		"agent": map[string]any{"model": "m", "system_prompt": "p"},
	}
	if llmFingerprint(changedContent).Hash == baseHash {
		t.Fatal("changing skill content must change the fingerprint")
	}

	changedName := map[string]any{
		"skill": map[string]any{"name": "other", "content": "do A"},
		"agent": map[string]any{"model": "m", "system_prompt": "p"},
	}
	if llmFingerprint(changedName).Hash == baseHash {
		t.Fatal("changing skill name must change the fingerprint")
	}

	changedPrompt := map[string]any{
		"skill": map[string]any{"name": "s", "content": "do A"},
		"agent": map[string]any{"model": "m", "system_prompt": "p2"},
	}
	if llmFingerprint(changedPrompt).Hash == baseHash {
		t.Fatal("changing the carrier agent system_prompt must change the fingerprint")
	}

	changedModel := map[string]any{
		"skill": map[string]any{"name": "s", "content": "do A"},
		"agent": map[string]any{"model": "m2", "system_prompt": "p"},
	}
	if llmFingerprint(changedModel).Hash == baseHash {
		t.Fatal("changing the carrier agent model must change the fingerprint")
	}
}

// TestLLMFingerprintAgentSystemPromptSensitive pins I3 for agent points: the
// system prompt (instructions) participates in the fingerprint.
func TestLLMFingerprintAgentSystemPromptSensitive(t *testing.T) {
	base := map[string]any{"id": "a1", "model": "qwen-max", "system_prompt": "p1", "tools": []any{"x"}}
	changed := map[string]any{"id": "a1", "model": "qwen-max", "system_prompt": "p2", "tools": []any{"x"}}
	if llmFingerprint(base).Hash == llmFingerprint(changed).Hash {
		t.Fatal("changing agent system_prompt must change the fingerprint")
	}
}

func TestAgentClientExecuteAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/agents/a1/execute" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "want POST", http.StatusMethodNotAllowed)
			return
		}
		_, _ = w.Write([]byte(`{"output":"planning output"}`))
	}))
	defer server.Close()
	a := &agentClient{client: newHTTPClient(server.URL, "test-token")}
	out, err := a.executeAgent(context.Background(), "a1", "do it")
	if err != nil {
		t.Fatalf("executeAgent: %v", err)
	}
	if out != "planning output" {
		t.Fatalf("executeAgent result = %q, want server output field", out)
	}
}

// TestAgentClientExecuteAgentFailsClosedOnNonCompleted pins that an agent run
// that did not reach a real "completed" result (e.g. "waiting_approval", a 202
// with a pending approval, an explicit error, or a completed run with a null
// result) fails the case as a case error — never an infraError, and never a
// silent pass that a loose judge could grade against "null".
func TestAgentClientExecuteAgentFailsClosedOnNonCompleted(t *testing.T) {
	scenarios := []struct {
		name   string
		status int
		body   string
	}{
		{
			name:   "waiting approval status",
			status: http.StatusOK,
			body:   `{"output":""}`,
		},
		{
			name:   "202 accepted with pending approval",
			status: http.StatusAccepted,
			body:   `{"output":""}`,
		},
		{
			name:   "error status",
			status: http.StatusOK,
			body:   `{"error":"boom"}`,
		},
		{
			name:   "completed without result",
			status: http.StatusOK,
			body:   `{"output":""}`,
		},
	}
	for _, tc := range scenarios {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			a := &agentClient{client: newHTTPClient(server.URL, "test-token")}
			_, err := a.executeAgent(context.Background(), "a1", "do it")
			if err == nil {
				t.Fatal("executeAgent must fail when the agent did not complete")
			}
			if isInfra(err) {
				t.Fatalf("non-completed agent run is a case error, not infra: %v", err)
			}

			// The same failure must surface as a failed case with an error, so a
			// judge never sees a "null" output to grade.
			ex := &llmExecutor{
				agents:  &agentClient{client: newHTTPClient(server.URL, "test-token")},
				agentID: "a1",
			}
			p := point{Kind: "agent", Dir: t.TempDir()}
			goldenPath := p.Dir + "/cases.yaml"
			writeTestFile(t, goldenPath, `
version: 1
cases:
  - id: c1
    query: "q"
    mode: contains
    expected_output: "x"
`)
			p.Golden = "cases.yaml"
			res, err := ex.runCases(context.Background(), mustLoadLLMSet(t, p))
			if err != nil {
				t.Fatalf("runCases: %v", err)
			}
			if res.Cases[0].Passed || res.Cases[0].Error == "" {
				t.Fatalf("non-completed agent run must fail the case: %+v", res.Cases[0])
			}
		})
	}
}

func mustLoadLLMSet(t *testing.T, p point) goldenSet {
	t.Helper()
	set, err := loadLLMSet(p)
	if err != nil {
		t.Fatal(err)
	}
	return set
}
