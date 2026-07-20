// Package persistence is the postgres-backed memory repository.
package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

type tenantPool interface {
	Begin(context.Context) (pgx.Tx, error)
}

// MemoryRepo persists memory entries to PostgreSQL using tenant schemas.
type MemoryRepo struct {
	pool tenantPool
}

// NewMemoryRepo wires a MemoryRepo over a pgx pool. Disabled persistence must
// be represented by an explicit noop implementation at wiring time.
func NewMemoryRepo(pool *pgxpool.Pool) *MemoryRepo {
	if pool == nil {
		return &MemoryRepo{}
	}
	return &MemoryRepo{pool: pool}
}

// execTenant runs fn in a transaction with search_path set to the tenant
// schema. Missing configuration or tenant identity fails closed.
func (r *MemoryRepo) execTenant(ctx context.Context, tenantID string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if r.pool == nil {
		return fmt.Errorf("memory: persistence pool is nil")
	}
	return pgstore.ExecTenantWith(ctx, r.pool, tenantID, fn)
}

// Add persists a memory entry. ON CONFLICT DO NOTHING — caller may treat
// duplicate ids as a no-op.
func (r *MemoryRepo) Add(ctx context.Context, entry *domain.MemoryEntry) error {
	return r.execTenant(ctx, entry.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO memory_entries (id, type, role, content, session_id, user_id, agent_id, importance, tags, metadata, expires_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT (id) DO NOTHING`,
			entry.ID, string(entry.Type), entry.Role, entry.Content,
			entry.SessionID, entry.UserID, entry.AgentID,
			entry.Importance, entry.Tags, entry.Metadata, entry.ExpiresAt,
		)
		return err
	})
}

// Get fetches a memory entry by id within tenant schema.
// Returns domain.ErrEntryNotFound when no row matches.
func (r *MemoryRepo) Get(ctx context.Context, tenantID, id string) (*domain.MemoryEntry, error) {
	var entry *domain.MemoryEntry
	if err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT id, type, role, content, session_id, user_id, agent_id, importance, tags, metadata, expires_at
			 FROM memory_entries WHERE id = $1`, id)
		e := &domain.MemoryEntry{}
		if err := row.Scan(&e.ID, &e.Type, &e.Role, &e.Content, &e.SessionID, &e.UserID, &e.AgentID,
			&e.Importance, &e.Tags, &e.Metadata, &e.ExpiresAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrEntryNotFound
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

// Search returns up to limit entries scoped by userID (empty = all users in tenant).
func (r *MemoryRepo) Search(ctx context.Context, tenantID, userID, query string, limit int) ([]*domain.MemoryEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	var out []*domain.MemoryEntry
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, type, role, content, session_id, user_id, agent_id, importance
			 FROM memory_entries
			 WHERE ($1::text = '' OR user_id = $1::text)
			   AND ($2::text = '' OR content ILIKE '%' || $2 || '%')
			 ORDER BY importance DESC
			 LIMIT $3`,
			userID, query, limit,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e := &domain.MemoryEntry{}
			if err := rows.Scan(&e.ID, &e.Type, &e.Role, &e.Content, &e.SessionID, &e.UserID, &e.AgentID, &e.Importance); err != nil {
				continue
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

// Delete removes a single entry by id.
func (r *MemoryRepo) Delete(ctx context.Context, tenantID, id string) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM memory_entries WHERE id = $1`, id)
		return err
	})
}

// ClearSession deletes all memory entries for a sessionID.
func (r *MemoryRepo) ClearSession(ctx context.Context, tenantID, sessionID string) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM memory_entries WHERE session_id = $1`, sessionID)
		return err
	})
}

// DeleteAllByUser hard-deletes every memory entry belonging to userID within the tenant schema.
func (r *MemoryRepo) DeleteAllByUser(ctx context.Context, tenantID, userID string) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		for _, query := range []string{
			`DELETE FROM memory_outbox WHERE user_id = $1`,
			`DELETE FROM memory_extraction_queue WHERE user_id = $1`,
			`DELETE FROM memory_summaries WHERE user_id = $1`,
			`DELETE FROM memory_active_snapshots WHERE user_id = $1`,
			`DELETE FROM memory_entries WHERE user_id = $1`,
		} {
			if _, err := tx.Exec(ctx, query, userID); err != nil {
				return fmt.Errorf("memory: delete user lifecycle data: %w", err)
			}
		}
		return nil
	})
}

// DeleteAllByAgent hard-deletes every memory entry belonging to agentID within the tenant schema.
func (r *MemoryRepo) DeleteAllByAgent(ctx context.Context, tenantID, agentID string) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		// outbox, snapshots, and legacy entries are agent/conversation lifecycle data.
		// Summaries also contain shared canonical history, so provenance agent_id alone is not ownership.
		for _, query := range []string{
			`DELETE FROM memory_outbox WHERE agent_id = $1`,
			`DELETE FROM memory_extraction_queue WHERE agent_id = $1`,
			`DELETE FROM memory_summaries WHERE agent_id = $1 AND scope = 'agent'`,
			`DELETE FROM memory_active_snapshots WHERE agent_id = $1`,
			`DELETE FROM memory_entries WHERE agent_id = $1`,
		} {
			if _, err := tx.Exec(ctx, query, agentID); err != nil {
				return fmt.Errorf("memory: delete agent lifecycle data: %w", err)
			}
		}
		return nil
	})
}

// Stats aggregates per-tenant memory volume from memory_entries / entities /
// chat_conversations. Errors are swallowed (matches legacy behaviour) — caller
// receives a zero-value MemoryStats.
func (r *MemoryRepo) Stats(ctx context.Context, tenantID string) (*domain.MemoryStats, error) {
	stats := &domain.MemoryStats{}
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM memory_entries").Scan(&stats.TotalEntries); err != nil {
			return fmt.Errorf("memory stats total entries: %w", err)
		}
		if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM memory_entries WHERE enriched_at IS NOT NULL").Scan(&stats.LongTermCount); err != nil {
			return fmt.Errorf("memory stats long-term entries: %w", err)
		}
		stats.ShortTermCount = stats.TotalEntries - stats.LongTermCount
		if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM memory_entities").Scan(&stats.EntityCount); err != nil {
			return fmt.Errorf("memory stats entities: %w", err)
		}
		if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM chat_conversations").Scan(&stats.SessionsCount); err != nil {
			return fmt.Errorf("memory stats sessions: %w", err)
		}
		if err := tx.QueryRow(ctx, "SELECT COUNT(DISTINCT user_id) FROM memory_entries WHERE user_id IS NOT NULL").Scan(&stats.ActiveUsers); err != nil {
			return fmt.Errorf("memory stats active users: %w", err)
		}
		stats.VectorCount = stats.LongTermCount
		return tx.QueryRow(ctx, "SELECT COALESCE(MAX(created_at), '1970-01-01') FROM memory_entries").Scan(&stats.LastAccessTime)
	})
	if err != nil {
		return nil, err
	}
	return stats, nil
}

// GetSummary fetches the latest stored summary for a session.
// Returns domain.ErrSessionNotFound when no summary exists.
func (r *MemoryRepo) GetSummary(ctx context.Context, tenantID, sessionID string) (string, error) {
	var summary string
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			"SELECT summary FROM memory_summaries WHERE conversation_id = $1 ORDER BY created_at DESC LIMIT 1",
			sessionID)
		if err := row.Scan(&summary); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrSessionNotFound
			}
			return err
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return summary, nil
}
