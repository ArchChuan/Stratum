package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// compactionPoolIface is the minimal pool surface needed by PgCompactionStore
// (allows pgxmock injection).
type compactionPoolIface interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// PgCompactionStore implements port.CompactionStore over the tenant-scoped
// chat_compaction_summaries table. One row per conversation; covered_until
// advances monotonically as older rounds get compacted. All tenant-scoped SQL
// goes through pkg/storage/postgres.ExecTenantWith (shared boundary, never a
// per-repository copy).
type PgCompactionStore struct {
	pool compactionPoolIface
}

// NewPgCompactionStore creates a PgCompactionStore.
func NewPgCompactionStore(pool *pgxpool.Pool) *PgCompactionStore {
	return &PgCompactionStore{pool: pool}
}

// GetCoverage reads the latest compaction coverage for a conversation.
// Returns (nil, nil) when no coverage exists yet (first compaction); any
// persistence failure is returned so the caller degrades to a full re-load
// (fail closed, never silently drop context).
func (s *PgCompactionStore) GetCoverage(ctx context.Context, tenantID, conversationID string) (*domain.CompactionCoverage, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("compaction_store: persistence pool is nil")
	}
	var cov domain.CompactionCoverage
	err := pgstore.ExecTenantWith(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT covered_until, summary, version
			 FROM chat_compaction_summaries
			 WHERE conversation_id = $1`,
			conversationID,
		).Scan(&cov.CoveredUntil, &cov.Summary, &cov.Version)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("compaction_store: get coverage: %w", err)
	}
	return &cov, nil
}

// Upsert writes or advances the compaction coverage for a conversation.
// Existing rows are advanced (covered_until/summary/source_end/token updated,
// version incremented) so the most recent compaction wins. source_start is
// preserved on conflict: coverage is cumulative (model A) — the segment starts
// at the earliest covered message and only covered_until advances. Persistence
// failures propagate — callers must surface them, never swallow.
func (s *PgCompactionStore) Upsert(ctx context.Context, tenantID string, seg *domain.CompactionSegment) error {
	if s.pool == nil {
		return fmt.Errorf("compaction_store: persistence pool is nil")
	}
	err := pgstore.ExecTenantWith(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO chat_compaction_summaries
			    (conversation_id, covered_until, summary, source_start, source_end, token_count)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (conversation_id) DO UPDATE SET
			    covered_until = EXCLUDED.covered_until,
			    summary       = EXCLUDED.summary,
			    source_start  = chat_compaction_summaries.source_start,
			    source_end    = EXCLUDED.source_end,
			    token_count   = EXCLUDED.token_count,
			    version       = chat_compaction_summaries.version + 1,
			    updated_at    = NOW()`,
			seg.ConversationID, seg.CoveredUntil, seg.Summary,
			seg.SourceStart, seg.SourceEnd, seg.TokenCount,
		)
		return err
	})
	if err != nil {
		return fmt.Errorf("compaction_store: upsert coverage: %w", err)
	}
	return nil
}
