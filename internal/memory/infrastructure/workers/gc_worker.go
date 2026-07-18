package workers

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/timeutil"
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

	w.purgeSupersededFacts(ctx)
}

// purgeSupersededFacts hard-deletes superseded facts older than the retention
// window in bounded batches. Superseded facts have been replaced by newer ones,
// so they are pure dead weight once past retention; archived facts are durable
// long-term memory and are deliberately never purged here. Batching caps the
// blast radius of any single pass and lets ticker cadence drain a large backlog
// over time rather than issuing one unbounded DELETE.
func (w *GCWorker) purgeSupersededFacts(ctx context.Context) {
	if w.factRepo == nil {
		return
	}
	cutoff := timeutil.Now().Add(-constants.MemorySupersededRetention)
	total := 0
	for {
		if ctx.Err() != nil {
			return
		}
		n, err := w.factRepo.PurgeSuperseded(ctx, w.tenantID, cutoff, constants.MemoryGCBatchSize)
		if err != nil {
			w.logger.Error("memory.gc_worker.purge_superseded_failed",
				zap.String("tenant_id", w.tenantID), zap.Error(err))
			return
		}
		total += n
		if n < constants.MemoryGCBatchSize {
			break
		}
	}
	if total > 0 {
		w.logger.Info("memory.gc_worker.purged_superseded_facts",
			zap.String("tenant_id", w.tenantID), zap.Int("count", total))
	}
}

// Stop gracefully stops the worker (idempotent).
func (w *GCWorker) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
}
