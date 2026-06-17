package nats

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go"
)

// Publisher publishes raw bytes to a subject. ctx is reserved for future
// timeout/tracing wiring; current NATS Go client is synchronous.
type Publisher interface {
	Publish(ctx context.Context, subject string, data []byte) error
}

// JetStreamPublisher adapts a JetStream context to the Publisher interface.
type JetStreamPublisher struct {
	js nats.JetStreamContext
}

// NewJetStreamPublisher constructs a JetStreamPublisher.
func NewJetStreamPublisher(js nats.JetStreamContext) *JetStreamPublisher {
	return &JetStreamPublisher{js: js}
}

// Publish sends data to subject via JetStream.
func (p *JetStreamPublisher) Publish(_ context.Context, subject string, data []byte) error {
	if _, err := p.js.Publish(subject, data); err != nil {
		return fmt.Errorf("nats: publish %q: %w", subject, err)
	}
	return nil
}

var _ Publisher = (*JetStreamPublisher)(nil)
