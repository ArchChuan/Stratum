package application

import (
	"context"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// TaskCleanupWorker periodically reclaims expired agent_tasks across tenants.
// It follows the same ticker+goroutine pattern as CheckpointCleanupWorker;
// DeleteExpired is idempotent (expires_at filter), so no lease/lock layer.
type TaskCleanupWorker struct {
	tenants  func(ctx context.Context) ([]string, error)
	repo     port.TaskRepo
	interval time.Duration
	logger   *zap.Logger
	metrics  observability.MetricsProvider
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewTaskCleanupWorker creates a worker that calls repo.DeleteExpired for every
// tenant on each tick.
func NewTaskCleanupWorker(
	tenants func(ctx context.Context) ([]string, error),
	repo port.TaskRepo,
	interval time.Duration,
	logger *zap.Logger,
	metrics observability.MetricsProvider,
) *TaskCleanupWorker {
	return &TaskCleanupWorker{
		tenants:  tenants,
		repo:     repo,
		interval: interval,
		logger:   logger,
		metrics:  metrics,
		stopCh:   make(chan struct{}),
	}
}

// Start begins the cleanup loop in a background goroutine.
func (w *TaskCleanupWorker) Start(ctx context.Context) {
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

// Stop signals the worker to stop and waits for the current tick to finish.
func (w *TaskCleanupWorker) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
	w.wg.Wait()
}

func (w *TaskCleanupWorker) cleanupExpired(ctx context.Context) {
	timestamp := float64(time.Now().Unix())
	w.metrics.SetComponentCycleTimestamp("task-cleanup", timestamp)
	tenantIDs, err := w.tenants(ctx)
	if err != nil {
		w.logger.Error("task cleanup: list tenants failed", zap.Error(err))
		w.metrics.IncComponentError("task-cleanup", "list_tenants")
		return
	}
	logger := w.logger
	if logger == nil {
		logger = zap.NewNop()
	}
	workerID := uuid.Must(uuid.NewV7()).String()
	var total int64
	for _, tenantID := range tenantIDs {
		deleted, err := w.repo.DeleteExpired(ctx, tenantID)
		if err != nil {
			logger.Error("task cleanup: delete expired failed",
				zap.String("worker_id", workerID),
				zap.String("tenant_id", tenantID),
				zap.Error(err))
			w.metrics.IncComponentError("task-cleanup", "delete")
			continue
		}
		total += deleted
	}
	w.metrics.RecordComponentCycle("task-cleanup")
	if total > 0 {
		logger.Info("task cleanup: deleted expired rows",
			zap.String("worker_id", workerID),
			zap.Int64("total_deleted", total))
	}
}
