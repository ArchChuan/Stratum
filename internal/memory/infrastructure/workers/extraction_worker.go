package workers

import (
	"context"
	"encoding/json"
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

// ExtractionWorker polls the extraction queue for a single tenant and processes tasks.
type ExtractionWorker struct {
	tenantID string
	queue    port.ExtractionQueue
	service  FactExtractor
	logger   *zap.Logger
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewExtractionWorker creates an extraction worker for a specific tenant.
func NewExtractionWorker(tenantID string, queue port.ExtractionQueue, service FactExtractor, logger *zap.Logger) *ExtractionWorker {
	return &ExtractionWorker{
		tenantID: tenantID,
		queue:    queue,
		service:  service,
		logger:   logger,
		stopCh:   make(chan struct{}),
	}
}

func (w *ExtractionWorker) Start(ctx context.Context) {
	runWithRestart(ctx, w.stopCh, w.logger, "memory.extraction_worker", w.run)
}

func (w *ExtractionWorker) run(ctx context.Context) {
	w.logger.Info("memory.extraction_worker.start", zap.String("tenant_id", w.tenantID))
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

		task, err := w.queue.Dequeue(ctx, w.tenantID)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			w.logger.Error("memory.extraction_worker.dequeue_failed", zap.Error(err))
			SleepCtx(ctx, w.stopCh, backoff)
			backoff = min(backoff*2, constants.MemoryFetchBackoffMax)
			continue
		}

		if task == nil {
			backoff = constants.MemoryFetchBackoffBase
			if !SleepCtx(ctx, w.stopCh, backoff) {
				return
			}
			continue
		}

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
			_ = w.queue.MarkFailed(ctx, task.TenantID, task.ID, fmt.Sprintf("panic: %v", r))
			incWorkerMessages("extraction", task.UserID, "panic")
		}
	}()

	var msgs []application.MessageDTO
	if err := json.Unmarshal([]byte(task.Content), &msgs); err != nil {
		msgs = []application.MessageDTO{{Role: "user", Content: task.Content}}
	}

	req := &application.ExtractFactsRequest{
		TenantID:       task.TenantID,
		UserID:         task.UserID,
		AgentID:        task.AgentID,
		ConversationID: task.ConversationID,
		Scope:          task.Scope,
		Messages:       msgs,
	}

	err := w.service.ExtractFacts(ctx, req)
	if err != nil {
		w.logger.Warn("memory.extraction_worker.extract_failed",
			zap.Int64("task_id", task.ID),
			zap.Error(err))
		_ = w.queue.MarkFailed(ctx, task.TenantID, task.ID, err.Error())
		incWorkerMessages("extraction", task.UserID, "error")
		return
	}

	if err := w.queue.MarkCompleted(ctx, task.TenantID, task.ID); err != nil {
		w.logger.Error("memory.extraction_worker.mark_completed_failed",
			zap.Int64("task_id", task.ID),
			zap.Error(err))
		return
	}

	incWorkerMessages("extraction", task.UserID, "success")
	observeWorkerDuration("extraction", task.UserID, time.Since(start).Seconds())
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
