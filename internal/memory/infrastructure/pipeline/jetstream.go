package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"
)

type JetStreamManager struct {
	js     jetstream.JetStream
	logger *zap.Logger
}

func NewJetStreamManager(nc *nats.Conn, logger *zap.Logger) (*JetStreamManager, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("jetstream.New: %w", err)
	}
	return &JetStreamManager{js: js, logger: logger}, nil
}

func (m *JetStreamManager) EnsureStreams(ctx context.Context) error {
	streams := []jetstream.StreamConfig{
		{
			Name:      constants.MemoryRawStream,
			Subjects:  []string{constants.MemoryRawSubject + ".>"},
			Retention: jetstream.WorkQueuePolicy,
			MaxAge:    constants.MemoryStreamMaxAge,
			Storage:   jetstream.FileStorage,
		},
		{
			Name:      constants.MemoryEnrichedStream,
			Subjects:  []string{constants.MemoryEnrichedSubject + ".>"},
			Retention: jetstream.WorkQueuePolicy,
			MaxAge:    constants.MemoryStreamMaxAge,
			Storage:   jetstream.FileStorage,
		},
		{
			Name:      constants.MemoryDLQStream,
			Subjects:  []string{constants.MemoryDLQSubject + ".>"},
			Retention: jetstream.LimitsPolicy,
			MaxAge:    constants.MemoryDLQMaxAge,
			Storage:   jetstream.FileStorage,
		},
	}

	for _, cfg := range streams {
		_, err := m.js.CreateOrUpdateStream(ctx, cfg)
		if err != nil {
			return fmt.Errorf("ensure stream %s: %w", cfg.Name, err)
		}
		m.logger.Info("jetstream stream ensured", zap.String("stream", cfg.Name))
	}
	return nil
}

func (m *JetStreamManager) JS() jetstream.JetStream {
	return m.js
}

func (m *JetStreamManager) CreateConsumer(ctx context.Context, stream, name, filterSubject string, ackWait time.Duration, maxDeliver int) (jetstream.Consumer, error) {
	consumer, err := m.js.CreateOrUpdateConsumer(ctx, stream, jetstream.ConsumerConfig{
		Durable:       name,
		AckWait:       ackWait,
		MaxDeliver:    maxDeliver,
		FilterSubject: filterSubject,
		AckPolicy:     jetstream.AckExplicitPolicy,
	})
	if err != nil {
		return nil, fmt.Errorf("create consumer %s on %s: %w", name, stream, err)
	}
	return consumer, nil
}
