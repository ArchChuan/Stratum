// Package application provides agent memory management orchestration.
package application

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"

	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// MemoryManager orchestrates the memory subsystems (vector, entity, persisted
// repo) behind a single domain interface. All persistence work is delegated
// to a domain-side MemoryRepo port; the application layer holds no SQL.
type MemoryManager struct {
	longTerm    VectorMemory
	entity      EntityMemory
	persistence Persistence
	repo        memport.MemoryRepo
	config      *MemoryConfig
	logger      *zap.Logger
	mu          sync.RWMutex
}

// NewMemoryManager wires a MemoryManager. repo may be nil when persistence
// is disabled (tests, in-memory mode).
func NewMemoryManager(
	config *MemoryConfig,
	logger *zap.Logger,
	vectorMemory VectorMemory,
	entityMemory EntityMemory,
	persistence Persistence,
	repo memport.MemoryRepo,
) *MemoryManager {
	if config == nil {
		config = DefaultMemoryConfig()
	}
	return &MemoryManager{
		config:      config,
		logger:      logger,
		longTerm:    vectorMemory,
		entity:      entityMemory,
		persistence: persistence,
		repo:        repo,
	}
}

// Add adds a memory entry to all applicable memory systems.
func (m *MemoryManager) Add(ctx context.Context, entry *MemoryEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.config.EnableVectorSearch && m.longTerm != nil {
		if err := m.longTerm.AddWithVector(ctx, entry, entry.Vector); err != nil {
			sc, _ := observability.SpanFromContext(ctx)
			m.logger.Warn("failed to add to long-term memory", zap.String("trace_id", sc.TraceID), zap.Error(err))
		}
	}

	if m.config.EnableEntityExtraction && m.entity != nil {
		sessionCtx := &SessionContext{
			TenantID:  entry.TenantID,
			UserID:    entry.UserID,
			SessionID: entry.SessionID,
			AgentID:   entry.AgentID,
		}
		if _, err := m.entity.ExtractEntities(ctx, entry.Content, sessionCtx); err != nil {
			sc, _ := observability.SpanFromContext(ctx)
			m.logger.Warn("failed to extract entities", zap.String("trace_id", sc.TraceID), zap.Error(err))
		}
	}

	if m.repo != nil {
		if err := m.repo.Add(ctx, entry); err != nil {
			sc, _ := observability.SpanFromContext(ctx)
			m.logger.Warn("failed to persist memory entry", zap.String("trace_id", sc.TraceID), zap.Error(err))
		}
	}
	return nil
}

// Get retrieves a memory entry by ID from the repo.
func (m *MemoryManager) Get(ctx context.Context, id string) (*MemoryEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.repo == nil {
		return nil, ErrNotFound
	}
	tenantID, _ := ctx.Value(tenantIDKey{}).(string)
	return m.repo.Get(ctx, tenantID, id)
}

// Search searches across all memory systems.
func (m *MemoryManager) Search(ctx context.Context, req *MemorySearchRequest) ([]*MemorySearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var allResults []*MemorySearchResult

	if m.repo != nil {
		tenantID, sessionID := "", ""
		if req.Context != nil {
			tenantID = req.Context.TenantID
			sessionID = req.Context.SessionID
		}
		entries, err := m.repo.Search(ctx, tenantID, sessionID, req.Limit)
		if err != nil {
			sc, _ := observability.SpanFromContext(ctx)
			m.logger.Warn("failed to search memory in db", zap.String("trace_id", sc.TraceID), zap.Error(err))
		}
		for _, e := range entries {
			allResults = append(allResults, &MemorySearchResult{Entry: e, Score: e.Importance})
		}
	}

	if m.config.EnableVectorSearch && m.longTerm != nil && req.Query != "" {
		sessionCtx := req.Context
		if sessionCtx == nil {
			sessionCtx = &SessionContext{}
		}
		longTermResults, err := m.longTerm.SemanticSearch(ctx, req.Query, sessionCtx, req.Limit)
		if err != nil {
			sc, _ := observability.SpanFromContext(ctx)
			m.logger.Warn("failed to search long-term memory", zap.String("trace_id", sc.TraceID), zap.Error(err))
		} else {
			allResults = append(allResults, longTermResults...)
		}
	}

	if req.MinScore > 0 {
		filtered := make([]*MemorySearchResult, 0, len(allResults))
		for _, result := range allResults {
			if result.Score >= req.MinScore {
				filtered = append(filtered, result)
			}
		}
		allResults = filtered
	}
	if req.Limit > 0 && len(allResults) > req.Limit {
		allResults = allResults[:req.Limit]
	}
	return allResults, nil
}

// Delete removes a memory entry from the repo.
func (m *MemoryManager) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.repo == nil {
		return nil
	}
	tenantID, _ := ctx.Value(tenantIDKey{}).(string)
	return m.repo.Delete(ctx, tenantID, id)
}

// Clear removes all memory entries for a session.
func (m *MemoryManager) Clear(ctx context.Context, sessionCtx *SessionContext) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.repo == nil || sessionCtx == nil {
		return nil
	}
	return m.repo.ClearSession(ctx, sessionCtx.TenantID, sessionCtx.SessionID)
}

// GetStats returns memory statistics.
func (m *MemoryManager) GetStats(ctx context.Context, sessionCtx *SessionContext) (*MemoryStats, error) {
	if m.repo == nil || sessionCtx == nil {
		return &MemoryStats{}, nil
	}
	return m.repo.Stats(ctx, sessionCtx.TenantID)
}

// Cleanup is a no-op: expiry handled at query time via expires_at.
func (m *MemoryManager) Cleanup(_ context.Context) error { return nil }

// GetEntities retrieves entities for a session.
func (m *MemoryManager) GetEntities(ctx context.Context, sessionCtx *SessionContext) ([]*Entity, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.entity == nil {
		return []*Entity{}, nil
	}
	return m.entity.SearchEntities(ctx, "", sessionCtx)
}

// ExtractEntities extracts entities from text.
func (m *MemoryManager) ExtractEntities(ctx context.Context, text string, sessionCtx *SessionContext) ([]*Entity, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.entity == nil {
		return []*Entity{}, nil
	}
	return m.entity.ExtractEntities(ctx, text, sessionCtx)
}

// AddRelation adds a relation between entities.
func (m *MemoryManager) AddRelation(ctx context.Context, relation *EntityRelation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entity == nil {
		return fmt.Errorf("entity memory not initialized")
	}
	return m.entity.AddRelation(ctx, relation)
}

// GetEntityRelations retrieves relations for an entity.
func (m *MemoryManager) GetEntityRelations(ctx context.Context, entityID string) ([]*EntityRelation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.entity == nil {
		return []*EntityRelation{}, nil
	}
	return m.entity.GetEntityRelations(ctx, entityID)
}

// GetSummary retrieves conversation summary.
func (m *MemoryManager) GetSummary(ctx context.Context, sessionCtx *SessionContext) (string, error) {
	if m.repo == nil || sessionCtx == nil {
		return "", nil
	}
	return m.repo.GetSummary(ctx, sessionCtx.TenantID, sessionCtx.SessionID)
}

// GetRecentMemory retrieves recent memory entries.
func (m *MemoryManager) GetRecentMemory(ctx context.Context, sessionCtx *SessionContext, limit int) ([]*MemoryEntry, error) {
	results, err := m.Search(ctx, &MemorySearchRequest{Context: sessionCtx, Limit: limit})
	if err != nil {
		return nil, err
	}
	entries := make([]*MemoryEntry, 0, len(results))
	for _, r := range results {
		entries = append(entries, r.Entry)
	}
	return entries, nil
}

// AddWithVector adds a memory entry with a pre-computed vector.
func (m *MemoryManager) AddWithVector(ctx context.Context, entry *MemoryEntry, vector []float32) error {
	entry.Vector = vector
	return m.Add(ctx, entry)
}

// SemanticSearch performs semantic similarity search.
func (m *MemoryManager) SemanticSearch(ctx context.Context, query string, sessionCtx *SessionContext, limit int) ([]*MemorySearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.longTerm == nil {
		return []*MemorySearchResult{}, nil
	}
	return m.longTerm.SemanticSearch(ctx, query, sessionCtx, limit)
}

// HybridSearch combines keyword and semantic search.
func (m *MemoryManager) HybridSearch(ctx context.Context, req *MemorySearchRequest) ([]*MemorySearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.longTerm == nil {
		return m.Search(ctx, req)
	}
	return m.longTerm.HybridSearch(ctx, req)
}

// UpdateEntity updates an entity.
func (m *MemoryManager) UpdateEntity(ctx context.Context, entity *Entity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entity == nil {
		return fmt.Errorf("entity memory not initialized")
	}
	return m.entity.UpdateEntity(ctx, entity)
}

// GetEntity retrieves an entity by ID.
func (m *MemoryManager) GetEntity(ctx context.Context, id string) (*Entity, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.entity == nil {
		return nil, fmt.Errorf("entity memory not initialized")
	}
	return m.entity.GetEntity(ctx, id)
}
