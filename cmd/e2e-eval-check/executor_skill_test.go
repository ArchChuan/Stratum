package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSkillExecutorCreatesAndRunsCarrierAgent pins the full skill flow: the
// executor provisions a temporary carrier agent, runs each case against it,
// then deletes it. The fake create-agent handler also asserts the real wire
// contract — camelCase llmModel/maxIterations/allowedSkills plus the required
// memoryScope — so a payload built against the wrong (proto-gen snake_case)
// DTO fails here instead of silently 400ing against the live server.
func TestSkillExecutorCreatesAndRunsCarrierAgent(t *testing.T) {
	var createdAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/agents":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, "bad create body", http.StatusBadRequest)
				return
			}
			if name, _ := payload["name"].(string); !strings.HasPrefix(name, "eval-carrier-") {
				http.Error(w, "bad create name", http.StatusBadRequest)
				return
			}
			if model, _ := payload["llmModel"].(string); model == "" {
				http.Error(w, "missing llmModel", http.StatusBadRequest)
				return
			}
			if scope, _ := payload["memoryScope"].(string); scope == "" {
				http.Error(w, "missing memoryScope", http.StatusBadRequest)
				return
			}
			maxIter, _ := payload["maxIterations"].(float64)
			if maxIter < 1 || maxIter > 90 {
				http.Error(w, "maxIterations out of [1,90]", http.StatusBadRequest)
				return
			}
			skills, _ := payload["allowedSkills"].([]any)
			if len(skills) != 1 || skills[0] != "my-skill" {
				http.Error(w, "allowedSkills must carry the skill name", http.StatusBadRequest)
				return
			}
			createdAgent = "carrier-1"
			_, _ = w.Write([]byte(`{"id":"carrier-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/agents/carrier-1/execute":
			_, _ = w.Write([]byte(`{"result":"skill applied","status":"completed"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/agents/carrier-1":
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	writeTestFile(t, dir+"/cases.yaml", `
version: 1
cases:
  - id: s1
    query: "apply the skill"
    mode: contains
    expected_output: "skill applied"
`)
	p := point{Kind: "skill", Key: "sk", Dir: dir, Golden: "cases.yaml", Baseline: "b.json",
		Snapshot: map[string]any{
			"skill": map[string]any{"name": "my-skill", "description": "d", "content": "c"},
			"agent": map[string]any{"model": "m", "system_prompt": "p"},
		}}
	o := options{kind: "skill", point: "sk", baseURL: server.URL}
	res, err := (&skillExecutor{client: newHTTPClient(server.URL, "test-token")}).Execute(context.Background(), o, p)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if createdAgent != "carrier-1" {
		t.Fatalf("carrier agent not created, got %q", createdAgent)
	}
	if len(res.Cases) != 1 || !res.Cases[0].Passed {
		t.Fatalf("expected contains match to pass: %+v", res.Cases)
	}
	if res.Aggregate.PassRate != 1 {
		t.Fatalf("pass_rate = %f", res.Aggregate.PassRate)
	}
	if len(res.Residuals) != 0 {
		t.Fatalf("carrier cleanup should not leave residuals: %v", res.Residuals)
	}
	if len(res.Evidence) != 1 || res.Evidence[0].Kind != "carrier_agent" || res.Evidence[0].Ref != "carrier-1" {
		t.Fatalf("expected carrier_agent evidence: %+v", res.Evidence)
	}
}

// TestSkillExecutorCleanupFailureSurfacesAsResidual pins that a failed carrier
// agent cleanup never fails the run: it surfaces as a residual for the report.
func TestSkillExecutorCleanupFailureSurfacesAsResidual(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/agents":
			_, _ = w.Write([]byte(`{"id":"carrier-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/agents/carrier-1/execute":
			_, _ = w.Write([]byte(`{"result":"skill applied","status":"completed"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/agents/carrier-1":
			http.Error(w, "delete failed", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	writeTestFile(t, dir+"/cases.yaml", `
version: 1
cases:
  - id: s1
    query: "apply the skill"
    mode: contains
    expected_output: "skill applied"
`)
	p := point{Kind: "skill", Key: "sk", Dir: dir, Golden: "cases.yaml", Baseline: "b.json",
		Snapshot: map[string]any{
			"skill": map[string]any{"name": "my-skill", "description": "d", "content": "c"},
			"agent": map[string]any{"model": "m", "system_prompt": "p"},
		}}
	o := options{kind: "skill", point: "sk", baseURL: server.URL}
	res, err := (&skillExecutor{client: newHTTPClient(server.URL, "test-token")}).Execute(context.Background(), o, p)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(res.Residuals) != 1 || res.Residuals[0] != "carrier-1" {
		t.Fatalf("delete failure must surface as residual, got %v", res.Residuals)
	}
	if res.Aggregate.PassRate != 1 {
		t.Fatalf("run must still pass despite cleanup failure, pass_rate = %f", res.Aggregate.PassRate)
	}
}

// TestCreateCarrierAgentRequiresSkillSnapshot pins the fail-closed guard: a
// skill point without a snapshot.skill map is a dataset defect, not a request
// to a server.
func TestCreateCarrierAgentRequiresSkillSnapshot(t *testing.T) {
	_, err := createCarrierAgent(context.Background(), newHTTPClient("http://unused", "t"), point{Key: "sk"})
	if err == nil || !strings.Contains(err.Error(), "snapshot.skill") {
		t.Fatalf("expected snapshot.skill error, got %v", err)
	}
}

// TestSkillDatasetValidation pins that skill datasets share the agent loader:
// a judge case without judge_spec.criteria must fail loudly at load time.
func TestSkillDatasetValidation(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir+"/cases.yaml", `
version: 1
cases:
  - id: s1
    query: "apply the skill"
    mode: judge
`)
	p := point{Kind: "skill", Dir: dir, Golden: "cases.yaml"}
	if _, err := loadLLMSet(p); err == nil {
		t.Fatal("expected validation error for judge case without judge_spec.criteria")
	}
}
