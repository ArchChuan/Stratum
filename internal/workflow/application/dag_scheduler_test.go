package application_test

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/workflow/application"
	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/internal/workflow/domain/port"
	"github.com/stretchr/testify/require"
)

type dagStore struct {
	*memoryStore
	events    []domain.Event
	approvals []domain.Approval
	effects   []domain.EffectIntent
}

type startCheckpointStore struct {
	*dagStore
	runEvents []string
}

func (s *startCheckpointStore) CheckpointRun(ctx context.Context, tenantID string, run *domain.Run, event domain.Event) error {
	s.runEvents = append(s.runEvents, event.Type)
	if err := s.UpdateRun(ctx, tenantID, run); err != nil {
		return err
	}
	_, err := s.AppendEvent(ctx, tenantID, event)
	return err
}
func (s *startCheckpointStore) CheckpointAttempt(ctx context.Context, tenantID string, attempt domain.NodeAttempt, event domain.Event) error {
	if err := s.SaveAttempt(ctx, tenantID, attempt); err != nil {
		return err
	}
	_, err := s.AppendEvent(ctx, tenantID, event)
	return err
}

func (s *dagStore) CreateApproval(_ context.Context, _ string, approval *domain.Approval, event domain.Event) error {
	s.approvals = append(s.approvals, *approval)
	if run := s.runs[approval.RunID]; run != nil {
		run.Status, run.PauseReason, run.Generation = domain.RunStatusPaused, approval.Reason, approval.RunGeneration
	}
	_, _ = s.AppendEvent(context.Background(), "", event)
	return nil
}
func (s *dagStore) ListApprovals(_ context.Context, _, runID string, pendingOnly bool) ([]domain.Approval, error) {
	var out []domain.Approval
	for _, a := range s.approvals {
		if a.RunID == runID && (!pendingOnly || a.Status == domain.ApprovalStatusPending) {
			out = append(out, a)
		}
	}
	return out, nil
}
func (s *dagStore) CreateEffectIntent(_ context.Context, _ string, intent *domain.EffectIntent) error {
	s.effects = append(s.effects, *intent)
	return nil
}
func (s *dagStore) StartExternalEffect(_ context.Context, _ string, intent *domain.EffectIntent, _ string, generation int64) error {
	intent.RunGeneration = generation
	intent.Status = domain.EffectIntentStatusStarted
	s.effects = append(s.effects, *intent)
	return nil
}
func (s *dagStore) UpdateEffectIntent(_ context.Context, _ string, intent *domain.EffectIntent, expected domain.EffectIntentStatus) error {
	for i := range s.effects {
		if s.effects[i].ID == intent.ID {
			if s.effects[i].Status != expected {
				return domain.ErrFenceConflict
			}
			s.effects[i] = *intent
			return nil
		}
	}
	return domain.ErrNotFound
}
func (s *dagStore) ListEffectIntents(_ context.Context, _, runID string) ([]domain.EffectIntent, error) {
	var out []domain.EffectIntent
	for _, i := range s.effects {
		if i.RunID == runID {
			out = append(out, i)
		}
	}
	return out, nil
}
func (s *dagStore) ControlRun(_ context.Context, _, runID string, expected int64, status domain.RunStatus, reason string, event domain.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[runID]
	if run.Generation != expected {
		return domain.ErrGenerationConflict
	}
	run.Status, run.Generation = status, run.Generation+1
	if status == domain.RunStatusPauseRequested || status == domain.RunStatusPaused {
		run.PauseReason = reason
	}
	if status == domain.RunStatusCancelRequested {
		run.CancelReason = reason
	}
	s.events = append(s.events, event)
	return nil
}

func newDAGStore() *dagStore { return &dagStore{memoryStore: newMemoryStore()} }

func (s *dagStore) AppendEvent(_ context.Context, _ string, event domain.Event) (domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	event.SequenceNo = int64(len(s.events) + 1)
	s.events = append(s.events, event)
	return event, nil
}

func (s *dagStore) ListEvents(_ context.Context, _, runID string, after int64, limit int) ([]domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Event
	for _, event := range s.events {
		if event.RunID == runID && event.SequenceNo > after {
			out = append(out, event)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

type scriptedRegistry struct {
	mu       sync.Mutex
	calls    []string
	attempts map[string]int
	run      func(context.Context, port.NodeExecutionRequest) (port.NodeExecutionResult, error)
}

func (r *scriptedRegistry) Execute(ctx context.Context, request port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, request.Node.ID)
	if r.attempts == nil {
		r.attempts = map[string]int{}
	}
	r.attempts[request.Node.ID]++
	r.mu.Unlock()
	if r.run != nil {
		return r.run(ctx, request)
	}
	return port.NodeExecutionResult{Output: request.Node.ID + "-output", TraceID: "trace-" + request.Node.ID}, nil
}

func createPublishedRun(t *testing.T, store *dagStore, registry port.NodeExecutorRegistry, spec domain.Spec) (*application.RunService, *domain.Run) {
	t.Helper()
	ids := &ids{}
	definitions := application.NewDefinitionService(store, store, ids.NewID)
	definition, err := definitions.Create(context.Background(), "tenant-1", application.CreateDefinitionCommand{Name: "DAG", Spec: spec})
	require.NoError(t, err)
	version, err := definitions.Publish(context.Background(), "tenant-1", definition.ID)
	require.NoError(t, err)
	runs := application.NewRunServiceWithRegistry(store, store, registry, ids.NewID)
	run, _, err := runs.Start(context.Background(), "tenant-1", application.StartRunCommand{VersionID: version.ID, Input: map[string]any{"route": true}, IdempotencyKey: "dag-key"})
	require.NoError(t, err)
	return runs, run
}

func TestDAGSchedulerDiamondFanInExecutesJoinOnce(t *testing.T) {
	store, registry := newDAGStore(), &scriptedRegistry{}
	spec := domain.Spec{Nodes: []domain.Node{
		{ID: "start", Type: domain.NodeTypeAgent, AgentID: "a"},
		{ID: "left", Type: domain.NodeTypeAgent, AgentID: "b"},
		{ID: "right", Type: domain.NodeTypeAgent, AgentID: "c"},
		{ID: "join", Type: domain.NodeTypeAgent, AgentID: "d"},
	}, Edges: []domain.Edge{{From: "start", To: "left"}, {From: "start", To: "right"}, {From: "left", To: "join"}, {From: "right", To: "join"}}, MaxConcurrency: 2}
	runs, run := createPublishedRun(t, store, registry, spec)
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	got, attempts, err := runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusCompleted, got.Status)
	require.Len(t, attempts, 4)
	require.Equal(t, 1, registry.attempts["join"])
	sort.Strings(registry.calls)
	require.Equal(t, []string{"join", "left", "right", "start"}, registry.calls)
}

func TestDAGSchedulerConditionSkipsUnselectedBranch(t *testing.T) {
	store := newDAGStore()
	registry := &scriptedRegistry{run: func(_ context.Context, request port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
		if request.Node.Type == domain.NodeTypeCondition {
			return port.NodeExecutionResult{ConditionValue: true}, nil
		}
		return port.NodeExecutionResult{Output: request.Node.ID + "-output"}, nil
	}}
	truth := true
	spec := domain.Spec{Nodes: []domain.Node{
		{ID: "route", Type: domain.NodeTypeCondition, Condition: "input.route == true"},
		{ID: "selected", Type: domain.NodeTypeAgent, AgentID: "a"},
		{ID: "fallback", Type: domain.NodeTypeAgent, AgentID: "b"},
	}, Edges: []domain.Edge{{From: "route", To: "selected", ConditionValue: &truth}, {From: "route", To: "fallback", Default: true}}}
	runs, run := createPublishedRun(t, store, registry, spec)
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	_, attempts, err := runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, err)
	byNode := map[string]domain.AttemptStatus{}
	var selectedEdges []string
	for _, attempt := range attempts {
		byNode[attempt.NodeID] = attempt.Status
		if attempt.NodeID == "route" {
			selectedEdges = attempt.SelectedEdges
		}
	}
	require.Equal(t, domain.AttemptStatusSucceeded, byNode["selected"])
	require.Equal(t, domain.AttemptStatusSkipped, byNode["fallback"])
	require.Equal(t, 0, registry.attempts["fallback"])
	require.Equal(t, []string{"route->selected"}, selectedEdges)
}

func TestDAGSchedulerRetryCreatesNewAttempt(t *testing.T) {
	store := newDAGStore()
	registry := &scriptedRegistry{run: func(_ context.Context, request port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
		if request.AttemptNo == 1 {
			return port.NodeExecutionResult{Retryable: true, ErrorCode: "temporary"}, errors.New("temporary")
		}
		return port.NodeExecutionResult{Output: "recovered"}, nil
	}}
	spec := domain.Spec{Nodes: []domain.Node{{ID: "retry", Type: domain.NodeTypeAgent, AgentID: "a", Retry: domain.RetryPolicy{MaxAttempts: 2}}}}
	runs, run := createPublishedRun(t, store, registry, spec)
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	_, attempts, err := runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 2)
	require.Equal(t, 1, attempts[0].AttemptNo)
	require.Equal(t, domain.AttemptStatusRetryWait, attempts[0].Status)
	require.Equal(t, 2, attempts[1].AttemptNo)
	require.Equal(t, domain.AttemptStatusSucceeded, attempts[1].Status)
}

func TestDAGSchedulerDoesNotRetryUndeclaredNonIdempotentEffect(t *testing.T) {
	store := newDAGStore()
	registry := &scriptedRegistry{run: func(_ context.Context, _ port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
		return port.NodeExecutionResult{Retryable: true, ErrorCode: "temporary"}, errors.New("uncertain external result")
	}}
	spec := domain.Spec{Nodes: []domain.Node{{ID: "write", Type: domain.NodeTypeMCPTool, MCPServerID: "crm", MCPToolName: "create", EffectClass: domain.EffectClassNonIdempotent, Retry: domain.RetryPolicy{MaxAttempts: 3}}}}
	runs, run := createPublishedRun(t, store, registry, spec)
	require.Error(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	_, attempts, err := runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, domain.AttemptStatusFailed, attempts[0].Status)
}

func TestDAGSchedulerPersistsBackoffWithoutSleepingWorker(t *testing.T) {
	store := newDAGStore()
	registry := &scriptedRegistry{run: func(_ context.Context, request port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
		if request.AttemptNo == 1 {
			return port.NodeExecutionResult{Retryable: true}, errors.New("temporary")
		}
		return port.NodeExecutionResult{Output: "recovered"}, nil
	}}
	spec := domain.Spec{Nodes: []domain.Node{{ID: "retry", Type: domain.NodeTypeAgent, AgentID: "a", Retry: domain.RetryPolicy{MaxAttempts: 2, BackoffMS: 20}}}}
	runs, run := createPublishedRun(t, store, registry, spec)
	started := time.Now()
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	require.Less(t, time.Since(started), 15*time.Millisecond)
	got, attempts, err := runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusQueued, got.Status)
	require.Len(t, attempts, 1)
	time.Sleep(25 * time.Millisecond)
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	got, attempts, err = runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusCompleted, got.Status)
	require.Len(t, attempts, 2)
}

func TestDAGSchedulerAttemptTimeoutDoesNotBlockWorker(t *testing.T) {
	store := newDAGStore()
	registry := &scriptedRegistry{run: func(ctx context.Context, request port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
		require.NoError(t, request.BeforeEffect())
		<-ctx.Done()
		return port.NodeExecutionResult{}, ctx.Err()
	}}
	spec := domain.Spec{Nodes: []domain.Node{{ID: "tool", Type: domain.NodeTypeMCPTool, MCPServerID: "slow", MCPToolName: "wait", EffectClass: domain.EffectClassPure, TimeoutMS: 20}}}
	runs, run := createPublishedRun(t, store, registry, spec)
	started := time.Now()
	require.Error(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	require.Less(t, time.Since(started), 500*time.Millisecond)
}

func TestWorkflowEventsHaveMonotonicCursorAndResumeAfterSequence(t *testing.T) {
	store, registry := newDAGStore(), &scriptedRegistry{}
	spec := domain.Spec{Nodes: []domain.Node{{ID: "one", Type: domain.NodeTypeAgent, AgentID: "a"}}}
	runs, run := createPublishedRun(t, store, registry, spec)
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	events, err := runs.Events(context.Background(), "tenant-1", run.ID, 0, 100)
	require.NoError(t, err)
	require.NotEmpty(t, events)
	for i := range events {
		require.Equal(t, int64(i+1), events[i].SequenceNo)
	}
	after, err := runs.Events(context.Background(), "tenant-1", run.ID, events[1].SequenceNo, 100)
	require.NoError(t, err)
	require.Equal(t, events[2:], after)
}

func TestSchedulerStampsAttemptWithClaimGenerationFence(t *testing.T) {
	store, registry := newDAGStore(), &scriptedRegistry{}
	runs, run := createPublishedRun(t, store, registry, domain.Spec{Nodes: []domain.Node{{ID: "one", Type: domain.NodeTypeAgent, AgentID: "a"}}})
	store.runs[run.ID].Generation = 7
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	require.Equal(t, int64(7), store.attempts[run.ID][0].FenceToken)
}

func TestHighRiskMCPPausedResultStopsDownstream(t *testing.T) {
	store := newDAGStore()
	registry := &scriptedRegistry{run: func(_ context.Context, request port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
		if request.Node.Type == domain.NodeTypeMCPTool {
			return port.NodeExecutionResult{Paused: true, ErrorCode: "approval_required"}, nil
		}
		return port.NodeExecutionResult{Output: "unexpected"}, nil
	}}
	spec := domain.Spec{Nodes: []domain.Node{{ID: "tool", Type: domain.NodeTypeMCPTool, MCPServerID: "crm", MCPToolName: "delete", EffectClass: domain.EffectClassNonIdempotent}, {ID: "after", Type: domain.NodeTypeAgent, AgentID: "agent"}}, Edges: []domain.Edge{{From: "tool", To: "after"}}}
	runs, run := createPublishedRun(t, store, registry, spec)
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	got, attempts, err := runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusPaused, got.Status)
	require.Len(t, attempts, 1)
	require.Equal(t, domain.AttemptStatusPaused, attempts[0].Status)
	require.Zero(t, registry.attempts["after"])
	require.Len(t, store.approvals, 1)
}

func TestApprovalNodePersistsRequestAndPauses(t *testing.T) {
	store, registry := newDAGStore(), &scriptedRegistry{}
	runs, run := createPublishedRun(t, store, registry, domain.Spec{Nodes: []domain.Node{{ID: "approval", Type: domain.NodeTypeApproval}}})
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	got, attempts, err := runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusPaused, got.Status)
	require.Equal(t, domain.AttemptStatusPaused, attempts[0].Status)
	require.Len(t, store.approvals, 1)
	require.Empty(t, registry.calls)
}

func TestNonIdempotentCanceledExecutionBecomesManualIntervention(t *testing.T) {
	store := newDAGStore()
	registry := &scriptedRegistry{run: func(ctx context.Context, request port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
		require.NoError(t, request.BeforeEffect())
		<-ctx.Done()
		return port.NodeExecutionResult{}, ctx.Err()
	}}
	runs, run := createPublishedRun(t, store, registry, domain.Spec{Nodes: []domain.Node{{ID: "tool", Type: domain.NodeTypeMCPTool, MCPServerID: "crm", MCPToolName: "create", EffectClass: domain.EffectClassNonIdempotent, TimeoutMS: 10}}})
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	got, attempts, err := runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusManualIntervention, got.Status)
	require.Equal(t, domain.AttemptStatusManualIntervention, attempts[0].Status)
	require.Equal(t, domain.EffectIntentStatusUnknown, store.effects[0].Status)
}

func TestApprovedHighRiskMCPExecutesExactlyOnceAfterExplicitResume(t *testing.T) {
	store := newDAGStore()
	calls := 0
	registry := &scriptedRegistry{run: func(_ context.Context, request port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
		if request.Node.Type == domain.NodeTypeMCPTool && !request.Approved {
			return port.NodeExecutionResult{Paused: true, ErrorCode: "approval_required"}, nil
		}
		calls++
		return port.NodeExecutionResult{Output: `{"ok":true}`}, nil
	}}
	runs, run := createPublishedRun(t, store, registry, domain.Spec{Nodes: []domain.Node{{ID: "tool", Type: domain.NodeTypeMCPTool, MCPServerID: "crm", MCPToolName: "delete", EffectClass: domain.EffectClassNonIdempotent}}})
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	require.Zero(t, calls)
	store.approvals[0].Status = domain.ApprovalStatusApproved
	store.runs[run.ID].Status = domain.RunStatusQueued
	store.runs[run.ID].Generation++
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	require.Equal(t, 1, calls)
	got, _, err := runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusCompleted, got.Status)
}

func TestRejectedApprovalFailsWithoutCallingNode(t *testing.T) {
	store := newDAGStore()
	calls := 0
	registry := &scriptedRegistry{run: func(context.Context, port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
		calls++
		return port.NodeExecutionResult{Output: "bad"}, nil
	}}
	runs, run := createPublishedRun(t, store, registry, domain.Spec{Nodes: []domain.Node{{ID: "approval", Type: domain.NodeTypeApproval}}})
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	store.approvals[0].Status = domain.ApprovalStatusRejected
	store.runs[run.ID].Status = domain.RunStatusQueued
	store.runs[run.ID].Generation++
	require.Error(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	require.Zero(t, calls)
}

func TestCancelBeforeStartFinalizesWithoutExecutingNode(t *testing.T) {
	store, registry := newDAGStore(), &scriptedRegistry{}
	runs, run := createPublishedRun(t, store, registry, domain.Spec{Nodes: []domain.Node{{ID: "one", Type: domain.NodeTypeAgent, AgentID: "a"}}})
	store.runs[run.ID].Status = domain.RunStatusCancelRequested
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	got, attempts, err := runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusCanceled, got.Status)
	require.Empty(t, attempts)
	require.Empty(t, registry.calls)
}

func TestCancelDuringPureNodeCannotCommitSuccess(t *testing.T) {
	store := newDAGStore()
	registry := &scriptedRegistry{run: func(_ context.Context, request port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
		run := store.runs[request.RunID]
		require.NoError(t, store.ControlRun(context.Background(), "tenant-1", request.RunID, run.Generation, domain.RunStatusCancelRequested, "stop", domain.Event{Type: "workflow.cancel_requested"}))
		return port.NodeExecutionResult{}, context.Canceled
	}}
	runs, run := createPublishedRun(t, store, registry, domain.Spec{Nodes: []domain.Node{{ID: "one", Type: domain.NodeTypeAgent, AgentID: "a", EffectClass: domain.EffectClassPure}}})
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	got, attempts, err := runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusCanceled, got.Status)
	require.Equal(t, domain.AttemptStatusCanceled, attempts[0].Status)
}

func TestPauseBeforeStartStopsAtBoundary(t *testing.T) {
	store, registry := newDAGStore(), &scriptedRegistry{}
	runs, run := createPublishedRun(t, store, registry, domain.Spec{Nodes: []domain.Node{{ID: "one", Type: domain.NodeTypeAgent, AgentID: "a"}}})
	store.runs[run.ID].Status = domain.RunStatusPauseRequested
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	got, attempts, err := runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusPaused, got.Status)
	require.Empty(t, attempts)
	require.Empty(t, registry.calls)
}

func TestRecoveryRequeuesExpiredPureAttemptWithoutRerunningSucceededNodes(t *testing.T) {
	store, registry := newDAGStore(), &scriptedRegistry{}
	runs, run := createPublishedRun(t, store, registry, domain.Spec{Nodes: []domain.Node{{ID: "done", Type: domain.NodeTypeAgent, AgentID: "a"}, {ID: "crashed", Type: domain.NodeTypeAgent, AgentID: "b"}}, Edges: []domain.Edge{{From: "done", To: "crashed"}}})
	store.runs[run.ID].Status = domain.RunStatusRunning
	store.runs[run.ID].Generation = 4
	store.attempts[run.ID] = []domain.NodeAttempt{{ID: "done-attempt", RunID: run.ID, NodeID: "done", AttemptNo: 1, Status: domain.AttemptStatusSucceeded, OutputSummary: "done", FenceToken: 3, RunGeneration: 3}, {ID: "crashed-attempt", RunID: run.ID, NodeID: "crashed", AttemptNo: 1, Status: domain.AttemptStatusRunning, FenceToken: 3, RunGeneration: 3, EffectClass: domain.EffectClassPure}}
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	require.Zero(t, registry.attempts["done"])
	require.Equal(t, 1, registry.attempts["crashed"])
	got, attempts, err := runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusCompleted, got.Status)
	require.Len(t, attempts, 3)
}

func TestRecoveryMarksStartedNonIdempotentEffectUnknownWithoutReplay(t *testing.T) {
	store, registry := newDAGStore(), &scriptedRegistry{}
	runs, run := createPublishedRun(t, store, registry, domain.Spec{Nodes: []domain.Node{{ID: "tool", Type: domain.NodeTypeMCPTool, MCPServerID: "crm", MCPToolName: "create", EffectClass: domain.EffectClassNonIdempotent}}})
	store.runs[run.ID].Status = domain.RunStatusRunning
	store.runs[run.ID].Generation = 5
	attempt := domain.NodeAttempt{ID: "attempt", RunID: run.ID, NodeID: "tool", AttemptNo: 1, Status: domain.AttemptStatusRunning, FenceToken: 4, RunGeneration: 4, EffectClass: domain.EffectClassNonIdempotent}
	store.attempts[run.ID] = []domain.NodeAttempt{attempt}
	intent := domain.NewEffectIntent("effect", run.ID, "tool", attempt.ID, 4, domain.EffectClassNonIdempotent, "stable")
	require.NoError(t, intent.Start(4))
	store.effects = []domain.EffectIntent{*intent}
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	require.Empty(t, registry.calls)
	got, attempts, err := runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusManualIntervention, got.Status)
	require.Equal(t, domain.AttemptStatusManualIntervention, attempts[0].Status)
	require.Equal(t, domain.EffectIntentStatusUnknown, store.effects[0].Status)
}

func TestRunningPauseRetriesAgentAtNodeBoundary(t *testing.T) {
	store := newDAGStore()
	calls := 0
	registry := &scriptedRegistry{run: func(_ context.Context, request port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
		calls++
		if calls == 1 {
			run := store.runs[request.RunID]
			require.NoError(t, store.ControlRun(context.Background(), "tenant-1", run.ID, run.Generation, domain.RunStatusPauseRequested, "operator", domain.Event{Type: "workflow.pause_requested"}))
			return port.NodeExecutionResult{}, context.Canceled
		}
		return port.NodeExecutionResult{Output: "done"}, nil
	}}
	runs, run := createPublishedRun(t, store, registry, domain.Spec{Nodes: []domain.Node{{ID: "agent", Type: domain.NodeTypeAgent, AgentID: "a", EffectClass: domain.EffectClassPure}}})
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	paused, attempts, err := runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusPaused, paused.Status)
	require.Equal(t, domain.AttemptStatusRetryWait, attempts[0].Status)
	store.runs[run.ID].Status = domain.RunStatusQueued
	store.runs[run.ID].Generation++
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	require.Equal(t, 2, calls)
}

func TestRunningPauseRetriesIdempotentMCPButManualsNonIdempotent(t *testing.T) {
	tests := []struct {
		name        string
		class       domain.EffectClass
		wantRun     domain.RunStatus
		wantAttempt domain.AttemptStatus
	}{
		{"idempotent", domain.EffectClassIdempotent, domain.RunStatusPaused, domain.AttemptStatusRetryWait},
		{"non-idempotent", domain.EffectClassNonIdempotent, domain.RunStatusManualIntervention, domain.AttemptStatusManualIntervention},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newDAGStore()
			registry := &scriptedRegistry{run: func(_ context.Context, request port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
				require.NoError(t, request.BeforeEffect())
				run := store.runs[request.RunID]
				require.NoError(t, store.ControlRun(context.Background(), "tenant-1", run.ID, run.Generation, domain.RunStatusPauseRequested, "operator", domain.Event{Type: "workflow.pause_requested"}))
				return port.NodeExecutionResult{}, context.Canceled
			}}
			runs, run := createPublishedRun(t, store, registry, domain.Spec{Nodes: []domain.Node{{ID: "tool", Type: domain.NodeTypeMCPTool, MCPServerID: "crm", MCPToolName: "write", EffectClass: tc.class}}})
			require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
			got, attempts, err := runs.Get(context.Background(), "tenant-1", run.ID)
			require.NoError(t, err)
			require.Equal(t, tc.wantRun, got.Status)
			require.Equal(t, tc.wantAttempt, attempts[0].Status)
		})
	}
}

func TestRunStartAndRecoveryUseDistinctAtomicEvents(t *testing.T) {
	base := newDAGStore()
	store := &startCheckpointStore{dagStore: base}
	ids := &ids{}
	definitions := application.NewDefinitionService(store, store, ids.NewID)
	definition, err := definitions.Create(context.Background(), "tenant-1", application.CreateDefinitionCommand{Name: "Events", Spec: domain.Spec{Nodes: []domain.Node{{ID: "one", Type: domain.NodeTypeAgent, AgentID: "a"}}}})
	require.NoError(t, err)
	version, err := definitions.Publish(context.Background(), "tenant-1", definition.ID)
	require.NoError(t, err)
	runs := application.NewRunServiceWithRegistry(store, store, &scriptedRegistry{}, ids.NewID)
	run, _, err := runs.Start(context.Background(), "tenant-1", application.StartRunCommand{VersionID: version.ID, Input: map[string]any{}, IdempotencyKey: "start-event"})
	require.NoError(t, err)
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	require.Contains(t, store.runEvents, "workflow.run_started")
	recovery, _, err := runs.Start(context.Background(), "tenant-1", application.StartRunCommand{VersionID: version.ID, Input: map[string]any{}, IdempotencyKey: "recovery-event"})
	require.NoError(t, err)
	store.runs[recovery.ID].Status = domain.RunStatusRunning
	store.runs[recovery.ID].Generation = 3
	store.events = append(store.events, domain.Event{RunID: recovery.ID, SequenceNo: int64(len(store.events) + 1), Type: "workflow.run_started"})
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", recovery.ID))
	require.Contains(t, store.runEvents, "workflow.run_recovered")
}

func TestDAGSchedulerAppliesStructuredOutputMapping(t *testing.T) {
	store := newDAGStore()
	registry := &scriptedRegistry{run: func(_ context.Context, _ port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
		return port.NodeExecutionResult{Output: `{"value":"ok","ignored":true}`}, nil
	}}
	spec := domain.Spec{Nodes: []domain.Node{{ID: "mapped", Type: domain.NodeTypeAgent, AgentID: "agent", OutputMapping: map[string]string{"answer": "$.value"}}}}
	runs, run := createPublishedRun(t, store, registry, spec)
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	got, attempts, err := runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, `{"answer":"ok"}`, got.Output)
	require.Equal(t, `{"answer":"ok"}`, attempts[0].OutputSummary)
}

func TestDAGSchedulerAppliesNestedOutputSelector(t *testing.T) {
	store := newDAGStore()
	registry := &scriptedRegistry{run: func(_ context.Context, _ port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
		return port.NodeExecutionResult{Output: `{"customer":{"name":"Ada"}}`}, nil
	}}
	spec := domain.Spec{Nodes: []domain.Node{{ID: "mapped", Type: domain.NodeTypeAgent, AgentID: "agent", OutputMapping: map[string]string{"name": "$.customer.name"}}}}
	runs, run := createPublishedRun(t, store, registry, spec)
	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	got, _, err := runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, `{"name":"Ada"}`, got.Output)
}
