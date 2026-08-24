// Package application internal worker: 过期审批清扫。
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

// ApprovalCleanupWorker periodically marks expired tool approvals across
// tenants. It follows the same ticker+goroutine pattern as
// CheckpointCleanupWorker; ExpireStale is idempotent (only pending/approved
// rows transition to expired).
type ApprovalCleanupWorker struct {
	tenants  func(ctx context.Context) ([]string, error)
	repo     port.ToolApprovalRepo
	interval time.Duration
	logger   *zap.Logger
	metrics  observability.MetricsProvider
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewApprovalCleanupWorker creates a worker that calls repo.ExpireStale for
// every tenant on each tick.
func NewApprovalCleanupWorker(
	tenants func(ctx context.Context) ([]string, error),
	repo port.ToolApprovalRepo,
	interval time.Duration,
	logger *zap.Logger,
	metrics observability.MetricsProvider,
) *ApprovalCleanupWorker {
	return &ApprovalCleanupWorker{
		tenants:  tenants,
		repo:     repo,
		interval: interval,
		logger:   logger,
		metrics:  metrics,
		stopCh:   make(chan struct{}),
	}
}

// Start begins the cleanup loop in a background goroutine.
func (w *ApprovalCleanupWorker) Start(ctx context.Context) {
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
func (w *ApprovalCleanupWorker) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
	w.wg.Wait()
}

func (w *ApprovalCleanupWorker) cleanupExpired(ctx context.Context) {
	timestamp := float64(time.Now().Unix())
	w.metrics.SetComponentCycleTimestamp("approval-cleanup", timestamp)

	tenantIDs, err := w.tenants(ctx)
	if err != nil {
		w.logger.Error("approval cleanup: list tenants failed", zap.Error(err))
		w.metrics.IncComponentError("approval-cleanup", "list_tenants")
		return
	}
	logger := w.logger
	if w.logger == nil {
		logger = zap.NewNop()
	}
	workerID := uuid.Must(uuid.NewV7()).String()
	var total int64
	for _, tenantID := range tenantIDs {
		expired, err := w.repo.ExpireStale(ctx, tenantID)
		if err != nil {
			logger.Error("approval cleanup: expire stale failed",
				zap.String("worker_id", workerID),
				zap.String("tenant_id", tenantID),
				zap.Error(err),
			)
			w.metrics.IncComponentError("approval-cleanup", "expire")
			continue
		}
		total += expired
	}
	w.metrics.RecordComponentCycle("approval-cleanup")
	if total > 0 {
		logger.Info("approval cleanup: expired stale approvals",
			zap.String("worker_id", workerID),
			zap.Int64("total_expired", total),
		)
	}
}
