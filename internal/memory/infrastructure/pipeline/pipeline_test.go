package pipeline

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natsserver "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

func startJetStreamServer(t *testing.T) (*server.Server, *nats.Conn) {
	t.Helper()
	opts := natsserver.DefaultTestOptions
	opts.JetStream = true
	opts.Port = -1
	opts.StoreDir = t.TempDir()
	s := natsserver.RunServer(&opts)
	t.Cleanup(s.Shutdown)

	nc, err := nats.Connect(s.ClientURL())
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	return s, nc
}

func TestJetStreamManager_EnsureStreams(t *testing.T) {
	_, nc := startJetStreamServer(t)
	logger := zaptest.NewLogger(t)

	jsm, err := NewJetStreamManager(nc, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = jsm.EnsureStreams(ctx)
	require.NoError(t, err)

	js := jsm.JS()
	stream, err := js.Stream(ctx, constants.MemoryRawStream)
	require.NoError(t, err)
	assert.Equal(t, constants.MemoryRawStream, stream.CachedInfo().Config.Name)
}

func TestEventPublishConsume(t *testing.T) {
	_, nc := startJetStreamServer(t)
	logger := zaptest.NewLogger(t)

	jsm, err := NewJetStreamManager(nc, logger)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, jsm.EnsureStreams(ctx))

	ev := &MemoryRawEvent{
		MessageID:      "test-msg-1",
		ConversationID: "conv-1",
		TenantID:       "tenant-test",
		UserID:         "user-1",
		AgentID:        "agent-1",
		Role:           "user",
		Content:        "Hello from test",
		CreatedAt:      time.Now().Truncate(time.Millisecond),
	}
	data, err := json.Marshal(ev)
	require.NoError(t, err)

	js := jsm.JS()
	_, err = js.Publish(ctx, constants.MemoryRawSubject+".tenant-test", data)
	require.NoError(t, err)

	consumer, err := jsm.CreateConsumer(ctx,
		constants.MemoryRawStream,
		"test-consumer",
		constants.MemoryRawSubject+".>",
		10*time.Second, 3)
	require.NoError(t, err)

	msgs, err := consumer.Fetch(1, jetstream.FetchMaxWait(2*time.Second))
	require.NoError(t, err)

	var received *MemoryRawEvent
	for msg := range msgs.Messages() {
		received, err = UnmarshalRawEvent(msg.Data())
		require.NoError(t, err)
		_ = msg.Ack()
	}
	require.NotNil(t, received)
	assert.Equal(t, ev.MessageID, received.MessageID)
	assert.Equal(t, ev.Content, received.Content)
}

func TestInvalidRawEventMovesToDeadLetterStream(t *testing.T) {
	_, nc := startJetStreamServer(t)
	logger := zaptest.NewLogger(t)
	jsm, err := NewJetStreamManager(nc, logger)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, jsm.EnsureStreams(ctx))
	js := jsm.JS()

	_, err = js.Publish(ctx, constants.MemoryRawSubject+".tenant-test", []byte(`{"broken":`))
	require.NoError(t, err)
	rawConsumer, err := jsm.CreateConsumer(
		ctx,
		constants.MemoryRawStream,
		"test-dlq-embed",
		constants.MemoryRawSubject+".>",
		time.Second,
		3,
	)
	require.NoError(t, err)

	batch, err := rawConsumer.Fetch(1, jetstream.FetchMaxWait(time.Second))
	require.NoError(t, err)
	for msg := range batch.Messages() {
		worker := &EmbedderWorker{js: js, logger: logger, ackWait: time.Second, maxDeliver: 3}
		worker.processMessage(ctx, msg)
	}

	dlqConsumer, err := jsm.CreateConsumer(
		ctx,
		constants.MemoryDLQStream,
		"test-dlq-reader",
		constants.MemoryDLQSubject+".tenant-test",
		time.Second,
		1,
	)
	require.NoError(t, err)
	dlqBatch, err := dlqConsumer.Fetch(1, jetstream.FetchMaxWait(time.Second))
	require.NoError(t, err)

	var got DeadLetterEvent
	for msg := range dlqBatch.Messages() {
		require.NoError(t, json.Unmarshal(msg.Data(), &got))
		require.NoError(t, msg.Ack())
	}
	assert.Equal(t, "tenant-test", got.TenantID)
	assert.Equal(t, "embed", got.Stage)
	assert.Equal(t, "invalid_event", got.ErrorCode)
	assert.Equal(t, constants.MemoryRawStream, got.OriginalStream)
	assert.NotZero(t, got.StreamSequence)
	assert.NotContains(t, string(mustJSON(t, got)), "broken")
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}
