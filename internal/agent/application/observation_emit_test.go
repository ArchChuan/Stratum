package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"go.uber.org/zap"
)

type stubEmitter struct {
	called int
	last   port.ObservationEvent
	err    error
}

func (s *stubEmitter) Emit(_ context.Context, evt port.ObservationEvent) error {
	s.called++
	s.last = evt
	return s.err
}

func newTestServiceWithEmitter(e port.ObservationEmitter) *AgentService {
	return NewAgentService(AgentServiceDeps{Logger: zap.NewNop(), ObservationEmitter: e})
}

func TestEmitObservationPostsEvent(t *testing.T) {
	emitter := &stubEmitter{}
	s := newTestServiceWithEmitter(emitter)
	s.emitObservation(context.Background(), ExecMeta{TenantID: "t1", TraceID: "trace-1"}, "agent-1", "exec-1", &AgentResult{Output: "ok"})

	if emitter.called != 1 {
		t.Fatalf("Emit called %d times, want 1", emitter.called)
	}
	evt := emitter.last
	if evt.TenantID != "t1" || evt.TraceID != "trace-1" || evt.ExecutionID != "exec-1" {
		t.Fatalf("event identity mismatch: %+v", evt)
	}
	if evt.AgentID != "agent-1" || evt.ResourceKind != "agent" || evt.ResourceID != "agent-1" {
		t.Fatalf("event resource mismatch: %+v", evt)
	}
	if evt.CompletedAt == "" {
		t.Fatal("completed_at must be set")
	}
}

func TestEmitObservationNilEmitterNoPanic(t *testing.T) {
	s := newTestServiceWithEmitter(nil)
	s.emitObservation(context.Background(), ExecMeta{TenantID: "t1"}, "agent-1", "exec-1", &AgentResult{Output: "ok"}) // 不得 panic
}

func TestEmitObservationNilResultSkips(t *testing.T) {
	emitter := &stubEmitter{}
	s := newTestServiceWithEmitter(emitter)
	s.emitObservation(context.Background(), ExecMeta{TenantID: "t1"}, "agent-1", "exec-1", nil)
	if emitter.called != 0 {
		t.Fatalf("Emit called %d times for nil result, want 0", emitter.called)
	}
}

func TestEmitObservationFailureDoesNotPropagate(t *testing.T) {
	emitter := &stubEmitter{err: errors.New("nats down")}
	s := newTestServiceWithEmitter(emitter)
	s.emitObservation(context.Background(), ExecMeta{TenantID: "t1"}, "agent-1", "exec-1", &AgentResult{Output: "ok"}) // 失败仅记日志
}
