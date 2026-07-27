package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var (
	ErrDefaultTenantMissing       = errors.New("default tenant missing")
	ErrDefaultTenantSchemaMissing = errors.New("default tenant schema missing")
)

type readinessQueryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const defaultTenantReadinessQuery = `SELECT EXISTS (SELECT 1 FROM information_schema.tables AS tables WHERE tables.table_schema = 'tenant_' || tenants.id::text AND tables.table_name = 'agents') AS agents_table_exists FROM public.tenants AS tenants WHERE tenants.is_default = true AND tenants.status = 'active' AND tenants.deleted_at IS NULL LIMIT 1`

// CheckDefaultTenantReadiness verifies the minimum tenant invariant required to serve traffic.
func CheckDefaultTenantReadiness(ctx context.Context, queryer readinessQueryer) error {
	var agentsTableExists bool
	if err := queryer.QueryRow(ctx, defaultTenantReadinessQuery).Scan(&agentsTableExists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("postgres readiness: %w", ErrDefaultTenantMissing)
		}
		return fmt.Errorf("postgres readiness query: %w", err)
	}
	if !agentsTableExists {
		return fmt.Errorf("postgres readiness: %w", ErrDefaultTenantSchemaMissing)
	}
	return nil
}
