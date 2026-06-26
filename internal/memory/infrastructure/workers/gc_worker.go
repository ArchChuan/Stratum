package workers

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// GCWorker periodically purges old deleted and superseded facts.
type GCWorker struct {
	tenantID string
	factRepo port.FactRepo
	queue    port.ExtractionQueue // optional: purge old completed queue tasks
	logger   *zap.Logger
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewGCWorker creates a garbage collection worker for a specific tenant.
func NewGCWorker(tenantID string, repo port.FactRepo, logger *zap.Logger) *GCWorker {
	return &GCWorker{
		tenantID: tenantID,
		factRepo: repo,
		logger:   logger,
		stopCh:   make(chan struct{}),
	}
}

// WithQueue sets an optional extraction queue for purging old completed tasks.
func (w *GCWorker) WithQueue(q port.ExtractionQueue) *GCWorker {
	w.queue = q
	return w
}

func (w *GCWorker) Start(ctx context.Context) {
	runWithRestart(ctx, w.stopCh, w.logger, "memory.gc_worker", w.run)
}

func (w *GCWorker) run(ctx context.Context) {
	w.logger.Info("memory.gc_worker.start")
	ticker := time.NewTicker(constants.MemoryGCInterval)
	defer ticker.Stop()
	w.RunOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("memory.gc_worker.context_cancelled")
			return
		case <-w.stopCh:
			w.logger.Info("memory.gc_worker.stopped")
			return
		case <-ticker.C:
			w.RunOnce(ctx)
		}
	}
}

// RunOnce performs a single GC pass with panic recovery.
func (w *GCWorker) RunOnce(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("memory.gc_worker.panic",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
	}()

	if w.queue != nil {
		n, err := w.queue.DeleteOldCompleted(ctx, w.tenantID, constants.MemoryGCQueueRetentionDays)
		if err != nil {
			w.logger.Error("memory.gc_worker.delete_old_completed_failed",
				zap.String("tenant_id", w.tenantID), zap.Error(err))
		} else if n > 0 {
			w.logger.Info("memory.gc_worker.deleted_old_completed",
				zap.String("tenant_id", w.tenantID), zap.Int("count", n))
		}
	}
}

// Stop gracefully stops the worker (idempotent).
func (w *GCWorker) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
}
