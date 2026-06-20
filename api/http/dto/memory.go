// Package dto defines API data models and types.

package dto

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

// AddMemoryRequest is the body for POST /memory.
type AddMemoryRequest struct {
	Type       string                 `json:"type"`
	Role       string                 `json:"role"`
	Content    string                 `json:"content" binding:"required"`
	SessionID  string                 `json:"session_id"`
	AgentID    string                 `json:"agent_id"`
	Importance float64                `json:"importance"`
	Tags       []string               `json:"tags"`
	Metadata   map[string]interface{} `json:"metadata"`
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
