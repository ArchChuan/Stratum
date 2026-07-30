package application

import (
	"context"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// CheckpointCleanupWorker periodically deletes expired checkpoints
// across tenants. It follows the same ticker+goroutine pattern as
// evaluation.Worker but skips the lease/locking layer because
// DeleteExpired is idempotent (it excludes already-final statuses).
type CheckpointCleanupWorker struct {
	tenants  func(ctx context.Context) ([]string, error)
	repo     port.CheckpointRepo
	interval time.Duration
	logger   *zap.Logger
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewCheckpointCleanupWorker creates a worker that calls
// repo.DeleteExpired for every tenant on each tick.
func NewCheckpointCleanupWorker(
	tenants func(ctx context.Context) ([]string, error),
	repo port.CheckpointRepo,
	interval time.Duration,
	logger *zap.Logger,
) *CheckpointCleanupWorker {
	return &CheckpointCleanupWorker{
		tenants:  tenants,
		repo:     repo,
		interval: interval,
		logger:   logger,
		stopCh:   make(chan struct{}),
	}
}

// Start begins the cleanup loop in a background goroutine.
func (w *CheckpointCleanupWorker) Start(ctx context.Context) {
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
				w.cleanupExpired(ctx)
			}
		}
	}()
}

// Stop signals the worker to stop and waits for the current tick to
// finish.
func (w *CheckpointCleanupWorker) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
	w.wg.Wait()
}

func (w *CheckpointCleanupWorker) cleanupExpired(ctx context.Context) {
	tenantIDs, err := w.tenants(ctx)
	if err != nil {
		w.logger.Error("checkpoint cleanup: list tenants failed", zap.Error(err))
		return
	}
	logger := w.logger
	if w.logger == nil {
		logger = zap.NewNop()
	}
	workerID := uuid.Must(uuid.NewV7()).String()
	var total int64
	for _, tenantID := range tenantIDs {
		deleted, err := w.repo.DeleteExpired(ctx, tenantID)
		if err != nil {
			logger.Error("checkpoint cleanup: delete expired failed",
				zap.String("worker_id", workerID),
				zap.String("tenant_id", tenantID),
				zap.Error(err),
			)
			continue
		}
		total += deleted
	}
	if total > 0 {
		logger.Info("checkpoint cleanup: deleted expired rows",
			zap.String("worker_id", workerID),
			zap.Int64("total_deleted", total),
		)
	}
}
