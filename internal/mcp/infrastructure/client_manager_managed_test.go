package infrastructure

import (
	"context"
	"errors"
	"testing"

	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/platformmcp"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres/postgrestest"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

func TestGetServerConfigDatabaseFailureIsNotNotFound(t *testing.T) {
	pool := postgrestest.NewPool(t)
	manager := NewClientManager(zap.NewNop(), nil, pool, "")
	pool.Close()
	ctx := tenantdb.WithTenant(t.Context(), &tenantdb.TenantContext{
		TenantID: "database_failure",
		Role:     tenantdb.RoleTenantAdmin,
	})

	_, err := manager.GetServerConfig(ctx, platformmcp.SystemServerID)

	if err == nil || errors.Is(err, mcpdomain.ErrServerNotFound) {
		t.Fatalf("database failure = %v, want propagated non-not-found error", err)
	}
}

func TestGetServerConfigLoadsPlatformManagedIdentity(t *testing.T) {
	pool := postgrestest.NewPool(t)
	tenantID := "mcp_" + uuid.NewString()[:8]
	ctx := context.Background()
	requireProvisionedTenant(t, ctx, pool, tenantID)

	tenantCtx := tenantdb.WithTenant(ctx, &tenantdb.TenantContext{
		TenantID: tenantID,
		Role:     tenantdb.RoleTenantAdmin,
	})
	manager := NewClientManager(zap.NewNop(), nil, pool, "")

	cfg, err := manager.GetServerConfig(tenantCtx, platformmcp.SystemServerID)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SystemKey != platformmcp.SystemServerKey || cfg.ManagementMode != platformmcp.ManagementPlatform {
		t.Fatalf("managed identity = (%q, %q)", cfg.SystemKey, cfg.ManagementMode)
	}
}

func requireProvisionedTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID string) {
	t.Helper()
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{"tenant_" + tenantID}.Sanitize()
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})
}
