package application_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/workflow/application"
	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/stretchr/testify/require"
)

type runtimeFake struct {
	mu              sync.Mutex
	claimed         bool
	run             *domain.Run
	tenant          string
	renewals        int
	cancelRequested bool
}

func (r *runtimeFake) RenewRunLease(_ context.Context, _, _, _ string, _ int64, _ time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.renewals++
	return nil
}

func (r *runtimeFake) RunControlState(_ context.Context, _, _ string, _ int64) (domain.RunStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancelRequested {
		return domain.RunStatusCancelRequested, nil
	}
	return domain.RunStatusRunning, nil
}

func (r *runtimeFake) ClaimRun(_ context.Context, owner string, _ time.Duration) (string, *domain.Run, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claimed || r.run == nil {
		return "", nil, false, nil
	}
	r.claimed = true
	copy := *r.run
	copy.SchedulerOwner = owner
	copy.Generation++
	r.run = &copy
	return r.tenant, &copy, true, nil
}
func (r *runtimeFake) ReleaseRun(_ context.Context, _, runID, owner string, generation int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.run.ID != runID || r.run.SchedulerOwner != owner || r.run.Generation != generation {
		return domain.ErrFenceConflict
	}
	r.run.SchedulerOwner = ""
	return nil
}

type runAdvancerFake struct {
	mu    sync.Mutex
	calls int
	run   func(context.Context) error
}

func (a *runAdvancerFake) Execute(ctx context.Context, _, _ string) error {
	return a.execute(ctx)
}

func (a *runAdvancerFake) execute(ctx context.Context) error {
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	if a.run != nil {
		return a.run(ctx)
	}
	return nil
}

func TestDurableWorkerClaimsAndAdvancesWithoutRequestContext(t *testing.T) {
	runtime := &runtimeFake{run: &domain.Run{ID: "run-1", Status: domain.RunStatusQueued, Generation: 1}, tenant: "tenant-1"}
	advancer := &runAdvancerFake{}
	worker := application.NewWorker("worker-1", runtime, advancer, 100*time.Millisecond)
	require.True(t, worker.RunOnce(context.Background()))
	require.Equal(t, 1, advancer.calls)
}

func TestWorkerRenewsLeaseDuringLongNode(t *testing.T) {
	runtime := &runtimeFake{run: &domain.Run{ID: "run-1", Status: domain.RunStatusQueued, Generation: 1}, tenant: "tenant-1"}
	advancer := &runAdvancerFake{run: func(context.Context) error { time.Sleep(45 * time.Millisecond); return nil }}
	worker := application.NewWorker("worker-1", runtime, advancer, 20*time.Millisecond)
	require.True(t, worker.RunOnce(context.Background()))
	require.GreaterOrEqual(t, runtime.renewals, 2)
}

func TestWorkerCancelsExecutionContextWhenPersistentCancelObserved(t *testing.T) {
	runtime := &runtimeFake{run: &domain.Run{ID: "run-1", Status: domain.RunStatusQueued, Generation: 1}, tenant: "tenant-1"}
	started := make(chan struct{})
	advancer := &runAdvancerFake{run: func(ctx context.Context) error { close(started); <-ctx.Done(); return ctx.Err() }}
	worker := application.NewWorker("worker-1", runtime, advancer, 20*time.Millisecond)
	done := make(chan bool, 1)
	go func() { done <- worker.RunOnce(context.Background()) }()
	<-started
	runtime.mu.Lock()
	runtime.cancelRequested = true
	runtime.mu.Unlock()
	require.True(t, <-done)
}

func TestTwoWorkersCannotClaimSameRunGeneration(t *testing.T) {
	runtime := &runtimeFake{run: &domain.Run{ID: "run-1", Status: domain.RunStatusQueued, Generation: 1}, tenant: "tenant-1"}
	a, b := &runAdvancerFake{}, &runAdvancerFake{}
	workers := []*application.Worker{application.NewWorker("a", runtime, a, time.Second), application.NewWorker("b", runtime, b, time.Second)}
	var wg sync.WaitGroup
	for _, worker := range workers {
		wg.Add(1)
		go func(w *application.Worker) { defer wg.Done(); w.RunOnce(context.Background()) }(worker)
	}
	wg.Wait()
	require.Equal(t, 1, a.calls+b.calls)
}
