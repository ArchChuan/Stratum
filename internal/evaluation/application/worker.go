package application

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/google/uuid"
)

type TenantLister interface {
	ListTenantIDs(ctx context.Context) ([]string, error)
}

type TenantJobRunner interface {
	RunOnce(ctx context.Context, tenantID, workerID string, lease time.Duration) (bool, error)
}

type Worker struct {
	lister       TenantLister
	runner       TenantJobRunner
	idleInterval time.Duration
	workerID     string
	metrics      observability.MetricsProvider
	stopCh       chan struct{}
	stopOnce     sync.Once
	wg           sync.WaitGroup
}

func NewWorker(lister TenantLister, runner TenantJobRunner, idleInterval time.Duration, metrics observability.MetricsProvider) *Worker {
	return &Worker{
		lister: lister, runner: runner, idleInterval: idleInterval,
		workerID: uuid.Must(uuid.NewV7()).String(),
		metrics:  metrics,
		stopCh:   make(chan struct{}),
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		idleTicker := time.NewTicker(w.idleInterval)
		defer idleTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.stopCh:
				return
			default:
			}
			// 有工作时紧接轮询；空转时等待 idle 间隔，禁止无退避空轮询。
			worked, _ := w.PollOnce(ctx)
			if worked {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case <-w.stopCh:
				return
			case <-idleTicker.C:
			}
		}
	}()
}

func (w *Worker) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
	w.wg.Wait()
}

func (w *Worker) PollOnce(ctx context.Context) (bool, error) {
	tenantIDs, err := w.lister.ListTenantIDs(ctx)
	if err != nil {
		w.metrics.IncEvaluationJob("list_error")
		return false, err
	}
	var failures []error
	anyWork := false
	for _, tenantID := range tenantIDs {
		worked, err := w.runner.RunOnce(ctx, tenantID, w.workerID, time.Minute)
		if err != nil {
			failures = append(failures, err)
			w.metrics.IncEvaluationJob("error")
		} else {
			w.metrics.IncEvaluationJob("ok")
			if worked {
				anyWork = true
			}
		}
	}
	return anyWork, errors.Join(failures...)
}

// NewMultiRunner returns a TenantJobRunner that delegates to each runner
// in order, merging their results.
func NewMultiRunner(runners ...TenantJobRunner) TenantJobRunner {
	return &multiRunner{runners: runners}
}

type multiRunner struct {
	runners []TenantJobRunner
}

func (m *multiRunner) RunOnce(ctx context.Context, tenantID, workerID string, lease time.Duration) (bool, error) {
	anyWork := false
	var failures []error
	for _, runner := range m.runners {
		didWork, err := runner.RunOnce(ctx, tenantID, workerID, lease)
		if err != nil {
			failures = append(failures, err)
		}
		if didWork {
			anyWork = true
		}
	}
	return anyWork, errors.Join(failures...)
}
