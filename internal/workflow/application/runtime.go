package application

import (
	"context"
	"time"

	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/internal/workflow/domain/port"
)

type RunAdvancer interface {
	Execute(context.Context, string, string) error
}

type Worker struct {
	owner   string
	runtime port.RuntimePort
	runs    RunAdvancer
	lease   time.Duration
}

type leaseHeartbeat interface {
	RenewRunLease(context.Context, string, string, string, int64, time.Duration) error
}

type runControlObserver interface {
	RunControlState(context.Context, string, string, int64) (domain.RunStatus, error)
}

func NewWorker(owner string, runtime port.RuntimePort, runs RunAdvancer, lease time.Duration) *Worker {
	return &Worker{owner: owner, runtime: runtime, runs: runs, lease: lease}
}

func (w *Worker) RunOnce(ctx context.Context) bool {
	tenantID, run, claimed, err := w.runtime.ClaimRun(ctx, w.owner, w.lease)
	if err != nil || !claimed {
		return false
	}
	execCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go w.heartbeat(execCtx, cancel, done, tenantID, run.ID, run.Generation)
	_ = w.runs.Execute(execCtx, tenantID, run.ID)
	close(done)
	cancel()
	_ = w.runtime.ReleaseRun(ctx, tenantID, run.ID, w.owner, run.Generation)
	return true
}

func (w *Worker) heartbeat(ctx context.Context, cancel context.CancelFunc, done <-chan struct{}, tenantID, runID string, generation int64) {
	heartbeat, renews := w.runtime.(leaseHeartbeat)
	observer, observes := w.runtime.(runControlObserver)
	if !renews && !observes {
		return
	}
	interval := w.lease / 3
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			if renews {
				if err := heartbeat.RenewRunLease(ctx, tenantID, runID, w.owner, generation, w.lease); err != nil {
					cancel()
					return
				}
			}
			if observes {
				status, err := observer.RunControlState(ctx, tenantID, runID, generation)
				if err != nil || status == domain.RunStatusCancelRequested {
					cancel()
					return
				}
			}
		}
	}
}

func (w *Worker) Run(ctx context.Context, idle time.Duration) {
	if idle <= 0 {
		idle = 250 * time.Millisecond
	}
	ticker := time.NewTicker(idle)
	defer ticker.Stop()
	for {
		if w.RunOnce(ctx) {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
