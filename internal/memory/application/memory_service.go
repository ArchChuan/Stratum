// Package application provides agent memory management orchestration.
package application

import (
	"context"
	"fmt"
	"regexp"
	"sync"

	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// MemoryManager orchestrates all memory systems
type MemoryManager struct {
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

	// Add to long-term memory if vector search is enabled
	if m.config.EnableVectorSearch && m.longTerm != nil {
		if err := m.longTerm.AddWithVector(ctx, entry, entry.Vector); err != nil {
			sc, _ := observability.SpanFromContext(ctx)
			m.logger.Warn("failed to add to long-term memory", zap.String("trace_id", sc.TraceID), zap.Error(err))
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
			sc, _ := observability.SpanFromContext(ctx)
			m.logger.Warn("failed to extract entities", zap.String("trace_id", sc.TraceID), zap.Error(err))
		}
	}

	// Persist to DB
	if err := m.execTenant(ctx, entry.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO memory_entries (id, type, role, content, session_id, user_id, agent_id, importance, tags, metadata, expires_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT (id) DO NOTHING`,
			entry.ID, string(entry.Type), entry.Role, entry.Content,
			entry.SessionID, entry.UserID, entry.AgentID,
			entry.Importance, entry.Tags, entry.Metadata, entry.ExpiresAt,
		)
		return err
	}); err != nil {
		sc, _ := observability.SpanFromContext(ctx)
		m.logger.Warn("failed to persist memory entry", zap.String("trace_id", sc.TraceID), zap.Error(err))
	}

	return nil
}

// Get retrieves a memory entry by ID from the database
func (m *MemoryManager) Get(ctx context.Context, id string) (*MemoryEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.pool == nil {
		return nil, ErrNotFound
	}

	var entry *MemoryEntry
	tenantID, _ := ctx.Value(tenantIDKey{}).(string)
	if err := m.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT id, type, role, content, session_id, user_id, agent_id, importance, tags, metadata, expires_at
			 FROM memory_entries WHERE id = $1`, id)
		e := &MemoryEntry{}
		if err := row.Scan(&e.ID, &e.Type, &e.Role, &e.Content, &e.SessionID, &e.UserID, &e.AgentID,
			&e.Importance, &e.Tags, &e.Metadata, &e.ExpiresAt); err != nil {
			if err == pgx.ErrNoRows {
				return ErrNotFound
			}
			return err
		}
		entry = e
		return nil
	}); err != nil {
		return nil, err
	}
	return entry, nil
}

// Search searches across all memory systems
func (m *MemoryManager) Search(ctx context.Context, req *MemorySearchRequest) ([]*MemorySearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var allResults []*MemorySearchResult

	// Search database for matching entries
	if m.pool != nil {
		tenantID := ""
		sessionID := ""
		if req.Context != nil {
			tenantID = req.Context.TenantID
			sessionID = req.Context.SessionID
		}
		lim := req.Limit
		if lim <= 0 {
			lim = 20
		}
		if err := m.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
			rows, err := tx.Query(ctx,
				`SELECT id, type, role, content, session_id, user_id, agent_id, importance
				 FROM memory_entries
				 WHERE ($1 = '' OR session_id = $1)
				 ORDER BY importance DESC
				 LIMIT $2`,
				sessionID, lim,
			)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				e := &MemoryEntry{}
				if err := rows.Scan(&e.ID, &e.Type, &e.Role, &e.Content, &e.SessionID, &e.UserID, &e.AgentID, &e.Importance); err != nil {
					continue
				}
				allResults = append(allResults, &MemorySearchResult{Entry: e, Score: e.Importance})
			}
			return rows.Err()
		}); err != nil {
			sc, _ := observability.SpanFromContext(ctx)
			m.logger.Warn("failed to search memory in db", zap.String("trace_id", sc.TraceID), zap.Error(err))
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
			sc, _ := observability.SpanFromContext(ctx)
			m.logger.Warn("failed to search long-term memory", zap.String("trace_id", sc.TraceID), zap.Error(err))
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

// Delete removes a memory entry from the database
func (m *MemoryManager) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.pool == nil {
		return nil
	}
	tenantID, _ := ctx.Value(tenantIDKey{}).(string)
	return m.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM memory_entries WHERE id = $1`, id)
		return err
	})
}

// Clear removes all memory entries for a session from the database
func (m *MemoryManager) Clear(ctx context.Context, sessionCtx *SessionContext) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.pool == nil || sessionCtx == nil {
		return nil
	}
	return m.execTenant(ctx, sessionCtx.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM memory_entries WHERE session_id = $1`, sessionCtx.SessionID)
		return err
	})
}

// GetStats returns memory statistics by querying actual pipeline tables.
func (m *MemoryManager) GetStats(ctx context.Context, sessionCtx *SessionContext) (*MemoryStats, error) {
	if m.pool == nil || sessionCtx == nil || sessionCtx.TenantID == "" {
		return &MemoryStats{}, nil
	}

	stats := &MemoryStats{}
	err := m.execTenant(ctx, sessionCtx.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		_ = tx.QueryRow(ctx, "SELECT COUNT(*) FROM memory_entries").Scan(&stats.TotalEntries)
		_ = tx.QueryRow(ctx, "SELECT COUNT(*) FROM memory_entries WHERE enriched_at IS NOT NULL").Scan(&stats.LongTermCount)
		stats.ShortTermCount = stats.TotalEntries - stats.LongTermCount
		_ = tx.QueryRow(ctx, "SELECT COUNT(*) FROM entities").Scan(&stats.EntityCount)
		_ = tx.QueryRow(ctx, "SELECT COUNT(*) FROM chat_conversations").Scan(&stats.SessionsCount)
		_ = tx.QueryRow(ctx, "SELECT COUNT(DISTINCT user_id) FROM memory_entries WHERE user_id IS NOT NULL").Scan(&stats.ActiveUsers)
		stats.VectorCount = stats.LongTermCount
		_ = tx.QueryRow(ctx, "SELECT COALESCE(MAX(created_at), '1970-01-01') FROM memory_entries").Scan(&stats.LastAccessTime)
		return nil
	})
	if err != nil {
		return &MemoryStats{}, nil
	}
	return stats, nil
}

// Cleanup removes expired entries from the database
func (m *MemoryManager) Cleanup(ctx context.Context) error {
	// No-op: expiry is handled at query time via expires_at column
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
	defer m.mu.Unlock()

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
	var summary string
	err := m.execTenant(ctx, sessionCtx.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			"SELECT summary FROM memory_summaries WHERE conversation_id = $1 ORDER BY created_at DESC LIMIT 1",
			sessionCtx.SessionID).Scan(&summary)
	})
	if err != nil {
		return "", err
	}
	return summary, nil
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
	defer m.mu.Unlock()

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
