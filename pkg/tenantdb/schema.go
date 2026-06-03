package tenantdb

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed tenant_schema.sql
var tenantSchemaDDL string

// ProvisionTenantSchema creates the schema for tenantID (if not exists) and
// executes all per-tenant DDL within that schema. Safe to call multiple times.
func ProvisionTenantSchema(ctx context.Context, pool *pgxpool.Pool, tenantID string) error {
	if tenantID == "" {
		return fmt.Errorf("tenantdb: tenantID must not be empty")
	}
	for _, r := range tenantID {
		if !isSafeTenantIDChar(r) {
			return fmt.Errorf("tenantdb: invalid tenantID %q", tenantID)
		}
	}

	schemaName := "tenant_" + tenantID

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("tenantdb: acquire conn: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+schemaName); err != nil {
		return fmt.Errorf("tenantdb: create schema %s: %w", schemaName, err)
	}

	if _, err := conn.Exec(ctx, "SET search_path = "+schemaName+", public"); err != nil {
		return fmt.Errorf("tenantdb: set search_path: %w", err)
	}

	for i, stmt := range splitStatements(tenantSchemaDDL) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" || strings.HasPrefix(stmt, "--") {
			continue
		}
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("tenantdb: exec statement %d: %w", i, err)
		}
	}

	return nil
}

func splitStatements(sql string) []string {
	parts := strings.Split(sql, ";")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			result = append(result, s)
		}
	}
	return result
}
