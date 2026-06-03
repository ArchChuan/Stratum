package memory

import (
	"testing"
	"time"

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

	if cfg.MaxVectorResults <= 0 {
		t.Errorf("expected positive MaxVectorResults, got %d", cfg.MaxVectorResults)
	}

	if !cfg.EnableSummary {
		t.Error("expected EnableSummary to be true")
	}

	if !cfg.EnableVectorSearch {
		t.Error("expected EnableVectorSearch to be true")
	}

	if !cfg.EnableEntityExtraction {
		t.Error("expected EnableEntityExtraction to be true")
	}

	if !cfg.EnablePersistence {
		t.Error("expected EnablePersistence to be true")
	}
}

func TestMemoryEntry(t *testing.T) {
	entry := &MemoryEntry{
		ID:         "entry-1",
		Type:       ShortTermMemory,
		Role:       "user",
		Content:    "test content",
		Timestamp:  time.Now(),
		TenantID:   "tenant-1",
		UserID:     "user-1",
		SessionID:  "session-1",
		AgentID:    "agent-1",
		Metadata:   map[string]interface{}{"key": "value"},
		Tags:       []string{"tag1", "tag2"},
		Importance: 0.8,
	}

	if entry.ID != "entry-1" {
		t.Errorf("expected ID entry-1, got %s", entry.ID)
	}

	if entry.Type != ShortTermMemory {
		t.Errorf("expected type ShortTermMemory, got %s", entry.Type)
	}

	if entry.Role != "user" {
		t.Errorf("expected role user, got %s", entry.Role)
	}

	if len(entry.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(entry.Tags))
	}

	if entry.Importance != 0.8 {
		t.Errorf("expected importance 0.8, got %f", entry.Importance)
	}
}

func TestMemoryEntryTypes(t *testing.T) {
	types := []MemoryType{
		ShortTermMemory,
		LongTermMemory,
		EntityTypeMemory,
		SummaryMemory,
	}

	if len(types) != 4 {
		t.Errorf("expected 4 memory types, got %d", len(types))
	}

	if ShortTermMemory != "short_term" {
		t.Errorf("expected ShortTermMemory to be 'short_term', got %s", ShortTermMemory)
	}

	if LongTermMemory != "long_term" {
		t.Errorf("expected LongTermMemory to be 'long_term', got %s", LongTermMemory)
	}
}

func TestTenantContext(t *testing.T) {
	ctx := &TenantContext{
		TenantID: "tenant-1",
		Defaults: map[string]interface{}{"key": "value"},
	}

	if ctx.TenantID != "tenant-1" {
		t.Errorf("expected tenant-1, got %s", ctx.TenantID)
	}

	if len(ctx.Defaults) != 1 {
		t.Errorf("expected 1 default, got %d", len(ctx.Defaults))
	}
}

func TestUserContext(t *testing.T) {
	ctx := &UserContext{
		TenantID: "tenant-1",
		UserID:   "user-1",
		Profile:  map[string]interface{}{"name": "John"},
	}

	if ctx.TenantID != "tenant-1" {
		t.Errorf("expected tenant-1, got %s", ctx.TenantID)
	}

	if ctx.UserID != "user-1" {
		t.Errorf("expected user-1, got %s", ctx.UserID)
	}

	if len(ctx.Profile) != 1 {
		t.Errorf("expected 1 profile field, got %d", len(ctx.Profile))
	}
}

func TestSessionContext(t *testing.T) {
	now := time.Now()
	ctx := &SessionContext{
		TenantID:  "tenant-1",
		UserID:    "user-1",
		SessionID: "session-1",
		AgentID:   "agent-1",
		StartTime: now,
		Metadata:  map[string]interface{}{"key": "value"},
	}

	if ctx.SessionID != "session-1" {
		t.Errorf("expected session-1, got %s", ctx.SessionID)
	}

	if ctx.StartTime != now {
		t.Error("expected start time to match")
	}
}

func TestMemorySearchRequest(t *testing.T) {
	req := &MemorySearchRequest{
		Query:    "test query",
		Types:    []MemoryType{ShortTermMemory},
		Limit:    10,
		MinScore: 0.7,
	}

	if req.Query != "test query" {
		t.Errorf("expected query 'test query', got %s", req.Query)
	}

	if len(req.Types) != 1 {
		t.Errorf("expected 1 type, got %d", len(req.Types))
	}

	if req.Limit != 10 {
		t.Errorf("expected limit 10, got %d", req.Limit)
	}
}

func TestTimeRange(t *testing.T) {
	from := time.Now().Add(-24 * time.Hour)
	to := time.Now()

	tr := &TimeRange{
		From: from,
		To:   to,
	}

	if tr.From != from {
		t.Error("expected from time to match")
	}

	if tr.To != to {
		t.Error("expected to time to match")
	}
}

func TestMemorySearchResult(t *testing.T) {
	entry := &MemoryEntry{
		ID:      "entry-1",
		Content: "test",
	}

	result := &MemorySearchResult{
		Entry:    entry,
		Score:    0.95,
		Distance: 0.05,
	}

	if result.Entry.ID != "entry-1" {
		t.Errorf("expected entry ID entry-1, got %s", result.Entry.ID)
	}

	if result.Score != 0.95 {
		t.Errorf("expected score 0.95, got %f", result.Score)
	}

	if result.Distance != 0.05 {
		t.Errorf("expected distance 0.05, got %f", result.Distance)
	}
}

func TestMemoryStats(t *testing.T) {
	stats := &MemoryStats{
		TotalEntries:     100,
		ShortTermCount:   50,
		LongTermCount:    30,
		EntityCount:      20,
		SessionsCount:    5,
		ActiveUsers:      3,
		VectorCount:      30,
		StorageSizeBytes: 1024000,
	}

	if stats.TotalEntries != 100 {
		t.Errorf("expected 100 total entries, got %d", stats.TotalEntries)
	}

	if stats.ShortTermCount != 50 {
		t.Errorf("expected 50 short-term entries, got %d", stats.ShortTermCount)
	}

	if stats.StorageSizeBytes != 1024000 {
		t.Errorf("expected 1024000 bytes, got %d", stats.StorageSizeBytes)
	}
}

func TestEntity(t *testing.T) {
	entity := &Entity{
		ID:         "entity-1",
		Name:       "John Doe",
		Type:       "person",
		Confidence: 0.95,
		TenantID:   "tenant-1",
		UserID:     "user-1",
		FirstSeen:  time.Now(),
		LastSeen:   time.Now(),
		Attributes: map[string]interface{}{"age": 30},
		Relations:  []EntityRelation{},
	}

	if entity.ID != "entity-1" {
		t.Errorf("expected ID entity-1, got %s", entity.ID)
	}

	if entity.Name != "John Doe" {
		t.Errorf("expected name John Doe, got %s", entity.Name)
	}

	if entity.Type != "person" {
		t.Errorf("expected type person, got %s", entity.Type)
	}

	if entity.Confidence != 0.95 {
		t.Errorf("expected confidence 0.95, got %f", entity.Confidence)
	}
}

func TestEntityRelation(t *testing.T) {
	rel := &EntityRelation{
		FromEntityID: "entity-1",
		ToEntityID:   "entity-2",
		RelationType: "works_for",
		Confidence:   0.85,
		LastSeen:     time.Now(),
		Metadata:     map[string]interface{}{"since": "2020"},
	}

	if rel.FromEntityID != "entity-1" {
		t.Errorf("expected from entity-1, got %s", rel.FromEntityID)
	}

	if rel.RelationType != "works_for" {
		t.Errorf("expected relation works_for, got %s", rel.RelationType)
	}

	if rel.Confidence != 0.85 {
		t.Errorf("expected confidence 0.85, got %f", rel.Confidence)
	}
}

func TestMemoryEvent(t *testing.T) {
	entry := &MemoryEntry{
		ID:      "entry-1",
		Content: "test",
	}

	event := &MemoryEvent{
		EventType: "created",
		Entry:     entry,
		Query:     "test query",
		TenantID:  "tenant-1",
		UserID:    "user-1",
		SessionID: "session-1",
	}

	if event.EventType != "created" {
		t.Errorf("expected event type created, got %s", event.EventType)
	}

	if event.Entry.ID != "entry-1" {
		t.Errorf("expected entry ID entry-1, got %s", event.Entry.ID)
	}

	if event.Query != "test query" {
		t.Errorf("expected query 'test query', got %s", event.Query)
	}
}

func TestNewMemoryManager(t *testing.T) {
	logger := zap.NewNop()
	cfg := DefaultMemoryConfig()

	manager := NewMemoryManager(cfg, logger, nil, nil, nil, nil)

	if manager == nil {
		t.Error("expected manager to be non-nil")
	}
}
