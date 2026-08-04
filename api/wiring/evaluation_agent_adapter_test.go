package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	evalapp "github.com/byteBuilderX/stratum/internal/evaluation/application"
	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	evalport "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

func TestAgentEvaluationAdapterRequiresPublishedTenantRevision(t *testing.T) {
	revisions := &fakeAgentRevisionService{revision: evaldomain.ResourceRevision{
		ID: "rev-1", ResourceKind: evaldomain.ResourceKindAgent, ResourceID: "agent-1",
		Status: evaldomain.RevisionStatusDraft,
	}, payload: []byte(`{"agent_id":"agent-1","type":"react","system_prompt":"baseline","model":"qwen-plus","max_iterations":5}`), found: true}
	adapter := agentEvaluationAdapter{revisions: revisions}
	_, err := adapter.LoadOptimizableSnapshot(context.Background(), "tenant-1", agentRef("rev-1"))
	if err == nil {
		t.Fatal("expected draft baseline rejection")
	}
	if revisions.tenantID != "tenant-1" {
		t.Fatalf("tenant not propagated: %q", revisions.tenantID)
	}
}

func TestAgentEvaluationAdapterCandidateIsIdempotentAndBounded(t *testing.T) {
	revisions := &fakeAgentRevisionService{revision: evaldomain.ResourceRevision{
		ID: "published-1", ResourceKind: evaldomain.ResourceKindAgent, ResourceID: "agent-1",
		Status: evaldomain.RevisionStatusPublished,
	}, payload: []byte(`{"agent_id":"agent-1","type":"react","system_prompt":"baseline","model":"qwen-plus","max_iterations":5,"bindings":[{"kind":"skill","id":"skill-1","enabled":true}]}`), found: true}
	adapter := agentEvaluationAdapter{revisions: revisions, actorID: "evaluation-worker"}
	patch := evaldomain.CandidatePatch{Source: "llm_rewrite", PromptPatch: map[string]any{"instructions": "candidate"}, ParameterPatch: map[string]any{
		"bindings": map[string]any{"skill:skill-1": false},
	}}
	first, err := adapter.CreateCandidate(context.Background(), "tenant-1", agentRef("published-1"), patch)
	if err != nil {
		t.Fatal(err)
	}
	second, err := adapter.CreateCandidate(context.Background(), "tenant-1", agentRef("published-1"), patch)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || revisions.createCalls != 2 || !strings.HasPrefix(revisions.input.IdempotencyKey, "agent-candidate-") {
		t.Fatalf("candidate replay mismatch: first=%#v second=%#v calls=%d", first, second, revisions.createCalls)
	}

	patch.ParameterPatch["bindings"] = map[string]any{"skill:skill-2": true}
	if _, err := adapter.CreateCandidate(context.Background(), "tenant-1", agentRef("published-1"), patch); err == nil {
		t.Fatal("expected unauthorized binding rejection")
	}
}

func TestAgentEvaluationAdapterPropagatesRevisionPersistenceFailure(t *testing.T) {
	wantErr := errors.New("object persistence failed")
	revisions := &fakeAgentRevisionService{revision: evaldomain.ResourceRevision{
		ID: "published-1", ResourceKind: evaldomain.ResourceKindAgent, ResourceID: "agent-1",
		Status: evaldomain.RevisionStatusPublished,
	}, payload: []byte(`{"agent_id":"agent-1","type":"react","system_prompt":"baseline","model":"qwen-plus","max_iterations":5}`), found: true, createErr: wantErr}
	adapter := agentEvaluationAdapter{revisions: revisions, actorID: "evaluation-worker"}
	_, err := adapter.CreateCandidate(context.Background(), "tenant-1", agentRef("published-1"), evaldomain.CandidatePatch{
		Source: "llm_rewrite", PromptPatch: map[string]any{"instructions": "candidate"},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected persistence failure, got %v", err)
	}
}

func TestAgentEvaluationAdapterTreatsProviderFailureAsExecutionFailure(t *testing.T) {
	wantErr := errors.New("provider unavailable")
	revisions := &fakeAgentRevisionService{revision: evaldomain.ResourceRevision{
		ID: "published-1", ResourceKind: evaldomain.ResourceKindAgent, ResourceID: "agent-1",
		Status: evaldomain.RevisionStatusPublished,
	}, payload: []byte(`{"agent_id":"agent-1","type":"react","system_prompt":"baseline","model":"qwen-plus","max_iterations":5}`), found: true}
	adapter := agentEvaluationAdapter{revisions: revisions, agents: fakeAgentRevisionExecutor{err: wantErr}}
	result, err := adapter.ExecuteRevision(
		context.Background(), "tenant-1", "user-1", agentRef("published-1"), evaldomain.EvalCase{Input: "hello"},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected provider failure, got result=%#v err=%v", result, err)
	}
}

func TestAgentEvaluationAdapterCrossTenantRevisionIsNotFound(t *testing.T) {
	adapter := agentEvaluationAdapter{revisions: &fakeAgentRevisionService{found: false}}
	_, err := adapter.ResolveRevision(context.Background(), "other-tenant", agentRef("published-1"))
	if !errors.Is(err, evalport.ErrCenterResourceNotFound) {
		t.Fatalf("expected tenant-safe not found, got %v", err)
	}
}

func TestAgentEvaluationAdapterRejectsDraftExecution(t *testing.T) {
	revisions := &fakeAgentRevisionService{revision: evaldomain.ResourceRevision{
		ID: "draft-1", ResourceKind: evaldomain.ResourceKindAgent, ResourceID: "agent-1",
		Status: evaldomain.RevisionStatusDraft,
	}, payload: []byte(`{"agent_id":"agent-1","type":"react","system_prompt":"baseline","model":"qwen-plus","max_iterations":5}`), found: true}
	adapter := agentEvaluationAdapter{revisions: revisions, agents: fakeAgentRevisionExecutor{}}
	_, err := adapter.ExecuteRevision(
		context.Background(), "tenant-1", "user-1", agentRef("draft-1"), evaldomain.EvalCase{Input: "hello"},
	)
	if !errors.Is(err, evaldomain.ErrRevisionNotPublished) {
		t.Fatalf("expected not-published error, got %v", err)
	}
}

func TestAgentEvaluationAdapterResolvesOptimizationCandidateForOfflineEvaluation(t *testing.T) {
	revisions := &fakeAgentRevisionService{revision: evaldomain.ResourceRevision{
		ID: "candidate-1", ResourceKind: evaldomain.ResourceKindAgent, ResourceID: "agent-1",
		Status: evaldomain.RevisionStatusDraft, Source: evaldomain.RevisionSourceOptimization,
	}, payload: []byte(`{"agent_id":"agent-1","type":"react","system_prompt":"candidate","model":"qwen-plus","max_iterations":5}`), found: true}

	resolved, err := (agentEvaluationAdapter{revisions: revisions}).ResolveRevision(
		context.Background(), "tenant-1", agentRef("candidate-1"),
	)
	if err != nil || !resolved.CanEvaluateOffline() {
		t.Fatalf("candidate resolution=%+v err=%v", resolved, err)
	}
}

func TestAgentEvaluationAdapterCreatesPublishedBaselineFromLiveAgent(t *testing.T) {
	revisions := &fakeAgentRevisionService{}
	agents := fakeAgentRevisionExecutor{snapshot: agentdomain.AgentRevision{
		AgentID: "agent-1", Type: agentdomain.ReActAgent, SystemPrompt: "baseline", Model: "qwen-plus",
		MaxIterations: 5,
	}}
	adapter := agentEvaluationAdapter{revisions: revisions, agents: agents, actorID: "evaluation-worker"}
	ref, err := adapter.CreatePublishedBaseline(context.Background(), "tenant-1", "agent-1")
	if err != nil || ref.RevisionID != "candidate-1" || revisions.publishCalls != 1 {
		t.Fatalf("unexpected baseline: ref=%+v publishCalls=%d err=%v", ref, revisions.publishCalls, err)
	}
}

func TestAgentEvaluationAdapterDoesNotPublishFailedBaselinePersistence(t *testing.T) {
	wantErr := errors.New("object persistence failed")
	revisions := &fakeAgentRevisionService{createErr: wantErr}
	agents := fakeAgentRevisionExecutor{snapshot: agentdomain.AgentRevision{
		AgentID: "agent-1", Type: agentdomain.ReActAgent, SystemPrompt: "baseline", Model: "qwen-plus",
		MaxIterations: 5,
	}}
	adapter := agentEvaluationAdapter{revisions: revisions, agents: agents}
	_, err := adapter.CreatePublishedBaseline(context.Background(), "tenant-1", "agent-1")
	if !errors.Is(err, wantErr) || revisions.publishCalls != 0 {
		t.Fatalf("failed persistence must abort publish: calls=%d err=%v", revisions.publishCalls, err)
	}
}

func TestAgentEvaluationAdapterParsesModelParameters(t *testing.T) {
	baseline := agentdomain.AgentRevision{AgentID: "agent-1", Type: agentdomain.ReActAgent,
		SystemPrompt: "baseline", Model: "qwen-plus", MaxIterations: 5}
	t.Run("accepts temperature and max_tokens", func(t *testing.T) {
		parsed, err := parseAgentCandidatePatch(baseline, evaldomain.CandidatePatch{
			ParameterPatch: map[string]any{"temperature": 0.9, "max_tokens": 2048},
		})
		if err != nil {
			t.Fatalf("expected supported parameters to be accepted: %v", err)
		}
		if parsed.ModelParameters == nil ||
			parsed.ModelParameters.Temperature != 0.9 || parsed.ModelParameters.MaxTokens != 2048 {
			t.Fatalf("parameters not written back: %#v", parsed.ModelParameters)
		}
	})
	t.Run("rejects unknown parameter fields", func(t *testing.T) {
		for _, field := range []string{"maxTokens", "top_p"} {
			_, err := parseAgentCandidatePatch(baseline, evaldomain.CandidatePatch{
				ParameterPatch: map[string]any{field: 1},
			})
			if err == nil {
				t.Fatalf("expected unsupported %s to be rejected", field)
			}
		}
	})
	t.Run("rejects non-numeric temperature and max_tokens", func(t *testing.T) {
		for _, field := range []string{"temperature", "max_tokens"} {
			_, err := parseAgentCandidatePatch(baseline, evaldomain.CandidatePatch{
				ParameterPatch: map[string]any{field: "hot"},
			})
			if err == nil {
				t.Fatalf("expected non-numeric %s to be rejected", field)
			}
		}
	})
	t.Run("accepts compaction keys", func(t *testing.T) {
		parsed, err := parseAgentCandidatePatch(baseline, evaldomain.CandidatePatch{
			ParameterPatch: map[string]any{
				"max_context_tokens":       16384,
				"compaction_recent_groups": 5,
				"compaction_safety_ratio":  0.85,
			},
		})
		if err != nil {
			t.Fatalf("expected compaction parameters to be accepted: %v", err)
		}
		if parsed.ModelParameters == nil ||
			parsed.ModelParameters.MaxContextTokens != 16384 ||
			parsed.ModelParameters.CompactionRecentGroups != 5 ||
			parsed.ModelParameters.CompactionSafetyRatio != 0.85 {
			t.Fatalf("compaction parameters not written back: %#v", parsed.ModelParameters)
		}
	})
	t.Run("rejects invalid compaction values", func(t *testing.T) {
		for _, patch := range []map[string]any{
			{"compaction_recent_groups": 4},   // outside {0,2,3,5}
			{"compaction_recent_groups": "3"}, // non-integer
			{"compaction_safety_ratio": 0.3},  // below 0.5
			{"compaction_safety_ratio": 1.2},  // above 0.95
			{"max_context_tokens": -1},        // negative window
			{"max_context_tokens": 40000},     // above 32768
		} {
			_, err := parseAgentCandidatePatch(baseline, evaldomain.CandidatePatch{
				ParameterPatch: patch,
			})
			if err == nil {
				t.Fatalf("expected patch %v to be rejected", patch)
			}
		}
	})
	t.Run("accepts zero compaction values as auto", func(t *testing.T) {
		parsed, err := parseAgentCandidatePatch(baseline, evaldomain.CandidatePatch{
			ParameterPatch: map[string]any{
				"compaction_recent_groups": 0,
				"compaction_safety_ratio":  0,
			},
		})
		if err != nil {
			t.Fatalf("expected zero/auto values to be accepted: %v", err)
		}
		if parsed.ModelParameters.CompactionRecentGroups != 0 || parsed.ModelParameters.CompactionSafetyRatio != 0 {
			t.Fatalf("auto values not written back: %#v", parsed.ModelParameters)
		}
	})
}

func TestAgentEvaluationAdapterSummariesPassRealRevisionValidation(t *testing.T) {
	store := &validatingRevisionStore{}
	repo := &validatingRevisionRepo{}
	revisions := evalapp.NewRevisionService(store, repo)
	agents := fakeAgentRevisionExecutor{snapshot: agentdomain.AgentRevision{
		AgentID: "agent-1", Type: agentdomain.ReActAgent, SystemPrompt: "baseline", Model: "qwen-plus",
		MaxIterations: 5,
	}}
	adapter := agentEvaluationAdapter{revisions: revisions, agents: agents}
	baseline, err := adapter.CreatePublishedBaseline(context.Background(), "tenant-1", "agent-1")
	if err != nil {
		t.Fatalf("baseline rejected by real RevisionService: %v", err)
	}
	if _, err := adapter.CreateCandidate(context.Background(), "tenant-1", baseline, evaldomain.CandidatePatch{
		PromptPatch: map[string]any{"instructions": "candidate"},
	}); err != nil {
		t.Fatalf("candidate rejected by real RevisionService: %v", err)
	}
}

func TestAgentEvaluationAdapterApplyPublishedRevisionFailsClosedOnModelValidation(t *testing.T) {
	payload := []byte(`{"agent_id":"agent-1","type":"react","system_prompt":"baseline","model":"qwen-plus","max_iterations":5,"model_parameters":{"temperature":0.9,"max_tokens":2048}}`)
	revisions := &fakeAgentRevisionService{revision: evaldomain.ResourceRevision{
		ID: "published-1", ResourceKind: evaldomain.ResourceKindAgent, ResourceID: "agent-1",
		Status: evaldomain.RevisionStatusPublished,
	}, payload: payload, found: true}

	t.Run("missing validator blocks apply", func(t *testing.T) {
		updater := &recordingAgentUpdater{}
		adapter := agentEvaluationAdapter{revisions: revisions, agentUpdater: updater}
		if err := adapter.ApplyPublishedRevision(context.Background(), "tenant-1", "agent-1", "published-1"); err == nil {
			t.Fatal("expected missing validator to fail closed")
		}
		if updater.updateCalls != 0 {
			t.Fatalf("apply proceeded without validator: updateCalls=%d", updater.updateCalls)
		}
	})

	t.Run("validator dependency failure fails closed", func(t *testing.T) {
		wantErr := errors.New("model registry unavailable")
		updater := &recordingAgentUpdater{}
		adapter := agentEvaluationAdapter{
			revisions: revisions, agentUpdater: updater,
			modelValidator: fakeModelValidator{err: wantErr},
		}
		err := adapter.ApplyPublishedRevision(context.Background(), "tenant-1", "agent-1", "published-1")
		if !errors.Is(err, wantErr) {
			t.Fatalf("expected validator failure, got %v", err)
		}
		if updater.updateCalls != 0 {
			t.Fatalf("apply proceeded despite validator failure: updateCalls=%d", updater.updateCalls)
		}
	})

	t.Run("valid model applies with routed parameters", func(t *testing.T) {
		updater := &recordingAgentUpdater{}
		adapter := agentEvaluationAdapter{
			revisions: revisions, agentUpdater: updater,
			modelValidator: fakeModelValidator{},
		}
		if err := adapter.ApplyPublishedRevision(context.Background(), "tenant-1", "agent-1", "published-1"); err != nil {
			t.Fatal(err)
		}
		if updater.updateCalls != 1 {
			t.Fatalf("expected exactly one update, got %d", updater.updateCalls)
		}
		if updater.input.Temperature != 0.9 || updater.input.MaxTokens != 2048 {
			t.Fatalf("routed parameters not applied: %#v", updater.input)
		}
	})
}

func TestAgentEvaluationAdapterCreateCandidateValidatesPatchedModel(t *testing.T) {
	revisions := &fakeAgentRevisionService{revision: evaldomain.ResourceRevision{
		ID: "published-1", ResourceKind: evaldomain.ResourceKindAgent, ResourceID: "agent-1",
		Status: evaldomain.RevisionStatusPublished,
	}, payload: []byte(`{"agent_id":"agent-1","type":"react","system_prompt":"baseline","model":"qwen-plus","max_iterations":5}`), found: true}
	adapter := agentEvaluationAdapter{
		revisions: revisions, actorID: "evaluation-worker",
		modelValidator: fakeModelValidator{err: errors.New("model not in tenant catalog")},
	}
	_, err := adapter.CreateCandidate(context.Background(), "tenant-1", agentRef("published-1"), evaldomain.CandidatePatch{
		Source: "llm_rewrite", ParameterPatch: map[string]any{"model": "other-model"},
	})
	if err == nil {
		t.Fatal("expected patched model to be validated and rejected")
	}
}

type recordingAgentUpdater struct {
	updateCalls int
	input       agentapp.UpdateAgentInput
}

func (u *recordingAgentUpdater) Get(context.Context, string) (agentapp.AgentDTO, error) {
	return agentapp.AgentDTO{}, nil
}

func (u *recordingAgentUpdater) Update(_ context.Context, _ string, input agentapp.UpdateAgentInput) (agentapp.AgentDTO, error) {
	u.updateCalls++
	u.input = input
	return agentapp.AgentDTO{}, nil
}

type fakeModelValidator struct{ err error }

func (f fakeModelValidator) ValidateTenantChatModel(context.Context, string, string) error {
	return f.err
}

func TestAgentEvaluationAdapterExecutionReceivesTenantContext(t *testing.T) {
	revisions := &fakeAgentRevisionService{revision: evaldomain.ResourceRevision{
		ID: "published-1", ResourceKind: evaldomain.ResourceKindAgent, ResourceID: "agent-1",
		Status: evaldomain.RevisionStatusPublished,
	}, payload: []byte(`{"agent_id":"agent-1","type":"react","system_prompt":"baseline","model":"qwen-plus","max_iterations":5}`), found: true}
	executor := &tenantCaptureAgentExecutor{}
	adapter := agentEvaluationAdapter{revisions: revisions, agents: executor}
	_, _ = adapter.ExecuteRevision(
		context.Background(), "tenant-1", "user-1", agentRef("published-1"), evaldomain.EvalCase{Input: "hello"},
	)
	if executor.tenantID != "tenant-1" || executor.userID != "user-1" {
		t.Fatalf("execution tenant context = tenant %q user %q", executor.tenantID, executor.userID)
	}
}

type tenantCaptureAgentExecutor struct{ tenantID, userID string }

func (e *tenantCaptureAgentExecutor) SnapshotRevision(context.Context, string, string) (agentdomain.AgentRevision, error) {
	return agentdomain.AgentRevision{}, nil
}

func (e *tenantCaptureAgentExecutor) ExecuteRevision(
	ctx context.Context, _ agentdomain.AgentRevision, _ agentapp.ExecRequest, _ agentapp.ExecMeta,
) (*agentapp.AgentResult, int, error) {
	tenant, _ := postgres.FromContext(ctx)
	if tenant != nil {
		e.tenantID = tenant.TenantID
		e.userID = tenant.UserID
	}
	return &agentapp.AgentResult{Output: "ok"}, 1, nil
}

type validatingRevisionStore struct{ payloads map[string][]byte }

func (s *validatingRevisionStore) Put(_ context.Context, payload evalport.RevisionPayload) (evalport.RevisionPayloadRef, error) {
	encoded, _ := json.Marshal(payload.Value)
	if s.payloads == nil {
		s.payloads = map[string][]byte{}
	}
	s.payloads[payload.ID] = encoded
	return evalport.RevisionPayloadRef{URI: "object://" + payload.ID, SHA256: "hash"}, nil
}
func (s *validatingRevisionStore) Get(_ context.Context, ref evalport.RevisionPayloadRef) ([]byte, error) {
	return s.payloads[strings.TrimPrefix(ref.URI, "object://")], nil
}
func (*validatingRevisionStore) Delete(context.Context, evalport.RevisionPayloadRef) error {
	return nil
}

type validatingRevisionRepo struct {
	revisions map[string]evaldomain.ResourceRevision
}

func (r *validatingRevisionRepo) Create(_ context.Context, _ string, revision evaldomain.ResourceRevision, _ string) (evaldomain.ResourceRevision, bool, error) {
	if r.revisions == nil {
		r.revisions = map[string]evaldomain.ResourceRevision{}
	}
	r.revisions[revision.ID] = revision
	return revision, true, nil
}
func (r *validatingRevisionRepo) Get(_ context.Context, _ string, ref evaldomain.ResourceRef) (evaldomain.ResourceRevision, bool, error) {
	revision, ok := r.revisions[ref.RevisionID]
	return revision, ok, nil
}
func (r *validatingRevisionRepo) Publish(_ context.Context, _ string, ref evaldomain.ResourceRef) (evaldomain.ResourceRevision, error) {
	revision, ok := r.revisions[ref.RevisionID]
	if !ok {
		return evaldomain.ResourceRevision{}, evalport.ErrCenterResourceNotFound
	}
	revision.Status = evaldomain.RevisionStatusPublished
	r.revisions[ref.RevisionID] = revision
	return revision, nil
}

type fakeAgentRevisionService struct {
	revision     evaldomain.ResourceRevision
	payload      []byte
	found        bool
	tenantID     string
	input        evalport.CreateRevisionInput
	createCalls  int
	createErr    error
	publishCalls int
}

func (f *fakeAgentRevisionService) Publish(
	_ context.Context, _ string, ref evaldomain.ResourceRef,
) (evaldomain.ResourceRevision, error) {
	f.publishCalls++
	return evaldomain.ResourceRevision{ID: ref.RevisionID, ResourceKind: ref.Kind,
		ResourceID: ref.ResourceID, Status: evaldomain.RevisionStatusPublished}, nil
}

type fakeAgentRevisionExecutor struct {
	err      error
	snapshot agentdomain.AgentRevision
}

func (f fakeAgentRevisionExecutor) SnapshotRevision(
	context.Context, string, string,
) (agentdomain.AgentRevision, error) {
	return f.snapshot, f.err
}

func (f fakeAgentRevisionExecutor) ExecuteRevision(
	context.Context, agentdomain.AgentRevision, agentapp.ExecRequest, agentapp.ExecMeta,
) (*agentapp.AgentResult, int, error) {
	return nil, 0, f.err
}

func (f *fakeAgentRevisionService) Get(_ context.Context, tenantID string, _ evaldomain.ResourceRef) (evaldomain.ResourceRevision, []byte, bool, error) {
	f.tenantID = tenantID
	return f.revision, f.payload, f.found, nil
}

func (f *fakeAgentRevisionService) Create(_ context.Context, tenantID string, input evalport.CreateRevisionInput) (evaldomain.ResourceRevision, bool, error) {
	f.tenantID, f.input = tenantID, input
	f.createCalls++
	if f.createErr != nil {
		return evaldomain.ResourceRevision{}, false, f.createErr
	}
	return evaldomain.ResourceRevision{ID: "candidate-1", ResourceKind: evaldomain.ResourceKindAgent, ResourceID: "agent-1"}, f.createCalls == 1, nil
}

func agentRef(revisionID string) evaldomain.ResourceRef {
	return evaldomain.ResourceRef{Kind: evaldomain.ResourceKindAgent, ResourceID: "agent-1", RevisionID: revisionID}
}

var _ = agentdomain.AgentRevision{}
