package application

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

func TestNewMemoryManager(t *testing.T) {
	m := NewMemoryManager(zap.NewNop(), nil)
	if m == nil {
		t.Fatal("expected non-nil manager")
	}
}

func TestMemoryManagerAdd_NilRepo(t *testing.T) {
	m := NewMemoryManager(zap.NewNop(), nil)
	err := m.Add(context.Background(), &MemoryEntry{
		ID: "e1", Type: ShortTermMemory, Role: "user", Content: "hello",
		TenantID: "t1", UserID: "u1", SessionID: "s1", AgentID: "a1",
	})
	if err != nil {
		t.Errorf("expected nil error with nil repo, got %v", err)
	}
}

func TestMemoryManagerSearch_NilRepo(t *testing.T) {
	m := NewMemoryManager(zap.NewNop(), nil)
	results, err := m.Search(context.Background(), &MemorySearchRequest{Query: "test", Limit: 10})
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}
