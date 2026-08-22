package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

type fakeJSPublisher struct {
	subjects []string
	payloads [][]byte
	err      error
}

func (f *fakeJSPublisher) Publish(_ context.Context, subject string, data []byte, _ ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	f.subjects = append(f.subjects, subject)
	f.payloads = append(f.payloads, append([]byte(nil), data...))
	if f.err != nil {
		return nil, f.err
	}
	return &jetstream.PubAck{}, nil
}

func TestNATSExtractionPublisher_Enqueue(t *testing.T) {
	js := &fakeJSPublisher{}
	p := NewNATSExtractionPublisher(js, zap.NewNop())
	task := &port.ExtractionTask{TenantID: "t1", MessageID: "msg-1", UserID: "u1", Content: `[{"role":"user","content":"hi"}]`}

	if err := p.Enqueue(context.Background(), "t1", task); err != nil {
		t.Fatal(err)
	}
	if len(js.subjects) != 1 || js.subjects[0] != "memory.extraction.t1" {
		t.Fatalf("subject=%v, want memory.extraction.t1", js.subjects)
	}
	var decoded port.ExtractionTask
	if err := json.Unmarshal(js.payloads[0], &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.MessageID != "msg-1" || decoded.TraceID == "" {
		t.Fatalf("unexpected decoded task: %#v", decoded)
	}
}

func TestNATSExtractionPublisher_EnqueueGuards(t *testing.T) {
	p := NewNATSExtractionPublisher(nil, zap.NewNop())
	if err := p.Enqueue(context.Background(), "t1", &port.ExtractionTask{MessageID: "m"}); err == nil {
		t.Fatal("nil js must fail")
	}
	js := &fakeJSPublisher{}
	p2 := NewNATSExtractionPublisher(js, zap.NewNop())
	if err := p2.Enqueue(context.Background(), "t1", &port.ExtractionTask{}); err == nil {
		t.Fatal("empty message_id must fail")
	}
	js.err = errors.New("nats down")
	if err := p2.Enqueue(context.Background(), "t1", &port.ExtractionTask{MessageID: "m"}); err == nil {
		t.Fatal("publish error must propagate")
	}
}

func TestNATSReflectionPublisher_Enqueue(t *testing.T) {
	js := &fakeJSPublisher{}
	p := NewNATSReflectionPublisher(js, zap.NewNop())
	task := &port.ReflectionTask{TenantID: "t1", UserID: "u1", ExecutionID: "exec-1", Skeleton: []byte(`{"execution_id":"exec-1"}`)}

	if err := p.Enqueue(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if len(js.subjects) != 1 || js.subjects[0] != "memory.reflection.t1" {
		t.Fatalf("subject=%v, want memory.reflection.t1", js.subjects)
	}
	var decoded port.ReflectionTask
	if err := json.Unmarshal(js.payloads[0], &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ExecutionID != "exec-1" {
		t.Fatalf("unexpected decoded task: %#v", decoded)
	}
}

func TestNATSReflectionPublisher_EnqueueGuards(t *testing.T) {
	p := NewNATSReflectionPublisher(nil, zap.NewNop())
	if err := p.Enqueue(context.Background(), &port.ReflectionTask{ExecutionID: "e"}); err == nil {
		t.Fatal("nil js must fail")
	}
	js := &fakeJSPublisher{}
	p2 := NewNATSReflectionPublisher(js, zap.NewNop())
	if err := p2.Enqueue(context.Background(), &port.ReflectionTask{}); err == nil {
		t.Fatal("empty execution_id must fail")
	}
}
