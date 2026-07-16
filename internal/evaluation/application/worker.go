package application

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

type TenantLister interface {
	ListTenantIDs(ctx context.Context) ([]string, error)
}

type TenantJobRunner interface {
	RunOnce(ctx context.Context, tenantID, workerID string, lease time.Duration) (bool, error)
}

type Worker struct {
	lister   TenantLister
	runner   TenantJobRunner
	interval time.Duration
	workerID string
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func NewWorker(lister TenantLister, runner TenantJobRunner, interval time.Duration) *Worker {
	return &Worker{
		lister: lister, runner: runner, interval: interval,
		workerID: uuid.Must(uuid.NewV7()).String(), stopCh: make(chan struct{}),
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.stopCh:
				return
			case <-ticker.C:
				_ = w.PollOnce(ctx)
			}
		}
	}()
}

func (w *Worker) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
	w.wg.Wait()
}

func (w *Worker) PollOnce(ctx context.Context) error {
	tenantIDs, err := w.lister.ListTenantIDs(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, tenantID := range tenantIDs {
		_, err := w.runner.RunOnce(ctx, tenantID, w.workerID, time.Minute)
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
