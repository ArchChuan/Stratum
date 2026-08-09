package application

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/byteBuilderX/stratum/pkg/observability"
)

// TenantLister yields the active tenant IDs to poll.
type TenantLister interface {
	ListTenantIDs(ctx context.Context) ([]string, error)
}

// TenantPoller fires the due scheduled tasks of a single tenant.
type TenantPoller interface {
	PollTenant(ctx context.Context, tenantID string, now time.Time) error
}

// Worker polls due scheduled tasks on a fixed interval, one pass per
// active tenant. It mirrors the evaluation worker's ticker + stopOnce
// shape; a panic in one tick is recovered so the worker keeps running.
type Worker struct {
	lister   TenantLister
	poller   TenantPoller
	interval time.Duration
	workerID string
	metrics  observability.MetricsProvider
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	lastPollMu sync.RWMutex
	lastPollAt time.Time
}

// NewWorker constructs the scheduler worker.
func NewWorker(lister TenantLister, poller TenantPoller, interval time.Duration, metrics observability.MetricsProvider) *Worker {
	return &Worker{
		lister:   lister,
		poller:   poller,
		interval: interval,
		workerID: uuid.Must(uuid.NewV7()).String(),
		metrics:  metrics,
		stopCh:   make(chan struct{}),
	}
}

// Start begins the poll loop; it returns immediately.
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
				w.pollSafe(ctx)
			}
		}
	}()
}

// Stop signals the loop and waits for the in-flight poll pass to finish.
// Idempotent: a second call is a no-op.
func (w *Worker) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
	w.wg.Wait()
}

// LastPollAt returns the timestamp of the most recent completed poll pass
// (zero when the worker never polled). Used by the harness health check.
func (w *Worker) LastPollAt() time.Time {
	w.lastPollMu.RLock()
	defer w.lastPollMu.RUnlock()
	return w.lastPollAt
}

// pollSafe runs one poll pass, recovering per tick so a single panic cannot
// kill the worker silently.
func (w *Worker) pollSafe(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			w.metrics.IncScheduledFire(scheduleTypeCron, "panic")
		}
	}()
	_ = w.PollOnce(ctx)
}

// PollOnce runs one full pass: list tenants, poll each, merge failures.
func (w *Worker) PollOnce(ctx context.Context) error {
	tenantIDs, err := w.lister.ListTenantIDs(ctx)
	if err != nil {
		return err
	}
	var failures []error
	now := time.Now().UTC()
	for _, tenantID := range tenantIDs {
		if err := w.poller.PollTenant(ctx, tenantID, now); err != nil {
			failures = append(failures, err)
		}
	}
	w.lastPollMu.Lock()
	w.lastPollAt = time.Now()
	w.lastPollMu.Unlock()
	return errors.Join(failures...)
}
