// Package memory provides agent memory management.
package memory

import (
	"time"
)

// MemoryType defines the type of memory
type MemoryType string

const (
	// ShortTermMemory is for current conversation context
	ShortTermMemory MemoryType = "short_term"
	// LongTermMemory is for persistent vector storage
	LongTermMemory MemoryType = "long_term"
	// EntityTypeMemory is for entities and relations
	EntityTypeMemory MemoryType = "entity"
	// SummaryMemory is for conversation summaries
	SummaryMemory MemoryType = "summary"
)

// MemoryEntry represents a single memory entry
type MemoryEntry struct {
	ID         string                 `json:"id"`
	Type       MemoryType             `json:"type"`
	Role       string                 `json:"role"` // "user", "assistant", "system"
	Content    string                 `json:"content"`
	Timestamp  time.Time              `json:"timestamp"`
	TenantID   string                 `json:"tenant_id"`
	UserID     string                 `json:"user_id"`
	SessionID  string                 `json:"session_id"`
	AgentID    string                 `json:"agent_id"`
	Metadata   map[string]interface{} `json:"metadata"`
	Vector     []float32              `json:"vector,omitempty"` // For semantic search
	Tags       []string               `json:"tags,omitempty"`
	Importance float64                `json:"importance"` // 0.0 to 1.0
	ExpiresAt  time.Time              `json:"expires_at,omitempty"`
}

// MemoryConfig holds configuration for memory systems
type MemoryConfig struct {
	// Short-term memory
	MaxShortTermMessages int  `json:"max_short_term_messages"`
	ShortTermWindowSize  int  `json:"short_term_window_size"`
	EnableSummary        bool `json:"enable_summary"`

	// Long-term memory
	EnableVectorSearch bool    `json:"enable_vector_search"`
	VectorCollection   string  `json:"vector_collection"`
	MaxVectorResults   int     `json:"max_vector_results"`
	MinRelevanceScore  float64 `json:"min_relevance_score"`

	// Entity memory
	EnableEntityExtraction bool    `json:"enable_entity_extraction"`
	EntityThreshold        float64 `json:"entity_threshold"`

	// Persistence
	EnablePersistence   bool          `json:"enable_persistence"`
	PersistenceInterval time.Duration `json:"persistence_interval"`
	MaxMemoryAge        time.Duration `json:"max_memory_age"`
}

// TenantContext provides tenant isolation
type TenantContext struct {
	TenantID string
	Defaults map[string]interface{}
}

// UserContext provides user-specific context
type UserContext struct {
	TenantID string
	UserID   string
	Profile  map[string]interface{}
}

// SessionContext represents a conversation session
type SessionContext struct {
	TenantID  string
	UserID    string
	SessionID string
	AgentID   string
	StartTime time.Time
	Metadata  map[string]interface{}
}

// MemorySearchRequest represents a search query
type MemorySearchRequest struct {
	Query     string
	Context   *SessionContext
	Types     []MemoryType
	Limit     int
	MinScore  float64
	TimeRange *TimeRange
	Filters   map[string]interface{}
}

// TimeRange defines a time filter
type TimeRange struct {
	From time.Time
	To   time.Time
}

// MemorySearchResult represents a search result
type MemorySearchResult struct {
	Entry    *MemoryEntry
	Score    float64
	Distance float64 // For vector similarity
}

// MemoryStats holds memory statistics
type MemoryStats struct {
	TotalEntries     int64
	ShortTermCount   int64
	LongTermCount    int64
	EntityCount      int64
	SessionsCount    int64
	ActiveUsers      int64
	VectorCount      int64
	LastAccessTime   time.Time
	StorageSizeBytes int64
}

// Entity represents an extracted entity
type Entity struct {
	ID         string
	Name       string
	Type       string // "person", "organization", "location", "concept", etc.
	Confidence float64
	TenantID   string
	UserID     string
	FirstSeen  time.Time
	LastSeen   time.Time
	Attributes map[string]interface{}
	Relations  []EntityRelation
}

// EntityRelation represents a relationship between entities
type EntityRelation struct {
	FromEntityID string
	ToEntityID   string
	RelationType string // "works_for", "located_in", "part_of", etc.
	Confidence   float64
	LastSeen     time.Time
	Metadata     map[string]interface{}
}

// MemoryEvent represents a memory-related event for Hermes
type MemoryEvent struct {
	EventType string       `json:"event_type"` // "created", "updated", "deleted", "searched"
	Entry     *MemoryEntry `json:"entry,omitempty"`
	Query     string       `json:"query,omitempty"`
	TenantID  string       `json:"tenant_id"`
	UserID    string       `json:"user_id"`
	SessionID string       `json:"session_id"`
}

// DefaultMemoryConfig returns the default memory configuration
func DefaultMemoryConfig() *MemoryConfig {
	return &MemoryConfig{
		MaxShortTermMessages: 100,
		ShortTermWindowSize:  10,
		EnableSummary:        true,

		EnableVectorSearch: true,
		VectorCollection:   "memory_vectors",
		MaxVectorResults:   5,
		MinRelevanceScore:  0.7,

		EnableEntityExtraction: true,
		EntityThreshold:        0.8,

		EnablePersistence:   true,
		PersistenceInterval: 5 * time.Minute,
		MaxMemoryAge:        30 * 24 * time.Hour, // 30 days
	}
}
