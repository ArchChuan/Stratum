package observation

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

// dlqPublisher 抽象发布死信消息的 JetStream publisher。
// jetstream.JetStream 满足该接口（Publish(ctx, subject, data, ...PublishOpt)），wiring 直接传入。
type dlqPublisher interface {
	Publish(ctx context.Context, subject string, data []byte, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error)
}

// deadLetterDetails 死信元数据。
type deadLetterDetails struct {
	Stage     string
	TenantID  string
	MessageID string
	TraceID   string
	ErrorCode string
}

// DeadLetterEvent 死信事件载荷（发布到 DLQ 流，供复盘/重放）。
type DeadLetterEvent struct {
	Stage        string `json:"stage"`
	TenantID     string `json:"tenant_id"`
	MessageID    string `json:"message_id"`
	TraceID      string `json:"trace_id"`
	ErrorCode    string `json:"error_code"`
	OriginalSubj string `json:"original_subject"`
	Payload      string `json:"payload"`
}

// tenantFromObservationSubject 从 subject "evaluation.observe.<tenant>" 提取 tenantID。
// 解析失败返回 ""（仅用于死信元数据标注，不影响 DLQ 发布）。
func tenantFromObservationSubject(subject string) string {
	parts := strings.Split(subject, ".")
	if len(parts) >= 3 {
		return parts[2]
	}
	return ""
}

// startProgressHeartbeat 周期发送 InProgress 保持消息 in-flight（judge 可能超过 AckWait，
// 防误判超时重投）。interval <= 0 时 noop。返回 stop func，可安全多次调用（sync.Once）。
func startProgressHeartbeat(msg jetstream.Msg, interval time.Duration) func() {
	if interval <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				_ = msg.InProgress()
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

// deadLetterWithHeartbeat 发布死信事件到 DLQ 流（带去重 msgID），随后 Term 原消息。
func deadLetterWithHeartbeat(ctx context.Context, pub dlqPublisher, msg jetstream.Msg, stopHeartbeat func(), details deadLetterDetails) error {
	event := DeadLetterEvent{
		Stage: details.Stage, TenantID: details.TenantID, MessageID: details.MessageID,
		TraceID: details.TraceID, ErrorCode: details.ErrorCode,
		OriginalSubj: msg.Subject(), Payload: string(msg.Data()),
	}
	data, err := json.Marshal(event)
	if err != nil {
		stopHeartbeat()
		return fmt.Errorf("marshal dead letter: %w", err)
	}
	subject := constants.ObservationDLQSubject
	if _, err := pub.Publish(ctx, subject, data, jetstream.WithMsgID(fmt.Sprintf("%s-%s", details.Stage, details.MessageID))); err != nil {
		stopHeartbeat()
		return fmt.Errorf("publish dead letter: %w", err)
	}
	stopHeartbeat()
	return msg.TermWithReason(details.ErrorCode)
}

// retryOrDeadLetterWithHeartbeat 按重投计数决定：未达上限 NakWithDelay 重投，达上限进 DLQ。
// 返回 (是否进 DLQ, error)。
func retryOrDeadLetterWithHeartbeat(ctx context.Context, pub dlqPublisher, msg jetstream.Msg, maxDeliver int, stopHeartbeat func(), details deadLetterDetails) (bool, error) {
	meta, err := msg.Metadata()
	if err != nil {
		stopHeartbeat()
		return false, fmt.Errorf("msg metadata: %w", err)
	}
	if meta.NumDelivered >= uint64(maxDeliver) { //nolint:gosec // G115: maxDeliver 由常量控制（3），恒为正小值
		return true, deadLetterWithHeartbeat(ctx, pub, msg, stopHeartbeat, details)
	}
	stopHeartbeat()
	if err := msg.NakWithDelay(constants.ObservationFetchBackoffBase); err != nil {
		return false, fmt.Errorf("nak with delay: %w", err)
	}
	return false, nil
}
