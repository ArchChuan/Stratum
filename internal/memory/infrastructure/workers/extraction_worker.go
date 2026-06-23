package workers

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// FactExtractor defines the minimal interface for fact extraction.
type FactExtractor interface {
	ExtractFacts(ctx context.Context, req *application.ExtractFactsRequest) error
}

// ExtractionWorker polls the extraction queue and processes tasks.
type ExtractionWorker struct {
	queue    port.ExtractionQueue
	service  FactExtractor
	logger   *zap.Logger
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewExtractionWorker creates an extraction worker.
func NewExtractionWorker(queue port.ExtractionQueue, service FactExtractor, logger *zap.Logger) *ExtractionWorker {
	return &ExtractionWorker{
		queue:   queue,
		service: service,
		logger:  logger,
		stopCh:  make(chan struct{}),
	}
}

// Start begins polling the queue until ctx is cancelled or Stop is called.
func (w *ExtractionWorker) Start(ctx context.Context) {
	w.logger.Info("memory.extraction_worker.start")
	backoff := constants.MemoryFetchBackoffBase

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("memory.extraction_worker.context_cancelled")
			return
		case <-w.stopCh:
			w.logger.Info("memory.extraction_worker.stopped")
			return
		default:
		}

		task, err := w.queue.Dequeue(ctx)
		if err != nil {
			w.logger.Error("memory.extraction_worker.dequeue_failed", zap.Error(err))
			SleepCtx(ctx, w.stopCh, backoff)
			backoff = min(backoff*2, constants.MemoryFetchBackoffMax)
			continue
		}

		if task == nil {
			// No task available, reset backoff and sleep
			backoff = constants.MemoryFetchBackoffBase
			if !SleepCtx(ctx, w.stopCh, backoff) {
				return
			}
			continue
		}

		// Process task
		backoff = constants.MemoryFetchBackoffBase
		w.processTask(ctx, task)
	}
}

// processTask handles a single extraction task with panic recovery.
func (w *ExtractionWorker) processTask(ctx context.Context, task *port.ExtractionTask) {
	start := time.Now()

	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("memory.extraction_worker.panic",
				zap.Int64("task_id", task.ID),
				zap.Any("panic", r),
				zap.Stack("stack"))
			_ = w.queue.MarkFailed(ctx, task.ID, fmt.Sprintf("panic: %v", r))
			WorkerMessagesProcessed.WithLabelValues("extraction", task.UserID, "panic").Inc()
		}
	}()

	req := &application.ExtractFactsRequest{
		TenantID: "", // ExtractionTask doesn't carry tenant_id; service must infer or we need to extend the schema
		UserID:   task.UserID,
		AgentID:  task.AgentID,
		Messages: []application.MessageDTO{
			{Role: "user", Content: task.Content},
		},
	}

	err := w.service.ExtractFacts(ctx, req)
	if err != nil {
		w.logger.Warn("memory.extraction_worker.extract_failed",
			zap.Int64("task_id", task.ID),
			zap.Error(err))
		_ = w.queue.MarkFailed(ctx, task.ID, err.Error())
		WorkerMessagesProcessed.WithLabelValues("extraction", task.UserID, "error").Inc()
		return
	}

	if err := w.queue.MarkCompleted(ctx, task.ID); err != nil {
		w.logger.Error("memory.extraction_worker.mark_completed_failed",
			zap.Int64("task_id", task.ID),
			zap.Error(err))
		return
	}

	WorkerMessagesProcessed.WithLabelValues("extraction", task.UserID, "success").Inc()
	WorkerProcessingDuration.WithLabelValues("extraction", task.UserID).Observe(time.Since(start).Seconds())
	w.logger.Debug("memory.extraction_worker.task_completed",
		zap.Int64("task_id", task.ID),
		zap.Int64("latency_ms", time.Since(start).Milliseconds()))
}

// Stop gracefully stops the worker (idempotent).
func (w *ExtractionWorker) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
}
