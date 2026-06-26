package nats

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// Publisher publishes raw bytes to a subject.
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

// Publish sends data to subject via JetStream with 3-attempt exponential backoff.
func (p *JetStreamPublisher) Publish(ctx context.Context, subject string, data []byte) error {
	const (
		attempts = 3
		base     = 100 * time.Millisecond
		max      = 10 * time.Second
	)
	delay := base
	var lastErr error
	for i := 0; i < attempts; i++ {
		if _, err := p.js.Publish(subject, data); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if i < attempts-1 {
			select {
			case <-ctx.Done():
				return errors.Join(lastErr, ctx.Err())
			case <-time.After(delay):
			}
			delay = min(delay*2, max)
		}
	}
	return fmt.Errorf("nats: publish %q: %w", subject, lastErr)
}

var _ Publisher = (*JetStreamPublisher)(nil)
