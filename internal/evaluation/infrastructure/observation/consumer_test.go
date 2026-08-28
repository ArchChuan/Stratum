package observation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"
)

type stubProcessor struct {
	events []domain.ObservationReferenceEvent
	err    error
}

func (s *stubProcessor) Process(ctx context.Context, evt domain.ObservationReferenceEvent) error {
	if s.err != nil {
		return s.err
	}
	s.events = append(s.events, evt)
	return nil
}

func newTestWorker(proc *stubProcessor, pub *fakePublisher) *ObservationConsumerWorker {
	return &ObservationConsumerWorker{
		js: pub, processor: proc, metrics: observability.NoopMetrics{}, logger: zap.NewNop(),
		ackWait: 30 * time.Second, maxDeliver: constants.ObservationMaxDeliver,
	}
}

func TestConsumerProcessMessageAcks(t *testing.T) {
	proc := &stubProcessor{}
	msg := &fakeMsg{
		data:      []byte(`{"tenant_id":"t1","trace_id":"trace-1","execution_id":"exec-1","agent_id":"agent-1","resource_kind":"agent","resource_id":"agent-1","completed_at":"2026-08-28T12:00:00Z"}`),
		delivered: 1,
	}
	newTestWorker(proc, &fakePublisher{}).processMessage(context.Background(), msg)
	if msg.ackCount != 1 {
		t.Fatalf("expected ack, got ack=%d", msg.ackCount)
	}
	if len(proc.events) != 1 || proc.events[0].TraceID != "trace-1" {
		t.Fatalf("processor events mismatch: %+v", proc.events)
	}
}

func TestConsumerProcessMessageMalformedDeadLetters(t *testing.T) {
	pub := &fakePublisher{}
	msg := &fakeMsg{data: []byte("{not json"), delivered: 1}
	newTestWorker(&stubProcessor{}, pub).processMessage(context.Background(), msg)
	if msg.dlqCount != 1 || msg.termReason == "" {
		t.Fatalf("expected DLQ+Term on malformed, got dlq=%d reason=%q", msg.dlqCount, msg.termReason)
	}
	if len(pub.subjects) == 0 || pub.subjects[0] != constants.ObservationDLQSubject {
		t.Fatalf("expected DLQ publish to %s, got %v", constants.ObservationDLQSubject, pub.subjects)
	}
}

func TestConsumerProcessMessageErrorRedelivers(t *testing.T) {
	proc := &stubProcessor{err: errors.New("evidence down")}
	msg := &fakeMsg{
		data:      []byte(`{"tenant_id":"t1","trace_id":"x","resource_kind":"agent","resource_id":"a1"}`),
		delivered: constants.ObservationMaxDeliver - 1, // 未达上限 → Nak 重投
	}
	newTestWorker(proc, &fakePublisher{}).processMessage(context.Background(), msg)
	if msg.nakCount != 1 || msg.dlqCount != 0 {
		t.Fatalf("expected NakWithDelay redelivery, got nak=%d dlq=%d", msg.nakCount, msg.dlqCount)
	}
}

func TestConsumerProcessMessageErrorDeadLettersAfterMax(t *testing.T) {
	proc := &stubProcessor{err: errors.New("evidence down")}
	pub := &fakePublisher{}
	msg := &fakeMsg{
		data:      []byte(`{"tenant_id":"t1","trace_id":"x","resource_kind":"agent","resource_id":"a1"}`),
		delivered: constants.ObservationMaxDeliver, // 已达上限 → DLQ
	}
	newTestWorker(proc, pub).processMessage(context.Background(), msg)
	if msg.dlqCount != 1 || msg.termReason == "" {
		t.Fatalf("expected DLQ+Term after max deliver, got dlq=%d reason=%q", msg.dlqCount, msg.termReason)
	}
	if len(pub.subjects) == 0 {
		t.Fatal("expected DLQ publish")
	}
}

type fakeMsg struct {
	jetstream.Msg // 嵌入接口零实现，编译期校验签名
	data          []byte
	delivered     int
	ackCount      int
	nakCount      int
	dlqCount      int
	termReason    string
}

func (m *fakeMsg) Data() []byte         { return m.data }
func (m *fakeMsg) Subject() string      { return "evaluation.observe.t1" }
func (m *fakeMsg) Headers() nats.Header { return nil }
func (m *fakeMsg) Ack() error           { m.ackCount++; return nil }
func (m *fakeMsg) Nak() error           { m.nakCount++; return nil }
func (m *fakeMsg) NakWithDelay(delay time.Duration) error {
	m.nakCount++
	return nil
}
func (m *fakeMsg) InProgress() error { return nil }
func (m *fakeMsg) TermWithReason(reason string) error {
	m.dlqCount++
	m.termReason = reason
	return nil
}
func (m *fakeMsg) Metadata() (*jetstream.MsgMetadata, error) {
	return &jetstream.MsgMetadata{NumDelivered: uint64(m.delivered)}, nil
}

type fakePublisher struct {
	subjects []string
	datas    [][]byte
	err      error
}

func (p *fakePublisher) Publish(ctx context.Context, subject string, data []byte, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	if p.err != nil {
		return nil, p.err
	}
	p.subjects = append(p.subjects, subject)
	p.datas = append(p.datas, data)
	return &jetstream.PubAck{}, nil
}
