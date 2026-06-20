// Package application provides agent memory management orchestration.
package application

import (
	"context"
	"sync"

	"go.uber.org/zap"

	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// MemoryManager orchestrates memory persistence behind a single domain interface.
// All storage is delegated to the MemoryRepo port; application layer holds no SQL.
type MemoryManager struct {
	repo   memport.MemoryRepo
	logger *zap.Logger
	mu     sync.RWMutex
}

// NewMemoryManager wires a MemoryManager. repo may be nil (tests / disabled persistence).
func NewMemoryManager(logger *zap.Logger, repo memport.MemoryRepo) *MemoryManager {
	return &MemoryManager{repo: repo, logger: logger}
}

// Add persists a memory entry.
func (m *MemoryManager) Add(ctx context.Context, entry *MemoryEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.repo == nil {
		return nil
	}
	if err := m.repo.Add(ctx, entry); err != nil {
		sc, _ := observability.SpanFromContext(ctx)
		m.logger.Warn("failed to persist memory entry", zap.String("trace_id", sc.TraceID), zap.Error(err))
	}
	return nil
}

// Get retrieves a memory entry by ID.
func (m *MemoryManager) Get(ctx context.Context, id string) (*MemoryEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.repo == nil {
		return nil, ErrNotFound
	}
	tenantID, _ := ctx.Value(tenantIDKey{}).(string)
	return m.repo.Get(ctx, tenantID, id)
}

// Search searches memory entries.
func (m *MemoryManager) Search(ctx context.Context, req *MemorySearchRequest) ([]*MemorySearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.repo == nil {
		return nil, nil
	}
	tenantID, userID := "", ""
	if req.Context != nil {
		tenantID = req.Context.TenantID
		userID = req.Context.UserID
	}
	entries, err := m.repo.Search(ctx, tenantID, userID, req.Query, req.Limit)
	if err != nil {
		sc, _ := observability.SpanFromContext(ctx)
		m.logger.Warn("failed to search memory in db", zap.String("trace_id", sc.TraceID), zap.Error(err))
		return nil, nil
	}
	results := make([]*MemorySearchResult, 0, len(entries))
	for _, e := range entries {
		results = append(results, &MemorySearchResult{Entry: e, Score: e.Importance})
	}
	if req.MinScore > 0 {
		filtered := results[:0]
		for _, r := range results {
			if r.Score >= req.MinScore {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}
	if req.Limit > 0 && len(results) > req.Limit {
		results = results[:req.Limit]
	}
	return results, nil
}

// Delete removes a memory entry.
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

// Cleanup is a no-op: expiry is handled at query time via expires_at.
func (m *MemoryManager) Cleanup(_ context.Context) error { return nil }

// GetSummary retrieves the latest conversation summary for a session.
func (m *MemoryManager) GetSummary(ctx context.Context, sessionCtx *SessionContext) (string, error) {
	if m.repo == nil || sessionCtx == nil {
		return "", nil
	}
	return m.repo.GetSummary(ctx, sessionCtx.TenantID, sessionCtx.SessionID)
}

// GetRecentMemory retrieves recent memory entries ordered by importance.
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
