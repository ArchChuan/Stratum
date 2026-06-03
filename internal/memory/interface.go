// Package memory provides agent memory management.
package memory

import (
	"context"
	"time"
)

// Memory is the core interface for all memory systems
type Memory interface {
	// Add adds a memory entry
	Add(ctx context.Context, entry *MemoryEntry) error

	// Get retrieves a memory entry by ID
	Get(ctx context.Context, id string) (*MemoryEntry, error)

	// Search searches memory entries
	Search(ctx context.Context, req *MemorySearchRequest) ([]*MemorySearchResult, error)

	// Delete removes a memory entry
	Delete(ctx context.Context, id string) error

	// Clear removes all memory entries for a context
	Clear(ctx context.Context, sessionCtx *SessionContext) error

	// GetStats returns memory statistics
	GetStats(ctx context.Context, sessionCtx *SessionContext) (*MemoryStats, error)

	// Cleanup removes expired entries
	Cleanup(ctx context.Context) error
}

// VectorMemory extends Memory with vector search capabilities
type VectorMemory interface {
	Memory

	// AddWithVector adds a memory entry with pre-computed vector
	AddWithVector(ctx context.Context, entry *MemoryEntry, vector []float32) error

	// SemanticSearch performs semantic similarity search
	SemanticSearch(ctx context.Context, query string, sessionCtx *SessionContext, limit int) ([]*MemorySearchResult, error)

	// HybridSearch combines keyword and semantic search
	HybridSearch(ctx context.Context, req *MemorySearchRequest) ([]*MemorySearchResult, error)
}

// EntityMemory handles entity extraction and relation management
type EntityMemory interface {
	// ExtractEntities extracts entities from text
	ExtractEntities(ctx context.Context, text string, sessionCtx *SessionContext) ([]*Entity, error)

	// GetEntity retrieves an entity
	GetEntity(ctx context.Context, id string) (*Entity, error)

	// SearchEntities searches entities by name or type
	SearchEntities(ctx context.Context, query string, sessionCtx *SessionContext) ([]*Entity, error)

	// AddRelation adds a relation between entities
	AddRelation(ctx context.Context, relation *EntityRelation) error

	// GetEntityRelations retrieves relations for an entity
	GetEntityRelations(ctx context.Context, entityID string) ([]*EntityRelation, error)

	// UpdateEntity updates an entity
	UpdateEntity(ctx context.Context, entity *Entity) error
}

// Persistence handles memory persistence to storage
type Persistence interface {
	// Save saves a memory entry
	Save(ctx context.Context, entry *MemoryEntry) error

	// Load loads memory entries for a session
	Load(ctx context.Context, sessionCtx *SessionContext) ([]*MemoryEntry, error)

	// SaveEntities saves entities
	SaveEntities(ctx context.Context, entities []*Entity) error

	// LoadEntities loads entities for a session
	LoadEntities(ctx context.Context, sessionCtx *SessionContext) ([]*Entity, error)

	// DeleteSession removes all data for a session
	DeleteSession(ctx context.Context, sessionID string) error

	// DeleteTenant removes all data for a tenant
	DeleteTenant(ctx context.Context, tenantID string) error
}

// MemoryOptions configures memory operations
type MemoryOptions struct {
	EnableShortTerm bool
	EnableLongTerm  bool
	EnableEntity    bool
	EnableSummary   bool
}

// DefaultMemoryOptions returns default memory options
func DefaultMemoryOptions() *MemoryOptions {
	return &MemoryOptions{
		EnableShortTerm: true,
		EnableLongTerm:  true,
		EnableEntity:    true,
		EnableSummary:   true,
	}
}

// MemoryFilter provides filtering options for memory operations
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

// ApplyFilter applies filter to a memory entry
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
	if !f.StartTime.IsZero() && entry.Timestamp.Before(*f.StartTime) {
		return false
	}
	if !f.EndTime.IsZero() && entry.Timestamp.After(*f.EndTime) {
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
