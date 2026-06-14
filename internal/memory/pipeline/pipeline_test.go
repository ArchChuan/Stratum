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
