package persistence

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/internal/platform/domain"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

type dashboardPool interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

type DashboardRepository struct {
	pool dashboardPool
}

func NewDashboardRepository(pool *pgxpool.Pool) *DashboardRepository {
	return &DashboardRepository{pool: pool}
}

func (r *DashboardRepository) Overview(
	ctx context.Context,
	tenantID string,
) (domain.DashboardOverview, error) {
	tenant, ok := pgstore.FromContext(ctx)
	if !ok || tenant.TenantID == "" {
		return domain.DashboardOverview{}, fmt.Errorf("dashboard repository: missing tenant context")
	}
	if tenant.TenantID != tenantID {
		return domain.DashboardOverview{}, fmt.Errorf("dashboard repository: tenant context mismatch")
	}

	var result domain.DashboardOverview
	err := pgstore.ExecTenantWith(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT
			(SELECT COUNT(*) FROM agents),
			(SELECT COUNT(*) FROM skills),
			(SELECT COUNT(*) FROM rag_workspaces),
			(SELECT COUNT(*) FROM mcp_configs),
			(SELECT COUNT(*) FROM providers),
			(SELECT COUNT(*) FROM public.tenant_members WHERE tenant_id = $1),
			(SELECT COUNT(*) FROM workflow_definitions),
			(SELECT COUNT(*) FROM chat_messages
				WHERE role = 'user' AND created_at >= NOW() - INTERVAL '168 hours')`, tenantID).Scan(
			&result.Agents,
			&result.Skills,
			&result.KnowledgeWorkspaces,
			&result.MCPServers,
			&result.ModelProviders,
			&result.TenantMembers,
			&result.Workflows,
			&result.AgentUserMessages7d,
		)
	})
	if err != nil {
		return domain.DashboardOverview{}, fmt.Errorf("dashboard repository: overview: %w", err)
	}
	return result, nil
}
