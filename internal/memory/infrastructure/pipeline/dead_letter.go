package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

type dlqPublisher interface {
	Publish(context.Context, string, []byte, ...jetstream.PublishOpt) (*jetstream.PubAck, error)
}

type DeadLetterEvent struct {
	MessageID      string    `json:"message_id,omitempty"`
	TenantID       string    `json:"tenant_id"`
	Stage          string    `json:"stage"`
	ErrorCode      string    `json:"error_code"`
	TraceID        string    `json:"trace_id,omitempty"`
	OriginalStream string    `json:"original_stream,omitempty"`
	OriginalSubj   string    `json:"original_subject"`
	Consumer       string    `json:"consumer,omitempty"`
	StreamSequence uint64    `json:"stream_sequence,omitempty"`
	Deliveries     uint64    `json:"deliveries,omitempty"`
	FailedAt       time.Time `json:"failed_at"`
	// Payload 是原始消息 body（TermWithReason 销毁原消息前读出），
	// 供定向重放重建消息。
	Payload []byte `json:"payload,omitempty"`
}

type deadLetterDetails struct {
	Stage     string
	TenantID  string
	MessageID string
	ErrorCode string
	TraceID   string
}

func deadLetter(ctx context.Context, pub dlqPublisher, msg jetstream.Msg, details deadLetterDetails) error {
	return deadLetterWithHeartbeat(ctx, pub, msg, func() {}, details)
}

func deadLetterWithHeartbeat(
	ctx context.Context,
	pub dlqPublisher,
	msg jetstream.Msg,
	stopHeartbeat func(),
	details deadLetterDetails,
) error {
	meta, err := msg.Metadata()
	if err != nil {
		stopHeartbeat()
		_ = msg.Nak()
		return fmt.Errorf("read message metadata: %w", err)
	}
	if meta == nil {
		stopHeartbeat()
		_ = msg.Nak()
		return fmt.Errorf("read message metadata: empty metadata")
	}
	if details.TenantID == "" {
		details.TenantID = tenantFromMemorySubject(msg.Subject())
	}
	if details.TenantID == "" {
		details.TenantID = "unknown"
	}

	event := DeadLetterEvent{
		MessageID:    details.MessageID,
		TenantID:     details.TenantID,
		Stage:        details.Stage,
		ErrorCode:    details.ErrorCode,
		TraceID:      details.TraceID,
		OriginalSubj: msg.Subject(),
		FailedAt:     time.Now().UTC(),
	}
	event.OriginalStream = meta.Stream
	event.Consumer = meta.Consumer
	event.StreamSequence = meta.Sequence.Stream
	event.Deliveries = meta.NumDelivered
	event.Payload = append([]byte(nil), msg.Data()...)

	payload, err := json.Marshal(event)
	if err != nil {
		stopHeartbeat()
		_ = msg.Nak()
		return fmt.Errorf("marshal dead letter: %w", err)
	}

	publishCtx, cancel := context.WithTimeout(ctx, constants.MemoryOutboxPublishTimeout)
	defer cancel()
	dedupID := deadLetterDedupID(event)
	if _, err := pub.Publish(
		publishCtx,
		fmt.Sprintf("%s.%s", constants.MemoryDLQSubject, details.TenantID),
		payload,
		jetstream.WithMsgID(dedupID),
	); err != nil {
		stopHeartbeat()
		_ = msg.Nak()
		return fmt.Errorf("publish dead letter: %w", err)
	}
	stopHeartbeat()
	if err := msg.TermWithReason(details.ErrorCode); err != nil {
		_ = msg.Nak()
		return fmt.Errorf("terminate original message: %w", err)
	}
	dlqTotal.WithLabelValues(details.TenantID, details.Stage).Inc()
	return nil
}

func deadLetterDedupID(event DeadLetterEvent) string {
	return fmt.Sprintf("dlq:%s:%d", event.OriginalStream, event.StreamSequence)
}

func retryOrDeadLetterWithHeartbeat(
	ctx context.Context,
	pub dlqPublisher,
	msg jetstream.Msg,
	maxDeliver int,
	stopHeartbeat func(),
	details deadLetterDetails,
) (bool, error) {
	meta, err := msg.Metadata()
	if err != nil {
		stopHeartbeat()
		_ = msg.Nak()
		return false, fmt.Errorf("read message metadata: %w", err)
	}
	if meta == nil {
		stopHeartbeat()
		_ = msg.Nak()
		return false, fmt.Errorf("read message metadata: empty metadata")
	}
	if maxDeliver > 0 && meta.NumDelivered >= uint64(maxDeliver) {
		if err := deadLetterWithHeartbeat(ctx, pub, msg, stopHeartbeat, details); err != nil {
			return false, err
		}
		return true, nil
	}
	stopHeartbeat()
	return false, msg.NakWithDelay(constants.MemoryFetchBackoffBase)
}

func retryOrDeadLetter(
	ctx context.Context,
	pub dlqPublisher,
	msg jetstream.Msg,
	maxDeliver int,
	details deadLetterDetails,
) (bool, error) {
	return retryOrDeadLetterWithHeartbeat(ctx, pub, msg, maxDeliver, func() {}, details)
}

// retryOrDeadLetterWithOrphan 执行重试/DLQ 决策；仅当消息被永久终止
// （dead-lettered）后删除 embedder 已写入的孤儿向量。调用点不感知
// dead-letter 判定细节，保持 worker 主流程复杂度在门禁内。
func retryOrDeadLetterWithOrphan(
	ctx context.Context,
	pub dlqPublisher,
	msg jetstream.Msg,
	maxDeliver int,
	stopHeartbeat func(),
	details deadLetterDetails,
	cleaner entryVectorDeleter,
	logger *zap.Logger,
	tenantID, messageID, event string,
) {
	deadLettered, retryErr := retryOrDeadLetterWithHeartbeat(ctx, pub, msg, maxDeliver, stopHeartbeat, details)
	if retryErr != nil {
		logger.Error(event, zap.Error(retryErr))
		return
	}
	if deadLettered {
		deleteOrphanEntryVector(ctx, cleaner, logger, tenantID, messageID)
	}
}

// deadLetterWithOrphan 永久终止消息（不重试）并删除孤儿向量；dead-letter
// 失败时以 event 记录错误。
func deadLetterWithOrphan(
	ctx context.Context,
	pub dlqPublisher,
	msg jetstream.Msg,
	stopHeartbeat func(),
	details deadLetterDetails,
	cleaner entryVectorDeleter,
	logger *zap.Logger,
	tenantID, messageID, event string,
) {
	if dlqErr := deadLetterWithHeartbeat(ctx, pub, msg, stopHeartbeat, details); dlqErr != nil {
		logger.Error(event, zap.Error(dlqErr))
		return
	}
	deleteOrphanEntryVector(ctx, cleaner, logger, tenantID, messageID)
}

// entryVectorDeleter removes raw-turn vectors by id across a tenant's memory
// collections. The embedder writes the vector before the enrich stage runs, so
// a permanently dead-lettered enriched event leaves an orphan vector with no
// memory_entries row; deletion here closes that gap (replay recreates it).
type entryVectorDeleter interface {
	DeleteEntryVectors(ctx context.Context, tenantID string, ids []string) error
}

// deleteOrphanEntryVector removes the embedder-written vector only after the
// message was actually terminated (dead-lettered), never on retry. Failures
// are ERROR-logged: the message is already gone so there is no safe way to
// propagate; the recall PG-side filter keeps the orphan invisible to users.
func deleteOrphanEntryVector(ctx context.Context, cleaner entryVectorDeleter, logger *zap.Logger, tenantID, messageID string) {
	if cleaner == nil || messageID == "" {
		return
	}
	if err := cleaner.DeleteEntryVectors(ctx, tenantID, []string{messageID}); err != nil {
		logger.Error("memory.pipeline.orphan_vector_delete_failed",
			zap.String("tenant_id", tenantID),
			zap.String("message_id", messageID),
			zap.Error(err))
	}
}

func startProgressHeartbeat(msg jetstream.Msg, interval time.Duration) func() {
	if interval <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = msg.InProgress()
			case <-done:
				return
			}
		}
	}()
	return func() {
		once.Do(func() { close(done) })
		<-stopped
	}
}

func tenantFromMemorySubject(subject string) string {
	parts := strings.Split(subject, ".")
	if len(parts) != 3 || parts[0] != "memory" {
		return ""
	}
	return parts[2]
}
