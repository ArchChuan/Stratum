// Package memory provides agent memory management.
package memory

import (
	"context"
	"fmt"
	"regexp"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// MemoryManager orchestrates all memory systems
type MemoryManager struct {
	shortTerm   Memory
	longTerm    VectorMemory
	entity      EntityMemory
	persistence Persistence
	pool        *pgxpool.Pool
	config      *MemoryConfig
	logger      *zap.Logger
	mu          sync.RWMutex
}

// NewMemoryManager creates a new memory manager
func NewMemoryManager(
	config *MemoryConfig,
	logger *zap.Logger,
	vectorMemory VectorMemory,
	entityMemory EntityMemory,
	persistence Persistence,
	pool *pgxpool.Pool,
) *MemoryManager {
	if config == nil {
		config = DefaultMemoryConfig()
	}

	m := &MemoryManager{
		config:      config,
		logger:      logger,
		longTerm:    vectorMemory,
		entity:      entityMemory,
		persistence: persistence,
		pool:        pool,
	}

	// Initialize short-term memory based on config
	switch {
	case config.ShortTermWindowSize > 0:
		m.shortTerm = NewConversationWindowMemory(config, logger)
	case config.EnableSummary:
		m.shortTerm = NewConversationSummaryMemory(config, logger)
	default:
		m.shortTerm = NewConversationBufferMemory(config, logger)
	}

	return m
}

// execTenant runs fn in a transaction with search_path set to the tenant schema from ctx.
func (m *MemoryManager) execTenant(ctx context.Context, tenantID string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if m.pool == nil || tenantID == "" {
		return nil
	}
	if !uuidRE.MatchString(tenantID) {
		return fmt.Errorf("memory: invalid tenant_id format")
	}
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("memory: begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()
	if _, err := tx.Exec(ctx, fmt.Sprintf(`SET LOCAL search_path = "tenant_%s", public`, tenantID)); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("memory: set search_path: %w", err)
	}
	if err := fn(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

// Add adds a memory entry to all applicable memory systems
func (m *MemoryManager) Add(ctx context.Context, entry *MemoryEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Add to short-term memory
	if err := m.shortTerm.Add(ctx, entry); err != nil {
		m.logger.Warn("failed to add to short-term memory", zap.Error(err))
	}

	// Add to long-term memory if vector search is enabled
	if m.config.EnableVectorSearch && m.longTerm != nil {
		if err := m.longTerm.AddWithVector(ctx, entry, entry.Vector); err != nil {
			m.logger.Warn("failed to add to long-term memory", zap.Error(err))
		}
	}

	// Extract entities if enabled
	if m.config.EnableEntityExtraction && m.entity != nil {
		sessionCtx := &SessionContext{
			TenantID:  entry.TenantID,
			UserID:    entry.UserID,
			SessionID: entry.SessionID,
			AgentID:   entry.AgentID,
		}
		if _, err := m.entity.ExtractEntities(ctx, entry.Content, sessionCtx); err != nil {
			m.logger.Warn("failed to extract entities", zap.Error(err))
		}
	}

	// Persist to DB when pool and tenantID are available
	if err := m.execTenant(ctx, entry.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO memory_entries (id, type, role, content, session_id, user_id, agent_id, importance, expires_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (id) DO NOTHING`,
			entry.ID, string(entry.Type), entry.Role, entry.Content,
			entry.SessionID, entry.UserID, entry.AgentID,
			entry.Importance, entry.ExpiresAt,
		)
		return err
	}); err != nil {
		m.logger.Warn("failed to persist memory entry", zap.Error(err))
	}

	return nil
}

// Get retrieves a memory entry by ID from short-term memory
func (m *MemoryManager) Get(ctx context.Context, id string) (*MemoryEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.shortTerm.Get(ctx, id)
}

// Search searches across all memory systems
func (m *MemoryManager) Search(ctx context.Context, req *MemorySearchRequest) ([]*MemorySearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var allResults []*MemorySearchResult

	// Search short-term memory
	if req.Query == "" || len(req.Types) == 0 {
		shortTermResults, err := m.shortTerm.Search(ctx, req)
		if err != nil {
			m.logger.Warn("failed to search short-term memory", zap.Error(err))
		} else {
			allResults = append(allResults, shortTermResults...)
		}
	}

	// Search long-term memory if vector search is enabled and query is provided
	if m.config.EnableVectorSearch && m.longTerm != nil && req.Query != "" {
		sessionCtx := req.Context
		if sessionCtx == nil {
			sessionCtx = &SessionContext{}
		}
		longTermResults, err := m.longTerm.SemanticSearch(ctx, req.Query, sessionCtx, req.Limit)
		if err != nil {
			m.logger.Warn("failed to search long-term memory", zap.Error(err))
		} else {
			allResults = append(allResults, longTermResults...)
		}
	}

	// Sort by score if min score is specified
	if req.MinScore > 0 {
		filtered := make([]*MemorySearchResult, 0, len(allResults))
		for _, result := range allResults {
			if result.Score >= req.MinScore {
				filtered = append(filtered, result)
			}
		}
		allResults = filtered
	}

	// Apply limit
	if req.Limit > 0 && len(allResults) > req.Limit {
		allResults = allResults[:req.Limit]
	}

	return allResults, nil
}

// Delete removes a memory entry from all memory systems
func (m *MemoryManager) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Delete from short-term memory
	if err := m.shortTerm.Delete(ctx, id); err != nil {
		m.logger.Warn("failed to delete from short-term memory", zap.Error(err))
	}

	// Note: Long-term memory deletion requires more complex handling
	// For now, we only delete from short-term

	return nil
}

// Clear removes all memory entries for a session
func (m *MemoryManager) Clear(ctx context.Context, sessionCtx *SessionContext) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Clear short-term memory
	if err := m.shortTerm.Clear(ctx, sessionCtx); err != nil {
		m.logger.Warn("failed to clear short-term memory", zap.Error(err))
	}

	return nil
}

// GetStats returns memory statistics
func (m *MemoryManager) GetStats(ctx context.Context, sessionCtx *SessionContext) (*MemoryStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	shortTermStats, err := m.shortTerm.GetStats(ctx, sessionCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to get short-term stats: %w", err)
	}

	return &MemoryStats{
		TotalEntries:     shortTermStats.TotalEntries,
		ShortTermCount:   shortTermStats.ShortTermCount,
		LongTermCount:    0,
		EntityCount:      0,
		SessionsCount:    0,
		ActiveUsers:      0,
		VectorCount:      0,
		LastAccessTime:   shortTermStats.LastAccessTime,
		StorageSizeBytes: 0,
	}, nil
}

// Cleanup removes expired entries from all memory systems
func (m *MemoryManager) Cleanup(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.shortTerm.Cleanup(ctx); err != nil {
		m.logger.Warn("failed to cleanup short-term memory", zap.Error(err))
	}

	return nil
}

// GetEntities retrieves entities for a session
func (m *MemoryManager) GetEntities(ctx context.Context, sessionCtx *SessionContext) ([]*Entity, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.entity == nil {
		return []*Entity{}, nil
	}

	return m.entity.SearchEntities(ctx, "", sessionCtx)
}

// ExtractEntities extracts entities from text
func (m *MemoryManager) ExtractEntities(ctx context.Context, text string, sessionCtx *SessionContext) ([]*Entity, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.entity == nil {
		return []*Entity{}, nil
	}

	return m.entity.ExtractEntities(ctx, text, sessionCtx)
}

// AddRelation adds a relation between entities
func (m *MemoryManager) AddRelation(ctx context.Context, relation *EntityRelation) error {
	m.mu.Lock()
	defer m.mu.RUnlock()

	if m.entity == nil {
		return fmt.Errorf("entity memory not initialized")
	}

	return m.entity.AddRelation(ctx, relation)
}

// GetEntityRelations retrieves relations for an entity
func (m *MemoryManager) GetEntityRelations(ctx context.Context, entityID string) ([]*EntityRelation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.entity == nil {
		return []*EntityRelation{}, nil
	}

	return m.entity.GetEntityRelations(ctx, entityID)
}

// GetSummary retrieves conversation summary
func (m *MemoryManager) GetSummary(ctx context.Context, sessionCtx *SessionContext) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Try to get summary from summary memory
	if summaryMem, ok := m.shortTerm.(*ConversationSummaryMemory); ok {
		return summaryMem.GetSummary(), nil
	}

	return "", fmt.Errorf("summary not available")
}

// GetRecentMemory retrieves recent memory entries
func (m *MemoryManager) GetRecentMemory(ctx context.Context, sessionCtx *SessionContext, limit int) ([]*MemoryEntry, error) {
	req := &MemorySearchRequest{
		Context: sessionCtx,
		Limit:   limit,
	}

	results, err := m.Search(ctx, req)
	if err != nil {
		return nil, err
	}

	entries := make([]*MemoryEntry, 0, len(results))
	for _, result := range results {
		entries = append(entries, result.Entry)
	}

	return entries, nil
}

// AddWithVector adds a memory entry with a pre-computed vector
func (m *MemoryManager) AddWithVector(ctx context.Context, entry *MemoryEntry, vector []float32) error {
	entry.Vector = vector
	return m.Add(ctx, entry)
}

// SemanticSearch performs semantic similarity search
func (m *MemoryManager) SemanticSearch(ctx context.Context, query string, sessionCtx *SessionContext, limit int) ([]*MemorySearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.longTerm == nil {
		return []*MemorySearchResult{}, nil
	}

	return m.longTerm.SemanticSearch(ctx, query, sessionCtx, limit)
}

// HybridSearch combines keyword and semantic search
func (m *MemoryManager) HybridSearch(ctx context.Context, req *MemorySearchRequest) ([]*MemorySearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.longTerm == nil {
		return m.Search(ctx, req)
	}

	return m.longTerm.HybridSearch(ctx, req)
}

// UpdateEntity updates an entity
func (m *MemoryManager) UpdateEntity(ctx context.Context, entity *Entity) error {
	m.mu.Lock()
	defer m.mu.RUnlock()

	if m.entity == nil {
		return fmt.Errorf("entity memory not initialized")
	}

	return m.entity.UpdateEntity(ctx, entity)
}

// GetEntity retrieves an entity by ID
func (m *MemoryManager) GetEntity(ctx context.Context, id string) (*Entity, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.entity == nil {
		return nil, fmt.Errorf("entity memory not initialized")
	}

	return m.entity.GetEntity(ctx, id)
}
