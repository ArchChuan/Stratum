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
		stmt = stripSQLLineComments(strings.TrimSpace(stmt))
		if stmt == "" {
			continue
		}
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("tenantdb: exec statement %d: %w", i, err)
		}
	}

	return nil
}

// stripSQLLineComments removes all `--` comment lines from a SQL statement block.
// Required because splitStatements groups a comment + following statement into one chunk,
// and the loop would skip the entire chunk if it starts with `--`.
func stripSQLLineComments(stmt string) string {
	lines := strings.Split(stmt, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			kept = append(kept, line)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
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

const defaultTenantName = "默认租户"
const defaultTenantSlug = "default"

// EnsureDefaultTenant creates the global default tenant if one does not already exist.
// Idempotent — checks the partial unique index (is_default = true) via SELECT first.
func EnsureDefaultTenant(ctx context.Context, pool *pgxpool.Pool, logger *zap.Logger) error {
	var existing string
	err := pool.QueryRow(ctx, `SELECT id FROM tenants WHERE is_default = true LIMIT 1`).Scan(&existing)
	if err == nil {
		logger.Info("default tenant already exists", zap.String("tenant_id", existing))
		return nil
	}

	var id string
	err = pool.QueryRow(ctx, `
		INSERT INTO tenants (name, slug, plan, status, settings, is_default, created_at, updated_at)
		VALUES ($1, $2, 'free', 'active', '{}'::jsonb, true, now(), now())
		RETURNING id`,
		defaultTenantName, defaultTenantSlug,
	).Scan(&id)
	if err != nil {
		return fmt.Errorf("tenantdb: ensure default tenant: %w", err)
	}
	logger.Info("created default tenant", zap.String("tenant_id", id))
	return nil
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
