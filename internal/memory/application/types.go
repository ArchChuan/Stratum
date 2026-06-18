// Package application provides agent memory management orchestration.
package application

import (
	memdomain "github.com/byteBuilderX/stratum/internal/memory/domain"
)

// ErrNotFound aliases domain.ErrEntryNotFound to keep middleware/error_mapping
// route source-compatible during refactor.
var ErrNotFound = memdomain.ErrEntryNotFound

// tenantIDKey is the context key for tenant ID.
type tenantIDKey struct{}

// Re-exports of domain types/values so existing consumers (handler, agent
// application, wiring, tests) remain source-compatible while the canonical
// definitions live in domain/.
type (
	MemoryType          = memdomain.MemoryType
	MemoryEntry         = memdomain.MemoryEntry
	MemoryConfig        = memdomain.MemoryConfig
	TenantContext       = memdomain.TenantContext
	UserContext         = memdomain.UserContext
	SessionContext      = memdomain.SessionContext
	TimeRange           = memdomain.TimeRange
	MemorySearchRequest = memdomain.MemorySearchRequest
	MemorySearchResult  = memdomain.MemorySearchResult
	MemoryStats         = memdomain.MemoryStats
	Entity              = memdomain.Entity
	EntityRelation      = memdomain.EntityRelation
	MemoryEvent         = memdomain.MemoryEvent
)

const (
	ShortTermMemory  = memdomain.ShortTermMemory
	LongTermMemory   = memdomain.LongTermMemory
	EntityTypeMemory = memdomain.EntityTypeMemory
	SummaryMemory    = memdomain.SummaryMemory
)

// DefaultMemoryConfig returns the default memory configuration.
func DefaultMemoryConfig() *MemoryConfig {
	return &MemoryConfig{
		EnableVectorSearch: true,
		VectorCollection:   "memory_vectors",
		MaxVectorResults:   5,
		MinRelevanceScore:  0.7,

		EnableEntityExtraction: true,
		EntityThreshold:        0.8,

		EnablePersistence:   true,
		PersistenceInterval: DefaultPersistInterval,
		MaxMemoryAge:        DefaultMaxMemoryAge,
	}
}
