package embedding

import (
	"testing"

	"go.uber.org/zap"
)

func TestNewEmbeddingService(t *testing.T) {
	logger := zap.NewNop()
	service := NewEmbeddingService("test-key", logger)

	if service == nil {
		t.Error("expected service to be non-nil")
	}

	if service.client == nil {
		t.Error("expected client to be non-nil")
	}

	if service.logger == nil {
		t.Error("expected logger to be non-nil")
	}
}

func TestNewEmbeddingServiceEmptyKey(t *testing.T) {
	logger := zap.NewNop()
	service := NewEmbeddingService("", logger)

	if service == nil {
		t.Error("expected service to be non-nil even with empty key")
	}
}
