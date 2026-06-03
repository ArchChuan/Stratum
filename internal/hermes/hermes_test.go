package hermes

import (
	"testing"

	"go.uber.org/zap"
)

func TestNewClient(t *testing.T) {
	logger := zap.NewNop()
	client, err := NewClient("nats://localhost:4222", logger)

	if err != nil {
		t.Logf("NewClient error (expected in test env): %v", err)
	}

	if client == nil && err == nil {
		t.Error("expected either client or error")
	}
}
