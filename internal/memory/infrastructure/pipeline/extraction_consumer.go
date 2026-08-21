package pipeline

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

// ExtractionConsumerWorker 消费 memory.extraction.{tenant} 上的提取任务，
// 调用 FactExtractionService.ExtractFacts 后 Ack；失败按 MaxDeliver 重投，
// 耗尽进入 memory.dlq。取代原 PG memory_extraction_queue 的 Dequeue/Mark 语义。
type ExtractionConsumerWorker struct {
	consumer   jetstream.Consumer
	js         dlqPublisher
	service    port.FactExtractionService
	logger     *zap.Logger
	guard      consumerStopGuard
	ackWait    time.Duration
	maxDeliver int
}

func NewExtractionConsumerWorker(
	consumer jetstream.Consumer,
	js dlqPublisher,
	service port.FactExtractionService,
	logger *zap.Logger,
	ackWait time.Duration,
	maxDeliver int,
) *ExtractionConsumerWorker {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ExtractionConsumerWorker{
		consumer:   consumer,
		js:         js,
		service:    service,
		logger:     logger,
		guard:      newConsumerStopGuard(),
		ackWait:    ackWait,
		maxDeliver: maxDeliver,
	}
}

func (w *ExtractionConsumerWorker) Start(ctx context.Context) {
	runConsumerLoop(ctx, w.guard.stopCh, w.logger, "extraction", w.consumer, w.safeProcessMessage)
}

func (w *ExtractionConsumerWorker) Stop() { w.guard.Stop() }

func (w *ExtractionConsumerWorker) safeProcessMessage(ctx context.Context, msg jetstream.Msg) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("memory.extraction.panic",
				zap.Any("panic", r),
				zap.String("subject", msg.Subject()),
				zap.Stack("stack"))
			_ = msg.Nak()
		}
	}()
	w.processMessage(ctx, msg)
}

func (w *ExtractionConsumerWorker) processMessage(ctx context.Context, msg jetstream.Msg) {
	start := time.Now()
	stopHeartbeat := startProgressHeartbeat(msg, w.ackWait/2)
	defer stopHeartbeat()

	var task port.ExtractionTask
	if err := json.Unmarshal(msg.Data(), &task); err != nil {
		w.logger.Error("memory.extraction.unmarshal", zap.Error(err))
		if dlqErr := deadLetterWithHeartbeat(ctx, w.js, msg, stopHeartbeat, deadLetterDetails{
			Stage: "extract", ErrorCode: "invalid_task",
		}); dlqErr != nil {
			w.logger.Error("memory.extraction.dlq", zap.Error(dlqErr))
		}
		return
	}

	tenantID := task.TenantID
	if tenantID == "" {
		tenantID = tenantFromMemorySubject(msg.Subject())
	}

	var msgs []port.MessageDTO
	if err := json.Unmarshal([]byte(task.Content), &msgs); err != nil {
		msgs = []port.MessageDTO{{Role: "user", Content: task.Content}}
	}
	req := &port.ExtractFactsRequest{
		TenantID:        tenantID,
		UserID:          task.UserID,
		AgentID:         task.AgentID,
		ConversationID:  task.ConversationID,
		Scope:           task.Scope,
		SourceMessageID: task.MessageID,
		SourceTaskID:    task.ID,
		Messages:        msgs,
	}
	if err := w.service.ExtractFacts(ctx, req); err != nil {
		w.logger.Warn("memory.extraction.extract_failed",
			zap.String("tenant_id", tenantID),
			zap.String("message_id", task.MessageID),
			zap.String("trace_id", task.TraceID),
			zap.Error(err))
		if _, rerr := retryOrDeadLetterWithHeartbeat(ctx, w.js, msg, w.maxDeliver, stopHeartbeat, deadLetterDetails{
			Stage: "extract", TenantID: tenantID, MessageID: task.MessageID,
			ErrorCode: "extract_failed", TraceID: task.TraceID,
		}); rerr != nil {
			w.logger.Error("memory.extraction.retry_or_dlq", zap.Error(rerr))
		}
		return
	}

	stopHeartbeat()
	_ = msg.Ack()
	w.logger.Debug("memory.extraction.success",
		zap.String("tenant_id", tenantID),
		zap.String("message_id", task.MessageID),
		zap.Int64("latency_ms", time.Since(start).Milliseconds()))
}
