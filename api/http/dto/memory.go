// Package dto defines API data models and types.

package dto

// CreateSessionRequest is the body for POST /memory/sessions.
type CreateSessionRequest struct {
	TenantID string                 `json:"tenant_id"`
	UserID   string                 `json:"user_id"`
	AgentID  string                 `json:"agent_id"`
	Metadata map[string]interface{} `json:"metadata"`
}

// CreateSessionResponse is returned by POST /memory/sessions.
type CreateSessionResponse struct {
	SessionID string `json:"session_id"`
	TenantID  string `json:"tenant_id"`
	UserID    string `json:"user_id"`
	AgentID   string `json:"agent_id"`
	StartTime string `json:"start_time"`
}

// AddMemoryRequest is the body for POST /memory.
type AddMemoryRequest struct {
	Role       string                 `json:"role" binding:"required,oneof=user assistant system"`
	Content    string                 `json:"content" binding:"required"`
	SessionID  string                 `json:"session_id" binding:"required"`
	TenantID   string                 `json:"tenant_id"`
	UserID     string                 `json:"user_id"`
	AgentID    string                 `json:"agent_id"`
	Metadata   map[string]interface{} `json:"metadata"`
	Tags       []string               `json:"tags"`
	Importance float64                `json:"importance"`
	ExpiresAt  string                 `json:"expires_at"`
}

// MemoryEntryResponse is the canonical entry shape returned by /memory endpoints.
type MemoryEntryResponse struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	Role       string                 `json:"role"`
	Content    string                 `json:"content"`
	Timestamp  string                 `json:"timestamp"`
	TenantID   string                 `json:"tenant_id"`
	UserID     string                 `json:"user_id"`
	SessionID  string                 `json:"session_id"`
	AgentID    string                 `json:"agent_id"`
	Metadata   map[string]interface{} `json:"metadata"`
	Tags       []string               `json:"tags"`
	Importance float64                `json:"importance"`
	ExpiresAt  string                 `json:"expires_at,omitempty"`
}

// SearchMemoryRequest is the body for POST /memory/search.
type SearchMemoryRequest struct {
	Query     string                 `json:"query"`
	SessionID string                 `json:"session_id"`
	TenantID  string                 `json:"tenant_id"`
	UserID    string                 `json:"user_id"`
	Types     []string               `json:"types"`
	Limit     int                    `json:"limit"`
	MinScore  float64                `json:"min_score"`
	Filters   map[string]interface{} `json:"filters"`
}

// SearchMemoryResponse is the result envelope for /memory/search.
type SearchMemoryResponse struct {
	Results []*MemorySearchResultItem `json:"results"`
	Count   int                       `json:"count"`
}

// MemorySearchResultItem is a single hit in SearchMemoryResponse.
type MemorySearchResultItem struct {
	Entry    *MemoryEntryResponse `json:"entry"`
	Score    float64              `json:"score"`
	Distance float64              `json:"distance,omitempty"`
}

// MemoryStatsResponse is the response for GET /memory/stats.
type MemoryStatsResponse struct {
	TotalEntries     int64  `json:"total_entries"`
	ShortTermCount   int64  `json:"short_term_count"`
	LongTermCount    int64  `json:"long_term_count"`
	EntityCount      int64  `json:"entity_count"`
	SessionsCount    int64  `json:"sessions_count"`
	ActiveUsers      int64  `json:"active_users"`
	VectorCount      int64  `json:"vector_count"`
	LastAccessTime   string `json:"last_access_time"`
	StorageSizeBytes int64  `json:"storage_size_bytes"`
}

// EntityResponse is a single entity returned by /memory/entities.
type EntityResponse struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	Type       string                 `json:"type"`
	Confidence float64                `json:"confidence"`
	FirstSeen  string                 `json:"first_seen"`
	LastSeen   string                 `json:"last_seen"`
	Attributes map[string]interface{} `json:"attributes"`
	Relations  []EntityRelationItem   `json:"relations"`
}

// EntityRelationItem is one edge attached to an EntityResponse.
type EntityRelationItem struct {
	FromEntityID string                 `json:"from_entity_id"`
	ToEntityID   string                 `json:"to_entity_id"`
	RelationType string                 `json:"relation_type"`
	Confidence   float64                `json:"confidence"`
	LastSeen     string                 `json:"last_seen"`
	Metadata     map[string]interface{} `json:"metadata"`
}

// ExtractEntitiesRequest is the body for POST /memory/extract-entities.
type ExtractEntitiesRequest struct {
	Text      string                 `json:"text" binding:"required"`
	SessionID string                 `json:"session_id" binding:"required"`
	UserID    string                 `json:"user_id"`
	Metadata  map[string]interface{} `json:"metadata"`
}
