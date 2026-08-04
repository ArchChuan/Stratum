package application_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/collab/application"
	"github.com/byteBuilderX/stratum/internal/collab/domain"
	"github.com/stretchr/testify/require"
)

// fakeRunner simulates the agent runner.
type fakeRunner struct {
	mu       sync.Mutex
	outputs  map[string]map[string]any // agentID -> output
	errs     map[string]error
	sleep    time.Duration
	callArgs []string // tenantID|agentID
}

func (r *fakeRunner) RunAgentStep(_ context.Context, tenantID, agentID string, _ map[string]any) (map[string]any, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.callArgs = append(r.callArgs, tenantID+"|"+agentID)
	if r.sleep > 0 {
		time.Sleep(r.sleep)
	}
	if err, ok := r.errs[agentID]; ok {
		return nil, "", err
	}
	return r.outputs[agentID], "trace-1", nil
}

func newWorker(store *collabStore, runner *fakeRunner, metrics *fakeMetrics) *application.Worker {
	return application.NewWorker("worker-1", store, store.stepsRepo(), store, runner, time.Minute, metrics)
}

func runningPlan(store *collabStore, id string) {
	createdPlan(store, id, "tenant-1", "creator")
	store.collabs[id].Status = domain.CollabRunning
}

func TestWorkerRunOnceFullFlow(t *testing.T) {
	store := newCollabStore()
	runningPlan(store, "plan-1")
	store.steps = []domain.TaskStep{{ID: "s1", PlanID: "plan-1", AgentID: "agent-1", Status: domain.TaskPending, MaxRetries: 3}}
	runner := &fakeRunner{outputs: map[string]map[string]any{"agent-1": {"answer": "ok"}}}
	metrics := &fakeMetrics{}
	worker := newWorker(store, runner, metrics)

	require.True(t, worker.RunOnce(context.Background()))
	require.Equal(t, domain.TaskCompleted, store.steps[0].Status)
	require.Equal(t, "ok", store.steps[0].Output["answer"])
	require.Equal(t, []string{"tenant-1|agent-1"}, runner.callArgs)
	// shared context aggregated under the agent id
	sc, err := store.Get(context.Background(), "tenant-1", "plan-1")
	require.NoError(t, err)
	require.NotNil(t, sc)
	require.Contains(t, string(sc.Data), `"agent-1"`)
	// plan judged completed
	require.Equal(t, domain.CollabCompleted, store.collabs["plan-1"].Status)
	require.Equal(t, []string{"sequential|completed"}, metrics.planOutcomes)
	require.Greater(t, metrics.taskDuration, float64(0))
}

func TestWorkerClaimFailureReturnsFalse(t *testing.T) {
	store := newCollabStore()
	store.claimErr = errors.New("db down")
	worker := newWorker(store, &fakeRunner{}, &fakeMetrics{})
	require.False(t, worker.RunOnce(context.Background()))

	store.claimErr = nil
	require.False(t, worker.RunOnce(context.Background()), "no claimable steps")
}

func TestWorkerRunnerErrorRetriesThenFails(t *testing.T) {
	t.Run("retries below budget release to pending", func(t *testing.T) {
		store := newCollabStore()
		runningPlan(store, "plan-1")
		store.steps = []domain.TaskStep{{ID: "s1", PlanID: "plan-1", AgentID: "agent-1", Status: domain.TaskPending, RetryCount: 0, MaxRetries: 3}}
		runner := &fakeRunner{errs: map[string]error{"agent-1": errors.New("boom")}}
		worker := newWorker(store, runner, &fakeMetrics{})

		require.True(t, worker.RunOnce(context.Background()))
		require.Equal(t, domain.TaskPending, store.steps[0].Status, "released for retry")
		require.Equal(t, 1, store.steps[0].RetryCount, "retry_count bumped on release")
		require.Equal(t, "boom", store.steps[0].Error)
	})

	t.Run("budget exhausted marks failed", func(t *testing.T) {
		store := newCollabStore()
		runningPlan(store, "plan-1")
		store.steps = []domain.TaskStep{{ID: "s1", PlanID: "plan-1", AgentID: "agent-1", Status: domain.TaskPending, RetryCount: 2, MaxRetries: 3}}
		runner := &fakeRunner{errs: map[string]error{"agent-1": errors.New("boom")}}
		metrics := &fakeMetrics{}
		worker := newWorker(store, runner, metrics)

		require.True(t, worker.RunOnce(context.Background()))
		require.Equal(t, domain.TaskFailed, store.steps[0].Status)
		require.Equal(t, domain.CollabFailed, store.collabs["plan-1"].Status)
		require.Equal(t, []string{"sequential|failed"}, metrics.planOutcomes)
	})
}

func TestWorkerLongErrorTruncated(t *testing.T) {
	store := newCollabStore()
	runningPlan(store, "plan-1")
	store.steps = []domain.TaskStep{{ID: "s1", PlanID: "plan-1", AgentID: "agent-1", Status: domain.TaskPending, RetryCount: 99, MaxRetries: 100}}
	runner := &fakeRunner{errs: map[string]error{"agent-1": errors.New(strings.Repeat("x", 5000))}}
	worker := newWorker(store, runner, &fakeMetrics{})

	require.True(t, worker.RunOnce(context.Background()))
	require.Equal(t, domain.TaskFailed, store.steps[0].Status)
	require.Len(t, []rune(store.steps[0].Error), application.StepErrorMaxRunes)
}

func TestWorkerOutputCappedInSharedContext(t *testing.T) {
	store := newCollabStore()
	runningPlan(store, "plan-1")
	store.steps = []domain.TaskStep{{ID: "s1", PlanID: "plan-1", AgentID: "agent-1", Status: domain.TaskPending, MaxRetries: 3}}
	big := strings.Repeat("x", 200*1024)
	runner := &fakeRunner{outputs: map[string]map[string]any{"agent-1": {"blob": big}}}
	worker := newWorker(store, runner, &fakeMetrics{})

	require.True(t, worker.RunOnce(context.Background()))
	sc, err := store.Get(context.Background(), "tenant-1", "plan-1")
	require.NoError(t, err)
	require.NotContains(t, string(sc.Data), big, "oversized output must not reach shared context")
	require.Contains(t, string(sc.Data), `"truncated":true`)
	// step row keeps the full output as source of truth
	require.Equal(t, big, store.steps[0].Output["blob"])
}

func TestWorkerSharedContextConflictRetried(t *testing.T) {
	store := newCollabStore()
	runningPlan(store, "plan-1")
	store.steps = []domain.TaskStep{{ID: "s1", PlanID: "plan-1", AgentID: "agent-1", Status: domain.TaskPending, MaxRetries: 3}}
	runner := &fakeRunner{outputs: map[string]map[string]any{"agent-1": {"answer": "ok"}}}
	store.conflictOn["plan-1"] = true // first Update conflicts, then succeeds
	worker := newWorker(store, runner, &fakeMetrics{})

	require.True(t, worker.RunOnce(context.Background()))
	sc, err := store.Get(context.Background(), "tenant-1", "plan-1")
	require.NoError(t, err)
	require.NotNil(t, sc, "conflict retry must eventually write shared context")
}

func TestWorkerStaleGenerationRejected(t *testing.T) {
	store := newCollabStore()
	runningPlan(store, "plan-1")
	store.steps = []domain.TaskStep{{ID: "s1", PlanID: "plan-1", AgentID: "agent-1", Status: domain.TaskPending, MaxRetries: 3}}
	runner := &fakeRunner{outputs: map[string]map[string]any{"agent-1": {"answer": "ok"}}}
	worker := newWorker(store, runner, &fakeMetrics{})

	require.True(t, worker.RunOnce(context.Background()))
	// stale finalize with an old generation must be rejected by the repo fake
	err := store.stepsRepo().UpdateStatus(context.Background(), "tenant-1", "s1", 0, domain.TaskCompleted, nil, "")
	require.ErrorIs(t, err, domain.ErrCollabConflict)
	require.Equal(t, domain.TaskCompleted, store.steps[0].Status, "stale write must not mutate the step")
}

func TestWorkerCanceledPlanNotJudged(t *testing.T) {
	store := newCollabStore()
	runningPlan(store, "plan-1")
	store.collabs["plan-1"].Status = domain.CollabCanceled
	store.steps = []domain.TaskStep{{ID: "s1", PlanID: "plan-1", AgentID: "agent-1", Status: domain.TaskPending, MaxRetries: 3}}
	runner := &fakeRunner{outputs: map[string]map[string]any{"agent-1": {"answer": "ok"}}}
	metrics := &fakeMetrics{}
	worker := newWorker(store, runner, metrics)

	require.True(t, worker.RunOnce(context.Background()))
	require.Equal(t, domain.TaskCompleted, store.steps[0].Status)
	require.Equal(t, domain.CollabCanceled, store.collabs["plan-1"].Status, "canceled plan must not be resurrected")
	require.Empty(t, metrics.planOutcomes, "no completion metric for canceled plan")
}

func TestWorkerHeartbeatRenewsLease(t *testing.T) {
	store := newCollabStore()
	runningPlan(store, "plan-1")
	store.steps = []domain.TaskStep{{ID: "s1", PlanID: "plan-1", AgentID: "agent-1", Status: domain.TaskPending, MaxRetries: 3}}
	runner := &fakeRunner{outputs: map[string]map[string]any{"agent-1": {"answer": "ok"}}, sleep: 150 * time.Millisecond}
	metrics := &fakeMetrics{}
	worker := application.NewWorker("worker-1", store, store.stepsRepo(), store, runner, 90*time.Millisecond, metrics)

	require.True(t, worker.RunOnce(context.Background()))
	require.GreaterOrEqual(t, store.renewCalls.Load(), int64(1), "heartbeat must renew the lease during execution")
}
