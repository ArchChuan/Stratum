// Package tenantdb provides tenant database isolation and execution helpers.

package tenantdb

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ExecTenant runs fn inside a transaction whose search_path is set to
// "tenant_{id}, public". Returns an error if ctx has no TenantContext.
func ExecTenant(ctx context.Context, pool *pgxpool.Pool, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tc, ok := FromContext(ctx)
	if !ok {
		return fmt.Errorf("tenantdb: missing tenant context")
	}
	if tc.TenantID == "" {
		return fmt.Errorf("tenantdb: tenant_id is empty (global_admin cannot use ExecTenant)")
	}
	for _, r := range tc.TenantID {
		if !isSafeTenantIDChar(r) {
			return fmt.Errorf("tenantdb: invalid tenant_id %q", tc.TenantID)
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("tenantdb: begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()

	schema := "tenant_" + tc.TenantID
	if _, err := tx.Exec(ctx, "SET LOCAL search_path = "+schema+", public"); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("tenantdb: set search_path: %w", err)
	}

	if err := fn(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}

	return tx.Commit(ctx)
}

// isSafeTenantIDChar returns true for characters safe in a PostgreSQL identifier.
func isSafeTenantIDChar(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= '0' && r <= '9') ||
		r == '_' || r == '-'
}
