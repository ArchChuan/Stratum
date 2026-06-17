package application

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ExecutionRecord is an agent execution history entry.
type ExecutionRecord struct {
	ID            string
	AgentID       string
	AgentName     string
	UserID        string
	Status        string
	InputPreview  string
	OutputPreview string
	ErrorMessage  string
	TotalTokens   int
	DurationMs    int
	CreatedAt     time.Time
}

// ExecutionStore persists and retrieves agent execution history from the tenant schema.
type ExecutionStore struct {
	pool *pgxpool.Pool
}

// NewExecutionStore creates an ExecutionStore backed by pool.
func NewExecutionStore(pool *pgxpool.Pool) *ExecutionStore {
	return &ExecutionStore{pool: pool}
}

func (s *ExecutionStore) execTenant(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	tc, ok := tenantdb.FromContext(ctx)
	if !ok || tc.TenantID == "" {
		return fmt.Errorf("execution_store: missing tenant context")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("execution_store: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	schema := "tenant_" + tc.TenantID
	if _, err := tx.Exec(ctx, fmt.Sprintf(`SET LOCAL search_path = "%s", public`, schema)); err != nil {
		return fmt.Errorf("execution_store: set search_path: %w", err)
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Insert writes an execution record into the tenant schema. Safe to call in a goroutine.
func (s *ExecutionStore) Insert(ctx context.Context, r ExecutionRecord) error {
	return s.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO agent_executions
			 (agent_id, agent_name, user_id, status,
			  input_preview, output_preview, error_message, total_tokens, duration_ms)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			r.AgentID, r.AgentName, r.UserID, r.Status,
			r.InputPreview, r.OutputPreview, r.ErrorMessage, r.TotalTokens, r.DurationMs,
		)
		if err != nil {
			return fmt.Errorf("insert: %w", err)
		}
		return nil
	})
}

// ListOptions controls pagination for List.
type ListOptions struct {
	Page     int
	PageSize int
}

// List returns execution records for the current tenant (last 30 days), newest first.
func (s *ExecutionStore) List(ctx context.Context, opts ListOptions) ([]ExecutionRecord, int64, error) {
	if opts.Page < 1 {
		opts.Page = 1
	}
	if opts.PageSize < 1 {
		opts.PageSize = 20
	}
	offset := (opts.Page - 1) * opts.PageSize

	var out []ExecutionRecord
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
			        input_preview, output_preview, error_message, total_tokens, duration_ms, created_at
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
			var r ExecutionRecord
			if err := rows.Scan(
				&r.ID, &r.AgentID, &r.AgentName, &r.UserID, &r.Status,
				&r.InputPreview, &r.OutputPreview, &r.ErrorMessage, &r.TotalTokens, &r.DurationMs, &r.CreatedAt,
			); err != nil {
				return fmt.Errorf("scan: %w", err)
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, total, err
}
