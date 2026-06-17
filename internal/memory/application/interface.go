// Package application provides agent memory management orchestration.
package application

import (
	"context"

	memdomain "github.com/byteBuilderX/stratum/internal/memory/domain"
)

// Memory is the core interface for all memory systems.
type Memory interface {
	Add(ctx context.Context, entry *MemoryEntry) error
	Get(ctx context.Context, id string) (*MemoryEntry, error)
	Search(ctx context.Context, req *MemorySearchRequest) ([]*MemorySearchResult, error)
	Delete(ctx context.Context, id string) error
	Clear(ctx context.Context, sessionCtx *SessionContext) error
	GetStats(ctx context.Context, sessionCtx *SessionContext) (*MemoryStats, error)
	Cleanup(ctx context.Context) error
}

// VectorMemory extends Memory with vector search capabilities.
type VectorMemory interface {
	Memory
	AddWithVector(ctx context.Context, entry *MemoryEntry, vector []float32) error
	SemanticSearch(ctx context.Context, query string, sessionCtx *SessionContext, limit int) ([]*MemorySearchResult, error)
	HybridSearch(ctx context.Context, req *MemorySearchRequest) ([]*MemorySearchResult, error)
}

// EntityMemory handles entity extraction and relation management.
type EntityMemory interface {
	ExtractEntities(ctx context.Context, text string, sessionCtx *SessionContext) ([]*Entity, error)
	GetEntity(ctx context.Context, id string) (*Entity, error)
	SearchEntities(ctx context.Context, query string, sessionCtx *SessionContext) ([]*Entity, error)
	AddRelation(ctx context.Context, relation *EntityRelation) error
	GetEntityRelations(ctx context.Context, entityID string) ([]*EntityRelation, error)
	UpdateEntity(ctx context.Context, entity *Entity) error
}

// Persistence handles memory persistence to storage.
type Persistence interface {
	Save(ctx context.Context, entry *MemoryEntry) error
	Load(ctx context.Context, sessionCtx *SessionContext) ([]*MemoryEntry, error)
	SaveEntities(ctx context.Context, entities []*Entity) error
	LoadEntities(ctx context.Context, sessionCtx *SessionContext) ([]*Entity, error)
	DeleteSession(ctx context.Context, sessionID string) error
	DeleteTenant(ctx context.Context, tenantID string) error
}

// MemoryOptions configures memory operations.
type MemoryOptions struct {
	EnableShortTerm bool
	EnableLongTerm  bool
	EnableEntity    bool
	EnableSummary   bool
}

// DefaultMemoryOptions returns default memory options.
func DefaultMemoryOptions() *MemoryOptions {
	return &MemoryOptions{
		EnableShortTerm: true,
		EnableLongTerm:  true,
		EnableEntity:    true,
		EnableSummary:   true,
	}
}

// MemoryFilter is re-exported from domain so existing call sites stay
// source-compatible. Canonical definition lives in memory/domain/types.go.
type MemoryFilter = memdomain.MemoryFilter
