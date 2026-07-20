// Package persistence — Postgres adapter for the agent execution-history
// store. Implements port.ExecutionRepo (and the application.ExecutionStore
// interface alias) via per-tenant search_path.

package persistence

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgExecutionStore persists agent_executions rows in the tenant schema.
type PgExecutionStore struct {
	pool *pgxpool.Pool
}

// NewPgExecutionStore constructs a Postgres-backed execution store.
func NewPgExecutionStore(pool *pgxpool.Pool) *PgExecutionStore {
	return &PgExecutionStore{pool: pool}
}

func (s *PgExecutionStore) execTenant(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	tc, ok := tenantdb.FromContext(ctx)
	if !ok || tc.TenantID == "" {
		return fmt.Errorf("execution_store: missing tenant context")
	}
	return pgstore.Wrap(s.pool).ExecTenant(ctx, tc.TenantID, fn)
}

// Insert writes an execution record into the tenant schema. Safe to call in a goroutine.
func (s *PgExecutionStore) Insert(ctx context.Context, r domain.ExecutionRecord) error {
	return s.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if r.ID != "" {
			_, err = tx.Exec(ctx,
				`INSERT INTO agent_executions
				 (id, trace_id, agent_id, agent_name, user_id, status,
				  input_preview, output_preview, error_message, total_tokens, duration_ms)
				 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
				 ON CONFLICT (id) DO UPDATE SET status=EXCLUDED.status, output_preview=EXCLUDED.output_preview,
				 error_message=EXCLUDED.error_message,total_tokens=EXCLUDED.total_tokens,duration_ms=EXCLUDED.duration_ms`,
				r.ID, r.TraceID, r.AgentID, r.AgentName, r.UserID, r.Status,
				r.InputPreview, r.OutputPreview, r.ErrorMessage, r.TotalTokens, r.DurationMs,
			)
		} else {
			_, err = tx.Exec(ctx,
				`INSERT INTO agent_executions
				 (trace_id, agent_id, agent_name, user_id, status,
				  input_preview, output_preview, error_message, total_tokens, duration_ms)
				 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
				r.TraceID, r.AgentID, r.AgentName, r.UserID, r.Status,
				r.InputPreview, r.OutputPreview, r.ErrorMessage, r.TotalTokens, r.DurationMs,
			)
		}
		if err != nil {
			return fmt.Errorf("insert: %w", err)
		}
		return nil
	})
}

// List returns execution records for the current tenant (last 30 days), newest first.
func (s *PgExecutionStore) List(ctx context.Context, opts domain.ListOptions) ([]domain.ExecutionRecord, int64, error) {
	if opts.Page < 1 {
		opts.Page = 1
	}
	if opts.PageSize < 1 {
		opts.PageSize = 20
	}
	offset := (opts.Page - 1) * opts.PageSize

	var out []domain.ExecutionRecord
	var total int64

	err := s.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM agent_executions
			 WHERE created_at >= now() - INTERVAL '30 days'`,
		).Scan(&total); err != nil {
			return fmt.Errorf("count: %w", err)
		}

		rows, err := tx.Query(ctx,
			`SELECT id, agent_id, agent_name, user_id, status,
			        trace_id, input_preview, output_preview, error_message, total_tokens, duration_ms, created_at
			 FROM agent_executions
			 WHERE created_at >= now() - INTERVAL '30 days'
			 ORDER BY created_at DESC
			 LIMIT $1 OFFSET $2`,
			opts.PageSize, offset,
		)
		if err != nil {
			return fmt.Errorf("query: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var r domain.ExecutionRecord
			if err := rows.Scan(
				&r.ID, &r.AgentID, &r.AgentName, &r.UserID, &r.Status,
				&r.TraceID, &r.InputPreview, &r.OutputPreview, &r.ErrorMessage, &r.TotalTokens, &r.DurationMs, &r.CreatedAt,
			); err != nil {
				return fmt.Errorf("scan: %w", err)
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, total, err
}
