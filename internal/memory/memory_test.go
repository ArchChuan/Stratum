package memory

import (
	"testing"

	"go.uber.org/zap"
)

func TestDefaultMemoryConfig(t *testing.T) {
	cfg := DefaultMemoryConfig()

	if cfg == nil {
		t.Error("expected config to be non-nil")
	}

	if cfg.MaxShortTermMessages <= 0 {
		t.Errorf("expected positive MaxShortTermMessages, got %d", cfg.MaxShortTermMessages)
	}
}

func TestNewMemoryManager(t *testing.T) {
	logger := zap.NewNop()
	cfg := DefaultMemoryConfig()

	manager := NewMemoryManager(cfg, logger, nil, nil, nil)

	if manager == nil {
		t.Error("expected manager to be non-nil")
	}
}
