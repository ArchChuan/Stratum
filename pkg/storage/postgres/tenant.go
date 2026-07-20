package postgres

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

const (
	schemaProvisionLockKey       int64 = 0x5374726174756d
	schemaProvisionLockSQL             = `SELECT pg_advisory_lock($1)`
	schemaProvisionUnlockSQL           = `SELECT pg_advisory_unlock($1)`
	schemaProvisionLockTimeout         = 2 * time.Minute
	schemaProvisionUnlockTimeout       = 5 * time.Second
)

type schemaProvisionLockConn interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

type tenantTxBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

// WithSchemaProvisionLock serializes the complete startup schema bootstrap
// across application instances using one PostgreSQL session advisory lock.
func WithSchemaProvisionLock(
	ctx context.Context,
	pool *pgxpool.Pool,
	fn func(context.Context) error,
) error {
	if pool == nil {
		return fmt.Errorf("postgres: schema provision lock: nil pool")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres: acquire schema provision lock connection: %w", err)
	}
	defer conn.Release()

	lockCtx, cancel := context.WithTimeout(ctx, schemaProvisionLockTimeout)
	defer cancel()
	return runWithSchemaProvisionLock(lockCtx, ctx, conn, fn)
}

func runWithSchemaProvisionLock(
	lockCtx context.Context,
	callbackCtx context.Context,
	conn schemaProvisionLockConn,
	fn func(context.Context) error,
) (result error) {
	if _, err := conn.Exec(lockCtx, schemaProvisionLockSQL, schemaProvisionLockKey); err != nil {
		return fmt.Errorf("postgres: acquire schema provision lock: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), schemaProvisionUnlockTimeout)
		defer cancel()
		if _, err := conn.Exec(unlockCtx, schemaProvisionUnlockSQL, schemaProvisionLockKey); err != nil {
			result = errors.Join(result, fmt.Errorf("postgres: release schema provision lock: %w", err))
		}
	}()

	return fn(callbackCtx)
}

// ----------------------------------------------------------------------------
// Tenant context
// ----------------------------------------------------------------------------

// Role represents the access role encoded in a JWT claim.
type Role string

const (
	RoleTenantAdmin Role = "tenant_admin"
	RoleTenantUser  Role = "tenant_user"
	RoleGlobalAdmin Role = "global_admin"
)

// TenantContext carries tenant identity through the request lifecycle.
// TenantID is empty for global_admin requests.
type TenantContext struct {
	TenantID string
	UserID   string
	Role     Role
}

type ctxKey struct{}

// WithTenant returns a new context with tc embedded.
func WithTenant(ctx context.Context, tc *TenantContext) context.Context {
	return context.WithValue(ctx, ctxKey{}, tc)
}

// FromContext extracts the TenantContext from ctx.
// Returns (nil, false) if not present.
func FromContext(ctx context.Context) (*TenantContext, bool) {
	tc, ok := ctx.Value(ctxKey{}).(*TenantContext)
	return tc, ok && tc != nil
}

// ----------------------------------------------------------------------------
// Tenant-scoped execution
// ----------------------------------------------------------------------------

// ExecTenant runs fn inside a transaction whose search_path is set to
// "tenant_{id}, public". Returns an error if ctx has no TenantContext.
func ExecTenant(ctx context.Context, pool *pgxpool.Pool, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tc, ok := FromContext(ctx)
	if !ok {
		return fmt.Errorf("postgres: missing tenant context")
	}
	if tc.TenantID == "" {
		return fmt.Errorf("postgres: tenant_id is empty (global_admin cannot use ExecTenant)")
	}
	return execTenantOnPool(ctx, pool, tc.TenantID, fn)
}

// ExecTenant is the method form on *Pool. tenantID is passed explicitly
// (instead of being read from ctx) so callers can isolate work without
// rebinding the context.
func (p *Pool) ExecTenant(ctx context.Context, tenantID string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if tenantID == "" {
		return fmt.Errorf("postgres: tenant_id is empty")
	}
	return execTenantOnPool(ctx, p.Pool, tenantID, fn)
}

// ExecTenantWith applies the shared tenant validation and transaction policy to
// any pgx-compatible transaction beginner, including test doubles.
func ExecTenantWith(ctx context.Context, pool tenantTxBeginner, tenantID string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if tenantID == "" {
		return fmt.Errorf("postgres: tenant_id is empty")
	}
	return execTenantOnPool(ctx, pool, tenantID, fn)
}

func execTenantOnPool(ctx context.Context, pool tenantTxBeginner, tenantID string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	for _, r := range tenantID {
		if !isSafeTenantIDChar(r) {
			return fmt.Errorf("postgres: invalid tenant_id %q", tenantID)
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() {
		if pnc := recover(); pnc != nil {
			_ = tx.Rollback(ctx)
			panic(pnc)
		}
	}()

	schema := "tenant_" + tenantID
	if _, err := tx.Exec(ctx, fmt.Sprintf(`SET LOCAL search_path = "%s", public`, schema)); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("postgres: set search_path: %w", err)
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

// ----------------------------------------------------------------------------
// Tenant schema provisioning
// ----------------------------------------------------------------------------

//go:embed tenant_schema.sql
var tenantSchemaDDL string

//go:embed public_schema.sql
var publicSchemaDDL string

// ProvisionTenantSchema creates the schema for tenantID (if not exists) and
// executes all per-tenant DDL within that schema. Safe to call multiple times.
func ProvisionTenantSchema(ctx context.Context, pool *pgxpool.Pool, tenantID string) error {
	if tenantID == "" {
		return fmt.Errorf("postgres: tenantID must not be empty")
	}
	for _, r := range tenantID {
		if !isSafeTenantIDChar(r) {
			return fmt.Errorf("postgres: invalid tenantID %q", tenantID)
		}
	}

	schemaName := "tenant_" + tenantID

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres: acquire conn: %w", err)
	}
	defer func() {
		// Reset search_path before returning to PgBouncer pool to avoid poisoning
		// other callers that acquire this server connection in transaction pooling mode.
		conn.Exec(ctx, `RESET search_path`) //nolint:errcheck
		conn.Release()
	}()

	if _, err := conn.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS "%s"`, schemaName)); err != nil {
		return fmt.Errorf("postgres: create schema %s: %w", schemaName, err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tenant schema tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, fmt.Sprintf(`SET LOCAL search_path = "%s", public`, schemaName)); err != nil {
		return fmt.Errorf("postgres: set search_path: %w", err)
	}

	for i, stmt := range splitStatements(tenantSchemaDDL) {
		stmt = stripSQLLineComments(strings.TrimSpace(stmt))
		if stmt == "" {
			continue
		}
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("postgres: exec statement %d: %w", i, err)
		}
	}

	return tx.Commit(ctx)
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

// splitStatements splits on `;` while respecting `$$` dollar-quote blocks
// so that PL/pgSQL function bodies with internal semicolons are not torn apart.
func splitStatements(sql string) []string {
	sql = stripSQLLineComments(sql)
	var stmts []string
	inDollarQuote := false
	start := 0
	for i := 0; i < len(sql); i++ {
		if i+1 < len(sql) && sql[i] == '$' && sql[i+1] == '$' {
			inDollarQuote = !inDollarQuote
			i++
			continue
		}
		if sql[i] == ';' && !inDollarQuote {
			if s := strings.TrimSpace(sql[start:i]); s != "" {
				stmts = append(stmts, s)
			}
			start = i + 1
		}
	}
	if s := strings.TrimSpace(sql[start:]); s != "" {
		stmts = append(stmts, s)
	}
	return stmts
}

const defaultTenantName = "默认租户"
const defaultTenantSlug = "default"

// EnsureDefaultTenant creates the global default tenant if one does not already exist.
// Idempotent — checks the partial unique index (is_default = true) via SELECT first.
func EnsureDefaultTenant(ctx context.Context, pool *pgxpool.Pool, logger *zap.Logger) error {
	var existing string
	err := pool.QueryRow(ctx, `SELECT id FROM public.tenants WHERE is_default = true LIMIT 1`).Scan(&existing)
	if err == nil {
		logger.Info("default tenant already exists", zap.String("tenant_id", existing))
		return nil
	}

	var id string
	err = pool.QueryRow(ctx, `
		INSERT INTO public.tenants (name, slug, plan, status, settings, is_default, created_at, updated_at)
		VALUES ($1, $2, 'free', 'active', '{}'::jsonb, true, now(), now())
		RETURNING id`,
		defaultTenantName, defaultTenantSlug,
	).Scan(&id)
	if err != nil {
		return fmt.Errorf("postgres: ensure default tenant: %w", err)
	}
	logger.Info("created default tenant", zap.String("tenant_id", id))
	return nil
}

// ProvisionPublicSchema applies idempotent DDL for the public schema on every startup.
// Equivalent to ProvisionTenantSchema but for the shared public schema.
func ProvisionPublicSchema(ctx context.Context, pool *pgxpool.Pool, logger *zap.Logger) error {
	for i, stmt := range splitStatements(publicSchemaDDL) {
		stmt = stripSQLLineComments(strings.TrimSpace(stmt))
		if stmt == "" {
			continue
		}
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("postgres: public schema stmt %d: %w", i, err)
		}
	}
	logger.Info("public schema provisioned")
	return nil
}

// ProvisionAllTenantSchemas iterates all tenants in the public.tenants table and
// calls ProvisionTenantSchema for each. Safe to call on startup — all DDL is idempotent.
func ProvisionAllTenantSchemas(ctx context.Context, pool *pgxpool.Pool, logger *zap.Logger) error {
	rows, err := pool.Query(ctx, `SELECT id FROM tenants WHERE deleted_at IS NULL`)
	if err != nil {
		return fmt.Errorf("postgres: list tenants: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("postgres: scan tenant id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: tenant rows iteration: %w", err)
	}

	return provisionTenantSchemaIDs(ctx, ids, func(ctx context.Context, id string) error {
		if err := ProvisionTenantSchema(ctx, pool, id); err != nil {
			logger.Warn("failed to provision tenant schema", zap.String("tenant_id", id), zap.Error(err))
			return err
		} else {
			logger.Info("provisioned tenant schema", zap.String("tenant_id", id))
		}
		return nil
	})
}

func provisionTenantSchemaIDs(ctx context.Context, ids []string, provision func(context.Context, string) error) error {
	var errs []error
	for _, id := range ids {
		if err := provision(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// ListTenantSchemas returns schema names ("tenant_<id>") for all active tenants.
func ListTenantSchemas(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT id FROM tenants WHERE deleted_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("postgres: list schemas: %w", err)
	}
	defer rows.Close()

	var schemas []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres: scan tenant id: %w", err)
		}
		schemas = append(schemas, "tenant_"+id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: rows iteration: %w", err)
	}
	return schemas, nil
}
