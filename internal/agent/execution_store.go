package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ExecutionRecord is a single agent execution history entry.
type ExecutionRecord struct {
	ID            string
	TenantID      string
	AgentID       string
	UserID        string
	AgentName     string
	Status        string
	InputPreview  string
	OutputPreview string
	ErrorMessage  string
	TotalTokens   int
	DurationMs    int
	CreatedAt     time.Time
}

// ExecutionStore persists and retrieves agent execution history.
type ExecutionStore struct {
	db *pgxpool.Pool
}

// NewExecutionStore creates an ExecutionStore.
func NewExecutionStore(db *pgxpool.Pool) *ExecutionStore {
	return &ExecutionStore{db: db}
}

// Insert writes one execution record. Never blocks the caller on error.
func (s *ExecutionStore) Insert(ctx context.Context, r ExecutionRecord) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO public.agent_executions
		 (tenant_id, agent_id, user_id, agent_name, status,
		  input_preview, output_preview, error_message, total_tokens, duration_ms)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		r.TenantID, r.AgentID, r.UserID, r.AgentName, r.Status,
		r.InputPreview, r.OutputPreview, r.ErrorMessage, r.TotalTokens, r.DurationMs,
	)
	if err != nil {
		return fmt.Errorf("execution_store: insert: %w", err)
	}
	return nil
}

// List returns executions for a tenant in the last 30 days, newest first.
func (s *ExecutionStore) List(ctx context.Context, tenantID string) ([]ExecutionRecord, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, tenant_id, agent_id, user_id, agent_name, status,
		        input_preview, output_preview, error_message, total_tokens, duration_ms, created_at
		 FROM public.agent_executions
		 WHERE tenant_id = $1 AND created_at >= now() - INTERVAL '30 days'
		 ORDER BY created_at DESC
		 LIMIT 500`,
		tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("execution_store: list: %w", err)
	}
	defer rows.Close()

	var out []ExecutionRecord
	for rows.Next() {
		var r ExecutionRecord
		if err := rows.Scan(
			&r.ID, &r.TenantID, &r.AgentID, &r.UserID, &r.AgentName, &r.Status,
			&r.InputPreview, &r.OutputPreview, &r.ErrorMessage, &r.TotalTokens, &r.DurationMs, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("execution_store: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, nil
}
