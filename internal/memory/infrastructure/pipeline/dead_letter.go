package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"

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
	OriginalStream string    `json:"original_stream,omitempty"`
	OriginalSubj   string    `json:"original_subject"`
	Consumer       string    `json:"consumer,omitempty"`
	StreamSequence uint64    `json:"stream_sequence,omitempty"`
	Deliveries     uint64    `json:"deliveries,omitempty"`
	FailedAt       time.Time `json:"failed_at"`
}

type deadLetterDetails struct {
	Stage     string
	TenantID  string
	MessageID string
	ErrorCode string
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
		OriginalSubj: msg.Subject(),
		FailedAt:     time.Now().UTC(),
	}
	event.OriginalStream = meta.Stream
	event.Consumer = meta.Consumer
	event.StreamSequence = meta.Sequence.Stream
	event.Deliveries = meta.NumDelivered

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
	if maxDeliver > 0 && meta.NumDelivered >= uint64(maxDeliver) {
		return deadLetterWithHeartbeat(ctx, pub, msg, stopHeartbeat, details)
	}
	stopHeartbeat()
	return msg.NakWithDelay(constants.MemoryFetchBackoffBase)
}

func retryOrDeadLetter(
	ctx context.Context,
	pub dlqPublisher,
	msg jetstream.Msg,
	maxDeliver int,
	details deadLetterDetails,
) error {
	return retryOrDeadLetterWithHeartbeat(ctx, pub, msg, maxDeliver, func() {}, details)
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
