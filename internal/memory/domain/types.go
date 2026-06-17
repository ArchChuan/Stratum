package domain

import "time"

// MemoryType defines the type of memory.
type MemoryType string

const (
	ShortTermMemory  MemoryType = "short_term"
	LongTermMemory   MemoryType = "long_term"
	EntityTypeMemory MemoryType = "entity"
	SummaryMemory    MemoryType = "summary"
)

// MemoryEntry represents a single memory record.
type MemoryEntry struct {
	ID         string                 `json:"id"`
	Type       MemoryType             `json:"type"`
	Role       string                 `json:"role"`
	Content    string                 `json:"content"`
	Timestamp  time.Time              `json:"timestamp"`
	TenantID   string                 `json:"tenant_id"`
	UserID     string                 `json:"user_id"`
	SessionID  string                 `json:"session_id"`
	AgentID    string                 `json:"agent_id"`
	Metadata   map[string]interface{} `json:"metadata"`
	Vector     []float32              `json:"vector,omitempty"`
	Tags       []string               `json:"tags,omitempty"`
	Importance float64                `json:"importance"`
	ExpiresAt  time.Time              `json:"expires_at,omitempty"`
}

// MemoryConfig holds runtime config for the memory subsystem.
type MemoryConfig struct {
	EnableVectorSearch bool    `json:"enable_vector_search"`
	VectorCollection   string  `json:"vector_collection"`
	MaxVectorResults   int     `json:"max_vector_results"`
	MinRelevanceScore  float64 `json:"min_relevance_score"`

	EnableEntityExtraction bool    `json:"enable_entity_extraction"`
	EntityThreshold        float64 `json:"entity_threshold"`

	EnablePersistence   bool          `json:"enable_persistence"`
	PersistenceInterval time.Duration `json:"persistence_interval"`
	MaxMemoryAge        time.Duration `json:"max_memory_age"`
}

// TenantContext carries tenant defaults.
type TenantContext struct {
	TenantID string
	Defaults map[string]interface{}
}

// UserContext carries per-user data.
type UserContext struct {
	TenantID string
	UserID   string
	Profile  map[string]interface{}
}

// SessionContext represents a conversation session.
type SessionContext struct {
	TenantID  string
	UserID    string
	SessionID string
	AgentID   string
	StartTime time.Time
	Metadata  map[string]interface{}
}

// TimeRange constrains a memory query window.
type TimeRange struct {
	From time.Time
	To   time.Time
}

// MemorySearchRequest is the search query value object.
type MemorySearchRequest struct {
	Query     string
	Context   *SessionContext
	Types     []MemoryType
	Limit     int
	MinScore  float64
	TimeRange *TimeRange
	Filters   map[string]interface{}
}

// MemorySearchResult wraps a hit with score / distance.
type MemorySearchResult struct {
	Entry    *MemoryEntry
	Score    float64
	Distance float64
}

// MemoryStats describes per-tenant memory volume.
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

// Entity represents an extracted entity.
type Entity struct {
	ID         string
	Name       string
	Type       string
	Confidence float64
	TenantID   string
	UserID     string
	FirstSeen  time.Time
	LastSeen   time.Time
	Attributes map[string]interface{}
	Relations  []EntityRelation
}

// EntityRelation links two entities.
type EntityRelation struct {
	FromEntityID string
	ToEntityID   string
	RelationType string
	Confidence   float64
	LastSeen     time.Time
	Metadata     map[string]interface{}
}

// MemoryEvent describes a memory mutation broadcast on the bus.
type MemoryEvent struct {
	EventType string       `json:"event_type"`
	Entry     *MemoryEntry `json:"entry,omitempty"`
	Query     string       `json:"query,omitempty"`
	TenantID  string       `json:"tenant_id"`
	UserID    string       `json:"user_id"`
	SessionID string       `json:"session_id"`
}

// MemoryFilter filters in-memory entries.
type MemoryFilter struct {
	TenantID      string
	UserID        string
	SessionID     string
	AgentID       string
	StartTime     *time.Time
	EndTime       *time.Time
	MemoryType    MemoryType
	Tags          []string
	MinImportance float64
}

// ApplyFilter returns true when entry matches the filter.
func (f *MemoryFilter) ApplyFilter(entry *MemoryEntry) bool {
	if f.TenantID != "" && entry.TenantID != f.TenantID {
		return false
	}
	if f.UserID != "" && entry.UserID != f.UserID {
		return false
	}
	if f.SessionID != "" && entry.SessionID != f.SessionID {
		return false
	}
	if f.AgentID != "" && entry.AgentID != f.AgentID {
		return false
	}
	if f.StartTime != nil && entry.Timestamp.Before(*f.StartTime) {
		return false
	}
	if f.EndTime != nil && entry.Timestamp.After(*f.EndTime) {
		return false
	}
	if f.MemoryType != "" && entry.Type != f.MemoryType {
		return false
	}
	if len(f.Tags) > 0 {
		hasTag := false
		for _, tag := range f.Tags {
			for _, entryTag := range entry.Tags {
				if tag == entryTag {
					hasTag = true
					break
				}
			}
			if hasTag {
				break
			}
		}
		if !hasTag {
			return false
		}
	}
	if entry.Importance < f.MinImportance {
		return false
	}
	return true
}
