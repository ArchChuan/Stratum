package hermes

import (
	"testing"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/observability"
)

func TestNewClient(t *testing.T) {
	client, err := NewClient(nil, zap.NewNop(), observability.NoopMetrics{})
	if err == nil {
		t.Error("expected error for nil nats connection")
	}
	if client != nil {
		t.Error("expected nil client")
	}
}
