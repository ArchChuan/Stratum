package persistence_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// NewTestTenantPool creates a test database pool with a provisioned tenant schema
func NewTestTenantPool(t *testing.T, tenantID string) *pgxpool.Pool {
	t.Helper()

	// Use test database URL or fallback to default
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/stratum_test?sslmode=disable"
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("skipping test: cannot connect to test database: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("skipping test: cannot reach test database: %v", err)
	}
	if err := postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()); err != nil {
		pool.Close()
		t.Fatalf("failed to provision public schema: %v", err)
	}

	// Provision tenant schema
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		pool.Close()
		t.Fatalf("failed to provision tenant schema: %v", err)
	}

	// Clean up on test completion
	t.Cleanup(func() {
		// Drop tenant schema
		schemaName := fmt.Sprintf("tenant_%s", tenantID)
		_, _ = pool.Exec(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaName))
		pool.Close()
	})

	return pool
}
