package memory

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

func TestMemoryManagerWithWindowSize(t *testing.T) {
	logger := zap.NewNop()
	cfg := &MemoryConfig{
		ShortTermWindowSize:    10,
		MaxShortTermMessages:   100,
		MaxVectorResults:       10,
		EnableSummary:          false,
		EnableVectorSearch:     false,
		EnableEntityExtraction: false,
		EnablePersistence:      false,
	}

	manager := NewMemoryManager(cfg, logger, nil, nil, nil, nil)

	if manager == nil { //nolint:staticcheck
		t.Error("expected non-nil manager")
	}

	if manager.shortTerm == nil { //nolint:staticcheck
		t.Error("expected short-term memory to be initialized")
	}
}

func TestMemoryManagerWithSummary(t *testing.T) {
	logger := zap.NewNop()
	cfg := &MemoryConfig{
		ShortTermWindowSize:    0,
		MaxShortTermMessages:   100,
		MaxVectorResults:       10,
		EnableSummary:          true,
		EnableVectorSearch:     false,
		EnableEntityExtraction: false,
		EnablePersistence:      false,
	}

	manager := NewMemoryManager(cfg, logger, nil, nil, nil, nil)

	if manager == nil { //nolint:staticcheck
		t.Error("expected non-nil manager")
	}

	if manager.shortTerm == nil { //nolint:staticcheck
		t.Error("expected short-term memory to be initialized")
	}
}

func TestMemoryManagerWithBuffer(t *testing.T) {
	logger := zap.NewNop()
	cfg := &MemoryConfig{
		ShortTermWindowSize:    0,
		MaxShortTermMessages:   100,
		MaxVectorResults:       10,
		EnableSummary:          false,
		EnableVectorSearch:     false,
		EnableEntityExtraction: false,
		EnablePersistence:      false,
	}

	manager := NewMemoryManager(cfg, logger, nil, nil, nil, nil)

	if manager == nil { //nolint:staticcheck
		t.Error("expected non-nil manager")
	}

	if manager.shortTerm == nil { //nolint:staticcheck
		t.Error("expected short-term memory to be initialized")
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

func TestConversationBufferMemory(t *testing.T) {
	logger := zap.NewNop()
	cfg := DefaultMemoryConfig()

	mem := NewConversationBufferMemory(cfg, logger)

	if mem == nil {
		t.Error("expected non-nil memory")
	}

	ctx := context.Background()
	entry := &MemoryEntry{
		ID:      "entry-1",
		Type:    ShortTermMemory,
		Role:    "user",
		Content: "test content",
	}

	err := mem.Add(ctx, entry)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestConversationSummaryMemory(t *testing.T) {
	logger := zap.NewNop()
	cfg := DefaultMemoryConfig()

	mem := NewConversationSummaryMemory(cfg, logger)

	if mem == nil {
		t.Error("expected non-nil memory")
	}

	ctx := context.Background()
	entry := &MemoryEntry{
		ID:      "entry-1",
		Type:    ShortTermMemory,
		Role:    "user",
		Content: "test content",
	}

	err := mem.Add(ctx, entry)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestConversationWindowMemory(t *testing.T) {
	logger := zap.NewNop()
	cfg := &MemoryConfig{
		ShortTermWindowSize:    5,
		MaxShortTermMessages:   100,
		MaxVectorResults:       10,
		EnableSummary:          false,
		EnableVectorSearch:     false,
		EnableEntityExtraction: false,
		EnablePersistence:      false,
	}

	mem := NewConversationWindowMemory(cfg, logger)

	if mem == nil {
		t.Error("expected non-nil memory")
	}

	ctx := context.Background()
	entry := &MemoryEntry{
		ID:      "entry-1",
		Type:    ShortTermMemory,
		Role:    "user",
		Content: "test content",
	}

	err := mem.Add(ctx, entry)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}
