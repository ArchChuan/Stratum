// Package postgrestest provides test helpers for PostgreSQL-backed repositories
// that require a live database. All functions skip the test when
// STRATUM_TEST_POSTGRES_URL is not set.
package postgrestest

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

// NewPool creates a pgxpool.Pool from STRATUM_TEST_POSTGRES_URL.
// Skips the test if the env var is not set.
func NewPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("STRATUM_TEST_POSTGRES_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	// Ensure public schema (tenants table, etc.) is provisioned.
	if err := postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()); err != nil {
		t.Fatalf("ProvisionPublicSchema: %v", err)
	}

	return pool
}

// CreateTestTenant inserts a new tenant record and provisions its tenant schema.
// Returns the tenant ID (UUID). The schema is cleaned up on test completion.
func CreateTestTenant(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	suffix := fmt.Sprintf("tmp_provider_model_%d", time.Now().UnixNano())

	var tenantID string
	err := pool.QueryRow(ctx,
		`INSERT INTO public.tenants (name, slug, plan, status, settings, created_at, updated_at)
		 VALUES ($1, $2, 'free', 'active', '{}'::jsonb, now(), now())
		 RETURNING id`,
		suffix, suffix,
	).Scan(&tenantID)
	if err != nil {
		t.Fatalf("insert tenant: %v", err)
	}

	schemaName := "tenant_" + tenantID
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS "%s" CASCADE`, schemaName))
	})

	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatalf("ProvisionTenantSchema: %v", err)
	}

	return tenantID
}
