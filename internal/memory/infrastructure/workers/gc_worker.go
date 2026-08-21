package workers

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/timeutil"
)

// GCWorker periodically purges old deleted and superseded facts.
type GCWorker struct {
	tenantID    string
	factRepo    port.FactRepo
	memoryRepo  port.MemoryRepo // optional: episodic TTL cleanup
	vectorStore port.VectorStore
	queue       port.ExtractionQueue // optional: purge old completed queue tasks
	logger      *zap.Logger
	stopCh      chan struct{}
	stopOnce    sync.Once
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

// WithMemoryRepo wires the memory entry repo used for episodic TTL cleanup.
func (w *GCWorker) WithMemoryRepo(r port.MemoryRepo) *GCWorker {
	w.memoryRepo = r
	return w
}

// WithVectorStore wires the vector store used to delete vectors of purged
// facts and expired episodic entries.
func (w *GCWorker) WithVectorStore(vs port.VectorStore) *GCWorker {
	w.vectorStore = vs
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
			incWorkerPanics("gc_worker")
		}
	}()

	start := time.Now()
	hasErr := false

	if w.queue != nil {
		n, err := w.queue.DeleteOldCompleted(ctx, w.tenantID, constants.MemoryGCQueueRetentionDays)
		if err != nil {
			w.logger.Error("memory.gc_worker.delete_old_completed_failed",
				zap.String("tenant_id", w.tenantID), zap.Error(err))
			hasErr = true
		} else if n > 0 {
			w.logger.Info("memory.gc_worker.deleted_old_completed",
				zap.String("tenant_id", w.tenantID), zap.Int("count", n))
		}
	}

	w.purgeSupersededFacts(ctx)
	w.purgeExpiredEntries(ctx)
	status := "success"
	if hasErr {
		status = "error"
	}
	incWorkerMessages("gc", w.tenantID, status)
	observeWorkerDuration("gc", w.tenantID, time.Since(start).Seconds())
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
		ids, err := w.factRepo.PurgeSuperseded(ctx, w.tenantID, cutoff, constants.MemoryGCBatchSize)
		if err != nil {
			w.logger.Error("memory.gc_worker.purge_superseded_failed",
				zap.String("tenant_id", w.tenantID), zap.Error(err))
			return
		}
		total += len(ids)
		// 行先删、向量后删：向量删除失败会留下孤儿向量（召回侧的状态过滤
		// 保证不可见），此处 ERROR 暴露；重试窗口由下一次 GC 兜底。
		if err := w.deleteFactVectors(ctx, ids); err != nil {
			w.logger.Error("memory.gc_worker.purge_superseded_vector_failed",
				zap.String("tenant_id", w.tenantID), zap.Error(err))
		}
		if len(ids) < constants.MemoryGCBatchSize {
			break
		}
	}
	if total > 0 {
		w.logger.Info("memory.gc_worker.purged_superseded_facts",
			zap.String("tenant_id", w.tenantID), zap.Int("count", total))
	}
}

// deleteFactVectors removes vectors for the given fact ids across all
// memory_facts_ collections. nil-safe: without a wired vector store the GC
// skips deletion (rows are purged regardless).
func (w *GCWorker) deleteFactVectors(ctx context.Context, ids []string) error {
	if len(ids) == 0 || w.vectorStore == nil {
		return nil
	}
	return w.vectorStore.DeleteFactVectors(ctx, w.tenantID, ids)
}

// purgeExpiredEntries physically removes episodic raw turns past their
// retention: per-entry expires_at first, otherwise the 90d TTL cutoff.
// Vectors are deleted before rows so a failed row delete can be retried
// idempotently next pass; a failed vector delete aborts before rows are
// touched, keeping PG the source of truth.
func (w *GCWorker) purgeExpiredEntries(ctx context.Context) {
	if w.memoryRepo == nil || w.vectorStore == nil {
		return
	}
	now := timeutil.Now()
	cutoff := now.Add(-constants.MemoryEpisodicTTL)
	total := 0
	for {
		if ctx.Err() != nil {
			return
		}
		n, err := w.purgeExpiredBatch(ctx, now, cutoff)
		if err != nil {
			w.logger.Error("memory.gc_worker.purge_expired_failed",
				zap.String("tenant_id", w.tenantID), zap.Error(err))
			return
		}
		total += n
		if n < constants.MemoryGCBatchSize {
			break
		}
	}
	if total > 0 {
		w.logger.Info("memory.gc_worker.purged_expired_entries",
			zap.String("tenant_id", w.tenantID), zap.Int("count", total))
	}
}

// purgeExpiredBatch 处理一个有界批次：先删向量（幂等，失败可重试），再删
// PG 行；任一步失败都返回错误，由外层暴露并留待下轮 GC 重试，PG 始终是
// 事实源。
func (w *GCWorker) purgeExpiredBatch(ctx context.Context, now, cutoff time.Time) (int, error) {
	ids, err := w.memoryRepo.ListExpired(ctx, w.tenantID, now, cutoff, constants.MemoryGCBatchSize)
	if err != nil {
		return 0, fmt.Errorf("list expired entries: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if err := w.vectorStore.DeleteEntryVectors(ctx, w.tenantID, ids); err != nil {
		return 0, fmt.Errorf("delete expired vectors: %w", err)
	}
	if err := w.memoryRepo.DeleteByIDs(ctx, w.tenantID, ids); err != nil {
		return 0, fmt.Errorf("delete expired entries: %w", err)
	}
	return len(ids), nil
}

// Stop gracefully stops the worker (idempotent).
func (w *GCWorker) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
}
