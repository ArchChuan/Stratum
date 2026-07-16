// Package dto defines API data models and types.

package dto

import "time"

// CreateMemoryRequest is the canonical request body for POST /memory.
// Tenant and user identity are deliberately absent and come from auth context.
type CreateMemoryRequest struct {
	Content    string  `json:"content" binding:"required"`
	Importance float64 `json:"importance"`
}

// MemoryFactResponse is the canonical fact shape returned by /memory endpoints.
type MemoryFactResponse struct {
	ID         string    `json:"id"`
	Scope      string    `json:"scope"`
	Content    string    `json:"content"`
	Importance float64   `json:"importance"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type MemorySessionsResponse struct {
	Sessions []string `json:"sessions"`
}

type MemorySummaryResponse struct {
	Summary string `json:"summary"`
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
