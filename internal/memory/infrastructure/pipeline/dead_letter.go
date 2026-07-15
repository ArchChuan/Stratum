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
	meta, _ := msg.Metadata()
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
	if meta != nil {
		event.OriginalStream = meta.Stream
		event.Consumer = meta.Consumer
		event.StreamSequence = meta.Sequence.Stream
		event.Deliveries = meta.NumDelivered
	}

	payload, err := json.Marshal(event)
	if err != nil {
		_ = msg.Nak()
		return fmt.Errorf("marshal dead letter: %w", err)
	}

	publishCtx, cancel := context.WithTimeout(ctx, constants.MemoryOutboxPublishTimeout)
	defer cancel()
	dedupID := fmt.Sprintf("dlq:%s:%d:%s", event.OriginalStream, event.StreamSequence, event.ErrorCode)
	if _, err := pub.Publish(
		publishCtx,
		fmt.Sprintf("%s.%s", constants.MemoryDLQSubject, details.TenantID),
		payload,
		jetstream.WithMsgID(dedupID),
	); err != nil {
		_ = msg.Nak()
		return fmt.Errorf("publish dead letter: %w", err)
	}
	if err := msg.TermWithReason(details.ErrorCode); err != nil {
		_ = msg.Nak()
		return fmt.Errorf("terminate original message: %w", err)
	}
	dlqTotal.WithLabelValues(details.TenantID, details.Stage).Inc()
	return nil
}

func retryOrDeadLetter(
	ctx context.Context,
	pub dlqPublisher,
	msg jetstream.Msg,
	maxDeliver int,
	details deadLetterDetails,
) error {
	meta, err := msg.Metadata()
	if err == nil && maxDeliver > 0 && meta.NumDelivered >= uint64(maxDeliver) {
		return deadLetter(ctx, pub, msg, details)
	}
	return msg.NakWithDelay(constants.MemoryFetchBackoffBase)
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
