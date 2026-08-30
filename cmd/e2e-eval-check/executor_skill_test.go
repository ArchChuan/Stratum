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
		case r.Method == http.MethodGet && r.URL.Path == "/skills":
			_, _ = w.Write([]byte(`{"skills":[{"id":"skill-1","name":"my-skill"}]}`))
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
			if len(skills) != 1 || skills[0] != "skill-1" {
				http.Error(w, "allowedSkills must carry the resolved skill ID", http.StatusBadRequest)
				return
			}
			createdAgent = "carrier-1"
			_, _ = w.Write([]byte(`{"id":"carrier-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/agents/carrier-1/execute":
			_, _ = w.Write([]byte(`{"output":"skill applied"}`))
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
		case r.Method == http.MethodGet && r.URL.Path == "/skills":
			_, _ = w.Write([]byte(`{"skills":[{"id":"skill-1","name":"my-skill"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/agents":
			_, _ = w.Write([]byte(`{"id":"carrier-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/agents/carrier-1/execute":
			_, _ = w.Write([]byte(`{"output":"skill applied"}`))
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
// skill point without a snapshot.skill map, or whose skill carries no usable
// name, is a dataset defect, not a request to a server. An empty name would
// send allowedSkills:[""], which the real server accepts — creating a carrier
// agent without the skill and false-passing the eval.
func TestCreateCarrierAgentRequiresSkillSnapshot(t *testing.T) {
	tests := []struct {
		name     string
		snapshot map[string]any
		wantErr  string
	}{
		{name: "missing skill snapshot", snapshot: nil, wantErr: "snapshot.skill"},
		{
			name:     "skill without name",
			snapshot: map[string]any{"skill": map[string]any{"description": "d"}},
			wantErr:  "snapshot.skill.name",
		},
		{
			name:     "skill name not a string",
			snapshot: map[string]any{"skill": map[string]any{"name": 42}},
			wantErr:  "snapshot.skill.name",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := createCarrierAgent(
				context.Background(), newHTTPClient("http://unused", "t"), point{Key: "sk", Snapshot: tc.snapshot})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// TestResolveSkillIDFailClosed pins the name->ID resolution guard: a skill
// name absent from the tenant catalog (or matching multiple skills) must abort
// the run as an infra defect, never silently attach a skill the agent cannot
// actually use.
func TestResolveSkillIDFailClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/skills" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"skills":[{"id":"a","name":"alpha"},{"id":"b","name":"alpha"}]}`))
	}))
	defer server.Close()

	client := newHTTPClient(server.URL, "test-token")
	if _, err := resolveSkillID(context.Background(), client, "missing"); err == nil {
		t.Fatal("expected infra error for missing skill name, got nil")
	}
	if _, err := resolveSkillID(context.Background(), client, "alpha"); err == nil {
		t.Fatal("expected infra error for ambiguous skill name, got nil")
	}
	id, err := resolveSkillID(context.Background(), client, "alpha")
	if err == nil && id != "" {
		// not reached: alpha is ambiguous
		t.Fatalf("ambiguous resolve must fail, got id %q", id)
	}
}

// TestCreateCarrierAgentRequiresAgentConfig pins the fail-closed guard for the
// carrier agent config: a missing agent snapshot or model would marshal
// llmModel:null and be rejected by the server with a generic binding 400, so
// surface it as a dataset defect with a clear message.
func TestCreateCarrierAgentRequiresAgentConfig(t *testing.T) {
	tests := []struct {
		name     string
		snapshot map[string]any
		wantErr  string
	}{
		{
			name:     "missing agent snapshot",
			snapshot: map[string]any{"skill": map[string]any{"name": "s"}},
			wantErr:  "snapshot.agent",
		},
		{
			name:     "agent without model",
			snapshot: map[string]any{"skill": map[string]any{"name": "s"}, "agent": map[string]any{"system_prompt": "p"}},
			wantErr:  "snapshot.agent.model",
		},
		{
			name:     "agent model not a string",
			snapshot: map[string]any{"skill": map[string]any{"name": "s"}, "agent": map[string]any{"model": 7}},
			wantErr:  "snapshot.agent.model",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := createCarrierAgent(
				context.Background(), newHTTPClient("http://unused", "t"), point{Key: "sk", Snapshot: tc.snapshot})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// TestSkillExecutorRunErrorStillDeletesCarrierAgent pins that the transient
// carrier agent is cleaned up even when execution aborts after creation (here,
// an invalid empty dataset). The deferred delete must not be skipped on the
// error path, and a failed DELETE on that path still records a residual so
// orphans stay visible to the report.
func TestSkillExecutorRunErrorStillDeletesCarrierAgent(t *testing.T) {
	tests := []struct {
		name          string
		deleteStatus  int
		wantResiduals []string
	}{
		{name: "delete succeeds on run error", deleteStatus: http.StatusOK, wantResiduals: nil},
		{
			name:          "delete failure on run error records residual",
			deleteStatus:  http.StatusInternalServerError,
			wantResiduals: []string{"carrier-1"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var deletes int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/skills":
					_, _ = w.Write([]byte(`{"skills":[{"id":"skill-1","name":"my-skill"}]}`))
				case r.Method == http.MethodPost && r.URL.Path == "/agents":
					_, _ = w.Write([]byte(`{"id":"carrier-1"}`))
				case r.Method == http.MethodDelete && r.URL.Path == "/agents/carrier-1":
					deletes++
					if tc.deleteStatus != http.StatusOK {
						http.Error(w, "delete failed", tc.deleteStatus)
						return
					}
					_, _ = w.Write([]byte(`{}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			dir := t.TempDir()
			writeTestFile(t, dir+"/cases.yaml", "version: 1\ncases: []\n")
			p := point{Kind: "skill", Key: "sk", Dir: dir, Golden: "cases.yaml", Baseline: "b.json",
				Snapshot: map[string]any{
					"skill": map[string]any{"name": "my-skill", "description": "d", "content": "c"},
					"agent": map[string]any{"model": "m", "system_prompt": "p"},
				}}
			o := options{kind: "skill", point: "sk", baseURL: server.URL}
			res, err := (&skillExecutor{client: newHTTPClient(server.URL, "test-token")}).Execute(context.Background(), o, p)
			if err == nil {
				t.Fatal("expected run to fail on empty dataset")
			}
			if deletes != 1 {
				t.Fatalf("carrier agent must be deleted once on the error path, deletes = %d", deletes)
			}
			if len(res.Evidence) != 1 || res.Evidence[0].Kind != "carrier_agent" || res.Evidence[0].Ref != "carrier-1" {
				t.Fatalf("expected carrier_agent evidence on the error path: %+v", res.Evidence)
			}
			if len(res.Residuals) != len(tc.wantResiduals) {
				t.Fatalf("residuals = %v, want %v", res.Residuals, tc.wantResiduals)
			}
			for i := range tc.wantResiduals {
				if res.Residuals[i] != tc.wantResiduals[i] {
					t.Fatalf("residuals = %v, want %v", res.Residuals, tc.wantResiduals)
				}
			}
		})
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
