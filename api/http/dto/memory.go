// Package dto defines API data models and types.

package dto

import "time"

// CreateMemoryRequest is the canonical request body for POST /memory.
// Tenant and user identity are deliberately absent and come from auth context.
type CreateMemoryRequest struct {
	AgentID        string   `json:"agent_id"`
	ConversationID string   `json:"conversation_id"`
	Content        string   `json:"content" binding:"required"`
	Importance     float64  `json:"importance"`
	EntityNames    []string `json:"entity_names"`
}

// MemoryFactResponse is the canonical fact shape returned by /memory endpoints.
type MemoryFactResponse struct {
	ID             string    `json:"id"`
	AgentID        string    `json:"agent_id,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
	Scope          string    `json:"scope"`
	Content        string    `json:"content"`
	Importance     float64   `json:"importance"`
	EntityNames    []string  `json:"entity_names"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// MemoryStatsResponse is the response for GET /memory/stats.
type MemoryStatsResponse struct {
	TotalEntries     int64     `json:"total_entries"`
	ShortTermCount   int64     `json:"short_term_count"`
	LongTermCount    int64     `json:"long_term_count"`
	EntityCount      int64     `json:"entity_count"`
	SessionsCount    int64     `json:"sessions_count"`
	ActiveUsers      int64     `json:"active_users"`
	VectorCount      int64     `json:"vector_count"`
	LastAccessTime   time.Time `json:"last_access_time"`
	StorageSizeBytes int64     `json:"storage_size_bytes"`
}
