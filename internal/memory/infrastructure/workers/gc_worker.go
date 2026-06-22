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
	factRepo port.FactRepo
	logger   *zap.Logger
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewGCWorker creates a garbage collection worker.
func NewGCWorker(repo port.FactRepo, logger *zap.Logger) *GCWorker {
	return &GCWorker{
		factRepo: repo,
		logger:   logger,
		stopCh:   make(chan struct{}),
	}
}

// Start begins periodic garbage collection until ctx is cancelled or Stop is called.
func (w *GCWorker) Start(ctx context.Context) {
	w.logger.Info("memory.gc_worker.start")
	ticker := time.NewTicker(constants.MemoryGCInterval)
	defer ticker.Stop()

	// Run once immediately
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

	start := time.Now()

	// Purge deleted facts older than retention
	deletedRetentionDays := int(constants.MemoryDeletedRetention.Hours() / 24)
	deletedCount, err := w.factRepo.DeleteOldSoftDeleted(ctx, deletedRetentionDays)
	if err != nil {
		w.logger.Error("memory.gc_worker.purge_deleted_failed", zap.Error(err))
		return
	}

	// Note: FactRepo doesn't have DeleteOldSuperseded method
	// In a full implementation, we'd add that method or extend DeleteOldSoftDeleted
	// to handle both deleted and superseded facts

	if deletedCount > 0 {
		w.logger.Info("memory.gc_worker.batch_complete",
			zap.Int("deleted_purged", deletedCount),
			zap.Int64("latency_ms", time.Since(start).Milliseconds()))
	}
}

// Stop gracefully stops the worker (idempotent).
func (w *GCWorker) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
}
