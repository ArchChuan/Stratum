package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"
)

// stubJetStream 嵌入 jetstream.JetStream 接口（nil 满足其余方法），只覆盖 Publish。
type stubJetStream struct {
	jetstream.JetStream
	publishedSubject string
	publishedData    []byte
	err              error
}

func (s *stubJetStream) Publish(ctx context.Context, subj string, data []byte, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	s.publishedSubject = subj
	s.publishedData = append([]byte(nil), data...)
	if s.err != nil {
		return nil, s.err
	}
	return &jetstream.PubAck{}, nil
}

func TestObservationEmitterAdapterPublishes(t *testing.T) {
	js := &stubJetStream{}
	adapter := &observationEmitterAdapter{js: js, logger: zap.NewNop()}
	evt := port.ObservationEvent{
		TenantID: "t1", TraceID: "trace-1", ExecutionID: "exec-1",
		AgentID: "agent-1", ResourceKind: "agent", ResourceID: "agent-1",
		CompletedAt: "2026-08-28T12:00:00Z",
	}
	if err := adapter.Emit(context.Background(), evt); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	wantSubject := "evaluation.observe.t1"
	if js.publishedSubject != wantSubject {
		t.Fatalf("subject = %q, want %q", js.publishedSubject, wantSubject)
	}
	var decoded port.ObservationEvent
	if err := json.Unmarshal(js.publishedData, &decoded); err != nil {
		t.Fatalf("unmarshal published payload: %v", err)
	}
	if decoded.TraceID != "trace-1" || decoded.ResourceKind != "agent" {
		t.Fatalf("published payload mismatch: %+v", decoded)
	}
}

func TestObservationEmitterAdapterPropagatesError(t *testing.T) {
	js := &stubJetStream{err: errors.New("nats down")}
	adapter := &observationEmitterAdapter{js: js, logger: zap.NewNop()}
	err := adapter.Emit(context.Background(), port.ObservationEvent{TenantID: "t1"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
