package pipeline

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

// ReflectionWorker 消费 memory.reflection.{tenant} 上的反思任务，调用
// ReflectionService.ReflectAndPersist 后 Ack；失败按 MaxDeliver 重投，
// 耗尽进入 memory.dlq。任务结束时由 agent 主路径 fail-open 入队。
type ReflectionWorker struct {
	consumer   jetstream.Consumer
	js         dlqPublisher
	service    port.ReflectionService
	logger     *zap.Logger
	guard      consumerStopGuard
	ackWait    time.Duration
	maxDeliver int
}

func NewReflectionWorker(
	consumer jetstream.Consumer,
	js dlqPublisher,
	service port.ReflectionService,
	logger *zap.Logger,
	ackWait time.Duration,
	maxDeliver int,
) *ReflectionWorker {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ReflectionWorker{
		consumer:   consumer,
		js:         js,
		service:    service,
		logger:     logger,
		guard:      newConsumerStopGuard(),
		ackWait:    ackWait,
		maxDeliver: maxDeliver,
	}
}

func (w *ReflectionWorker) Start(ctx context.Context) {
	runConsumerLoop(ctx, w.guard.stopCh, w.logger, "reflection", w.consumer, w.safeProcessMessage)
}

func (w *ReflectionWorker) Stop() { w.guard.Stop() }

func (w *ReflectionWorker) safeProcessMessage(ctx context.Context, msg jetstream.Msg) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("memory.reflection.panic",
				zap.Any("panic", r),
				zap.String("subject", msg.Subject()),
				zap.Stack("stack"))
			_ = msg.Nak()
		}
	}()
	w.processMessage(ctx, msg)
}

func (w *ReflectionWorker) processMessage(ctx context.Context, msg jetstream.Msg) {
	start := time.Now()
	stopHeartbeat := startProgressHeartbeat(msg, w.ackWait/2)
	defer stopHeartbeat()

	var task port.ReflectionTask
	if err := json.Unmarshal(msg.Data(), &task); err != nil {
		w.logger.Error("memory.reflection.unmarshal", zap.Error(err))
		if dlqErr := deadLetterWithHeartbeat(ctx, w.js, msg, stopHeartbeat, deadLetterDetails{
			Stage: "reflection", ErrorCode: "invalid_task",
		}); dlqErr != nil {
			w.logger.Error("memory.reflection.dlq", zap.Error(dlqErr))
		}
		return
	}

	tenantID := task.TenantID
	if tenantID == "" {
		tenantID = tenantFromMemorySubject(msg.Subject())
	}
	if err := w.service.ReflectAndPersist(ctx, &task); err != nil {
		w.logger.Warn("memory.reflection.reflect_failed",
			zap.String("tenant_id", tenantID),
			zap.String("execution_id", task.ExecutionID),
			zap.Error(err))
		if _, rerr := retryOrDeadLetterWithHeartbeat(ctx, w.js, msg, w.maxDeliver, stopHeartbeat, deadLetterDetails{
			Stage: "reflection", TenantID: tenantID, MessageID: task.ExecutionID,
			ErrorCode: "reflect_failed",
		}); rerr != nil {
			w.logger.Error("memory.reflection.retry_or_dlq", zap.Error(rerr))
		}
		return
	}

	stopHeartbeat()
	_ = msg.Ack()
	w.logger.Debug("memory.reflection.success",
		zap.String("tenant_id", tenantID),
		zap.String("execution_id", task.ExecutionID),
		zap.Int64("latency_ms", time.Since(start).Milliseconds()))
}
