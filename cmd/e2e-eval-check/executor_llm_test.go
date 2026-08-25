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
			_, _ = w.Write([]byte(`{"result":"planning output","status":"completed"}`))
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
		_, _ = w.Write([]byte(`{"result":"hello world","status":"completed"}`))
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
		_, _ = w.Write([]byte(`{"result":"output","status":"completed"}`))
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
	res, err := ex.runCases(context.Background(), mustLoadLLMSet(t, p))
	if err != nil {
		t.Fatalf("runCases: %v", err)
	}
	if res.Cases[0].Passed || res.Cases[0].Error == "" {
		t.Fatalf("agent execute failure must fail the case: %+v", res.Cases[0])
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
		_, _ = w.Write([]byte(`{"result":{"text":"nested object"},"status":"completed"}`))
	}))
	defer server.Close()
	a := &agentClient{client: newHTTPClient(server.URL, "test-token")}
	out, err := a.executeAgent(context.Background(), "a1", "do it")
	if err != nil {
		t.Fatalf("executeAgent: %v", err)
	}
	if out != `{"text":"nested object"}` {
		t.Fatalf("executeAgent result = %q, want serialized nested object", out)
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
