package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PgOperationUsageRepo struct {
	pool poolIface
}

func NewPgOperationUsageRepo(pool *pgxpool.Pool) *PgOperationUsageRepo {
	return &PgOperationUsageRepo{pool: pool}
}

func (r *PgOperationUsageRepo) execTenant(
	ctx context.Context,
	fn func(context.Context, pgx.Tx) error,
) error {
	tenant, ok := tenantdb.FromContext(ctx)
	if !ok || tenant.TenantID == "" {
		return fmt.Errorf("operation_usage_repo: missing tenant context")
	}
	return pgstore.ExecTenantWith(ctx, r.pool, tenant.TenantID, fn)
}

// AddUsage upserts the daily counters, adding to any existing row. The upsert
// is idempotent under retry and atomic, so usage never loses a concurrent run.
func (r *PgOperationUsageRepo) AddUsage(
	ctx context.Context,
	tenantID, agentID string,
	opType port.OperationType,
	at time.Time,
	costUSD float64,
	executions int,
) error {
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO operation_usage (agent_id, op_type, usage_date, cost_usd, executions)
            VALUES ($1, $2, $3, $4, $5)
            ON CONFLICT (agent_id, op_type, usage_date)
            DO UPDATE SET cost_usd = operation_usage.cost_usd + EXCLUDED.cost_usd,
                          executions = operation_usage.executions + EXCLUDED.executions`,
			agentID, opType, at, costUSD, executions)
		if err != nil {
			return fmt.Errorf("add operation usage: %w", err)
		}
		return nil
	})
}

// DailyUsage returns the day's aggregated counters. A missing row is a zero
// budget (no usage yet), not an error.
func (r *PgOperationUsageRepo) DailyUsage(
	ctx context.Context,
	tenantID, agentID string,
	opType port.OperationType,
	at time.Time,
) (port.DailyOperationUsage, error) {
	var usage port.DailyOperationUsage
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT cost_usd, executions
            FROM operation_usage
            WHERE agent_id = $1 AND op_type = $2 AND usage_date = $3`,
			agentID, opType, at).Scan(&usage.CostUSD, &usage.Executions)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return usage, nil
		}
		return usage, fmt.Errorf("query daily operation usage: %w", err)
	}
	return usage, nil
}
