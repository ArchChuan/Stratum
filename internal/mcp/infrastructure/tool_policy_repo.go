package infrastructure

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PgToolPolicyRepo struct{ db *pgxpool.Pool }

func NewPgToolPolicyRepo(db *pgxpool.Pool) *PgToolPolicyRepo { return &PgToolPolicyRepo{db: db} }

func (r *PgToolPolicyRepo) Get(ctx context.Context, serverID, toolName string) (domain.ToolPolicy, bool, error) {
	var out domain.ToolPolicy
	err := postgres.ExecTenant(ctx, r.db, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT server_id,tool_name,risk_level,updated_by FROM mcp_tool_policies WHERE server_id=$1 AND tool_name=$2`, serverID, toolName).
			Scan(&out.ServerID, &out.ToolName, &out.RiskLevel, &out.UpdatedBy)
	})
	if err == pgx.ErrNoRows {
		return domain.ToolPolicy{}, false, nil
	}
	if err != nil {
		return domain.ToolPolicy{}, false, fmt.Errorf("mcp tool policy get: %w", err)
	}
	return out, true, nil
}

func (r *PgToolPolicyRepo) List(ctx context.Context) ([]domain.ToolPolicy, error) {
	var out []domain.ToolPolicy
	err := postgres.ExecTenant(ctx, r.db, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT server_id,tool_name,risk_level,updated_by FROM mcp_tool_policies ORDER BY server_id,tool_name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p domain.ToolPolicy
			if err := rows.Scan(&p.ServerID, &p.ToolName, &p.RiskLevel, &p.UpdatedBy); err != nil {
				return err
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	return out, err
}

func (r *PgToolPolicyRepo) Upsert(ctx context.Context, policy domain.ToolPolicy) error {
	return postgres.ExecTenant(ctx, r.db, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO mcp_tool_policies(server_id,tool_name,risk_level,updated_by) VALUES($1,$2,$3,$4)
		 ON CONFLICT(server_id,tool_name) DO UPDATE SET risk_level=EXCLUDED.risk_level,updated_by=EXCLUDED.updated_by,updated_at=NOW()`, policy.ServerID, policy.ToolName, policy.RiskLevel, policy.UpdatedBy)
		return err
	})
}
