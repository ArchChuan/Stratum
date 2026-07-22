package wiring

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	evalport "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
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
	result, err := adapter.ExecuteRevision(context.Background(), "tenant-1", agentRef("published-1"), evaldomain.EvalCase{Input: "hello"})
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
	_, err := adapter.ExecuteRevision(context.Background(), "tenant-1", agentRef("draft-1"), evaldomain.EvalCase{Input: "hello"})
	if !errors.Is(err, evaldomain.ErrRevisionNotPublished) {
		t.Fatalf("expected not-published error, got %v", err)
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

func TestAgentEvaluationAdapterRejectsUnsupportedModelParameters(t *testing.T) {
	baseline := agentdomain.AgentRevision{AgentID: "agent-1", Type: agentdomain.ReActAgent,
		SystemPrompt: "baseline", Model: "qwen-plus", MaxIterations: 5}
	for _, field := range []string{"temperature", "maxTokens"} {
		_, err := parseAgentCandidatePatch(baseline, evaldomain.CandidatePatch{ParameterPatch: map[string]any{field: 1}})
		if err == nil {
			t.Fatalf("expected unsupported %s to be rejected", field)
		}
	}
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
