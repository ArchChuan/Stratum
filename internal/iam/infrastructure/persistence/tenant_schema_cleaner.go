package persistence

import (
	"context"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TenantSchemaCleaner drops a tenant's PostgreSQL schema and all its data.
type TenantSchemaCleaner struct {
	pool *pgxpool.Pool
}

// NewTenantSchemaCleaner wires the pool.
func NewTenantSchemaCleaner(pool *pgxpool.Pool) *TenantSchemaCleaner {
	return &TenantSchemaCleaner{pool: pool}
}

var tenantIDRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// DropTenantSchema drops the tenant schema cascade. Safe to call multiple times (IF EXISTS).
func (c *TenantSchemaCleaner) DropTenantSchema(ctx context.Context, tenantID string) error {
	if c.pool == nil {
		return nil
	}
	if !tenantIDRE.MatchString(tenantID) {
		return fmt.Errorf("tenant_cleaner: invalid tenantID format")
	}
	schemaName := fmt.Sprintf("tenant_%s", tenantID)
	_, err := c.pool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "%s" CASCADE`, schemaName))
	if err != nil {
		return fmt.Errorf("tenant_cleaner: drop schema %s: %w", schemaName, err)
	}
	return nil
}
