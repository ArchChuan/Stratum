// Package tenantdb provides tenant database isolation and execution helpers.

package tenantdb

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
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

	if _, err := conn.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS "%s"`, schemaName)); err != nil {
		return fmt.Errorf("tenantdb: create schema %s: %w", schemaName, err)
	}

	if _, err := conn.Exec(ctx, fmt.Sprintf(`SET search_path = "%s", public`, schemaName)); err != nil {
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

// ProvisionAllTenantSchemas iterates all tenants in the public.tenants table and
// calls ProvisionTenantSchema for each. Safe to call on startup — all DDL is idempotent.
func ProvisionAllTenantSchemas(ctx context.Context, pool *pgxpool.Pool, logger *zap.Logger) error {
	rows, err := pool.Query(ctx, `SELECT id FROM tenants WHERE deleted_at IS NULL`)
	if err != nil {
		return fmt.Errorf("tenantdb: list tenants: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("tenantdb: scan tenant id: %w", err)
		}
		ids = append(ids, id)
	}

	for _, id := range ids {
		if err := ProvisionTenantSchema(ctx, pool, id); err != nil {
			logger.Warn("failed to provision tenant schema", zap.String("tenant_id", id), zap.Error(err))
		} else {
			logger.Info("provisioned tenant schema", zap.String("tenant_id", id))
		}
	}
	return nil
}

// ListTenantSchemas returns schema names ("tenant_<id>") for all active tenants.
func ListTenantSchemas(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT id FROM tenants WHERE deleted_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("tenantdb: list schemas: %w", err)
	}
	defer rows.Close()

	var schemas []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("tenantdb: scan tenant id: %w", err)
		}
		schemas = append(schemas, "tenant_"+id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenantdb: rows iteration: %w", err)
	}
	return schemas, nil
}
