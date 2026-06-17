package application

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

func TestNewMemoryManagerDefault(t *testing.T) {
	logger := zap.NewNop()
	cfg := DefaultMemoryConfig()

	manager := NewMemoryManager(cfg, logger, nil, nil, nil, nil)

	if manager == nil { //nolint:staticcheck
		t.Error("expected non-nil manager")
	}

	if manager.config != cfg { //nolint:staticcheck
		t.Error("config not set correctly")
	}

	if manager.logger != logger { //nolint:staticcheck
		t.Error("logger not set correctly")
	}
}

func TestNewMemoryManagerWithNilConfig(t *testing.T) {
	logger := zap.NewNop()

	manager := NewMemoryManager(nil, logger, nil, nil, nil, nil)

	if manager == nil { //nolint:staticcheck
		t.Error("expected non-nil manager")
	}

	if manager.config == nil { //nolint:staticcheck
		t.Error("expected default config to be set")
	}
}

func TestMemoryManagerWithVectorConfig(t *testing.T) {
	logger := zap.NewNop()
	cfg := &MemoryConfig{
		MaxVectorResults:       10,
		EnableVectorSearch:     true,
		EnableEntityExtraction: false,
		EnablePersistence:      false,
	}

	manager := NewMemoryManager(cfg, logger, nil, nil, nil, nil)

	if manager == nil { //nolint:staticcheck
		t.Error("expected non-nil manager")
		return
	}

	if manager.config == nil { //nolint:staticcheck
		t.Error("expected config to be set")
	}
}

func TestMemoryManagerAdd(t *testing.T) {
	logger := zap.NewNop()
	cfg := DefaultMemoryConfig()

	manager := NewMemoryManager(cfg, logger, nil, nil, nil, nil)

	ctx := context.Background()
	entry := &MemoryEntry{
		ID:        "entry-1",
		Type:      ShortTermMemory,
		Role:      "user",
		Content:   "test content",
		TenantID:  "tenant-1",
		UserID:    "user-1",
		SessionID: "session-1",
		AgentID:   "agent-1",
	}

	err := manager.Add(ctx, entry)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}
