package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type fakeDLQPublisher struct {
	subject string
	payload []byte
	err     error
	count   int
	delay   time.Duration
}

func (p *fakeDLQPublisher) Publish(_ context.Context, subject string, payload []byte, _ ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	p.subject = subject
	p.payload = append([]byte(nil), payload...)
	p.count++
	time.Sleep(p.delay)
	return &jetstream.PubAck{}, p.err
}

type fakeJetStreamMsg struct {
	data                 []byte
	subject              string
	metadata             *jetstream.MsgMetadata
	metadataErr          error
	ackCount             int
	nakCount             int
	termCount            int
	progresses           atomic.Int32
	disposed             atomic.Bool
	progressAfterDispose atomic.Int32
	termDelay            time.Duration
}

func (m *fakeJetStreamMsg) Metadata() (*jetstream.MsgMetadata, error) {
	return m.metadata, m.metadataErr
}
func (m *fakeJetStreamMsg) Data() []byte                     { return m.data }
func (m *fakeJetStreamMsg) Headers() nats.Header             { return nil }
func (m *fakeJetStreamMsg) Subject() string                  { return m.subject }
func (m *fakeJetStreamMsg) Reply() string                    { return "" }
func (m *fakeJetStreamMsg) Ack() error                       { m.ackCount++; return nil }
func (m *fakeJetStreamMsg) DoubleAck(context.Context) error  { m.ackCount++; return nil }
func (m *fakeJetStreamMsg) Nak() error                       { m.nakCount++; return nil }
func (m *fakeJetStreamMsg) NakWithDelay(time.Duration) error { m.nakCount++; return nil }
func (m *fakeJetStreamMsg) InProgress() error {
	m.progresses.Add(1)
	if m.disposed.Load() {
		m.progressAfterDispose.Add(1)
	}
	return nil
}
func (m *fakeJetStreamMsg) Term() error { return m.TermWithReason("") }
func (m *fakeJetStreamMsg) TermWithReason(string) error {
	m.termCount++
	m.disposed.Store(true)
	time.Sleep(m.termDelay)
	return nil
}

func TestDeadLetterPublishesMetadataWithoutContentThenTerminates(t *testing.T) {
	msg := &fakeJetStreamMsg{
		data:    []byte(`{"content":"secret user text"}`),
		subject: "memory.raw.tenant-a",
		metadata: &jetstream.MsgMetadata{
			Stream:       "MEMORY_RAW",
			Consumer:     "embed-worker",
			NumDelivered: 2,
			Sequence:     jetstream.SequencePair{Stream: 42},
		},
	}
	pub := &fakeDLQPublisher{}

	err := deadLetter(context.Background(), pub, msg, deadLetterDetails{
		Stage:     "embed",
		TenantID:  "tenant-a",
		MessageID: "message-a",
		ErrorCode: "invalid_event",
	})
	require.NoError(t, err)
	assert.Equal(t, "memory.dlq.tenant-a", pub.subject)
	assert.Equal(t, 1, msg.termCount)
	assert.Zero(t, msg.nakCount)
	assert.NotContains(t, string(pub.payload), "secret user text")

	var event DeadLetterEvent
	require.NoError(t, json.Unmarshal(pub.payload, &event))
	assert.Equal(t, uint64(42), event.StreamSequence)
	assert.Equal(t, uint64(2), event.Deliveries)
	assert.Equal(t, "invalid_event", event.ErrorCode)
}

func TestDeadLetterPublishFailureNaksOriginal(t *testing.T) {
	msg := &fakeJetStreamMsg{
		subject:  "memory.raw.tenant-a",
		metadata: &jetstream.MsgMetadata{Stream: "MEMORY_RAW", Sequence: jetstream.SequencePair{Stream: 2}},
	}
	pub := &fakeDLQPublisher{err: errors.New("nats unavailable")}

	err := deadLetter(context.Background(), pub, msg, deadLetterDetails{
		Stage:     "embed",
		TenantID:  "tenant-a",
		ErrorCode: "invalid_event",
	})
	require.Error(t, err)
	assert.Zero(t, msg.termCount)
	assert.Equal(t, 1, msg.nakCount)
}

func TestDeadLetterMetadataFailureNaksWithoutPublishing(t *testing.T) {
	msg := &fakeJetStreamMsg{
		subject:     "memory.raw.tenant-a",
		metadataErr: errors.New("not a JetStream message"),
	}
	pub := &fakeDLQPublisher{}

	err := deadLetter(context.Background(), pub, msg, deadLetterDetails{
		Stage: "embed", TenantID: "tenant-a", ErrorCode: "invalid_event",
	})
	require.Error(t, err)
	assert.Zero(t, pub.count)
	assert.Zero(t, msg.termCount)
	assert.Equal(t, 1, msg.nakCount)
}

func TestDeadLetterDedupIDIgnoresFailureClassification(t *testing.T) {
	first := DeadLetterEvent{OriginalStream: "MEMORY_RAW", StreamSequence: 42, ErrorCode: "embedding_failed"}
	second := first
	second.ErrorCode = "vector_upsert_failed"

	assert.Equal(t, deadLetterDedupID(first), deadLetterDedupID(second))
}

func TestRetryOrDeadLetterUsesDLQOnLastDelivery(t *testing.T) {
	msg := &fakeJetStreamMsg{
		subject: "memory.enriched.tenant-a",
		metadata: &jetstream.MsgMetadata{
			NumDelivered: 5,
			Sequence:     jetstream.SequencePair{Stream: 7},
		},
	}
	pub := &fakeDLQPublisher{}

	retryOrDeadLetter(context.Background(), pub, msg, 5, deadLetterDetails{
		Stage:     "enrich",
		TenantID:  "tenant-a",
		MessageID: "message-a",
		ErrorCode: "llm_failed",
	})

	assert.Equal(t, 1, msg.termCount)
	assert.Zero(t, msg.nakCount)
}

func TestRetryOrDeadLetterNilMetadataNaksWithoutPanic(t *testing.T) {
	msg := &fakeJetStreamMsg{subject: "memory.enriched.tenant-a"}
	pub := &fakeDLQPublisher{}

	assert.NotPanics(t, func() {
		err := retryOrDeadLetter(context.Background(), pub, msg, 5, deadLetterDetails{
			Stage: "enrich", TenantID: "tenant-a", ErrorCode: "llm_failed",
		})
		require.Error(t, err)
	})
	assert.Equal(t, 1, msg.nakCount)
	assert.Zero(t, pub.count)
}

func TestStartProgressHeartbeatRenewsUntilStopped(t *testing.T) {
	msg := &fakeJetStreamMsg{}
	stop := startProgressHeartbeat(msg, 10*time.Millisecond)
	t.Cleanup(stop)

	require.Eventually(t, func() bool { return msg.progresses.Load() >= 2 }, 200*time.Millisecond, 5*time.Millisecond)
	stop()
	count := msg.progresses.Load()
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, count, msg.progresses.Load())
}

func TestEmbedderInvalidEventGoesToDLQ(t *testing.T) {
	msg := &fakeJetStreamMsg{
		data:    []byte(`{"broken":`),
		subject: "memory.raw.tenant-a",
		metadata: &jetstream.MsgMetadata{
			Stream:       "MEMORY_RAW",
			Consumer:     "embed-worker",
			NumDelivered: 1,
			Sequence:     jetstream.SequencePair{Stream: 11},
		},
	}
	pub := &fakeDLQPublisher{}
	worker := &EmbedderWorker{js: pub, logger: zaptest.NewLogger(t), maxDeliver: 5}

	worker.processMessage(context.Background(), msg)

	assert.Equal(t, 1, pub.count)
	assert.Equal(t, 1, msg.termCount)
	assert.Zero(t, msg.ackCount)
}

func TestEmbedderStopsHeartbeatBeforeDeadLetterDisposition(t *testing.T) {
	msg := &fakeJetStreamMsg{
		data:      []byte(`{"broken":`),
		subject:   "memory.raw.tenant-a",
		termDelay: 30 * time.Millisecond,
		metadata: &jetstream.MsgMetadata{
			Stream: "MEMORY_RAW", Sequence: jetstream.SequencePair{Stream: 13}, NumDelivered: 1,
		},
	}
	worker := &EmbedderWorker{
		js: &fakeDLQPublisher{}, logger: zaptest.NewLogger(t), ackWait: 10 * time.Millisecond, maxDeliver: 5,
	}

	worker.processMessage(context.Background(), msg)

	assert.Zero(t, msg.progressAfterDispose.Load())
}

func TestEmbedderRenewsLeaseWhileDeadLetterPublishIsInFlight(t *testing.T) {
	msg := &fakeJetStreamMsg{
		data:    []byte(`{"broken":`),
		subject: "memory.raw.tenant-a",
		metadata: &jetstream.MsgMetadata{
			Stream: "MEMORY_RAW", Sequence: jetstream.SequencePair{Stream: 14}, NumDelivered: 1,
		},
	}
	worker := &EmbedderWorker{
		js: &fakeDLQPublisher{delay: 35 * time.Millisecond}, logger: zaptest.NewLogger(t),
		ackWait: 10 * time.Millisecond, maxDeliver: 5,
	}

	worker.processMessage(context.Background(), msg)

	assert.GreaterOrEqual(t, msg.progresses.Load(), int32(2))
	assert.Zero(t, msg.progressAfterDispose.Load())
}

func TestEnricherInvalidEventGoesToDLQ(t *testing.T) {
	msg := &fakeJetStreamMsg{
		data:    []byte(`{"broken":`),
		subject: "memory.enriched.tenant-a",
		metadata: &jetstream.MsgMetadata{
			Stream:       "MEMORY_ENRICHED",
			Consumer:     "enrich-worker",
			NumDelivered: 1,
			Sequence:     jetstream.SequencePair{Stream: 12},
		},
	}
	pub := &fakeDLQPublisher{}
	worker := &EnricherWorker{js: pub, logger: zaptest.NewLogger(t), maxDeliver: 5}

	worker.processMessage(context.Background(), msg)

	assert.Equal(t, 1, pub.count)
	assert.Equal(t, 1, msg.termCount)
	assert.Zero(t, msg.ackCount)
}
