package infrastructure

import (
	"errors"
	"testing"

	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres/postgrestest"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
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

	_, err := manager.GetServerConfig(ctx, "some-server")

	if err == nil || errors.Is(err, mcpdomain.ErrServerNotFound) {
		t.Fatalf("database failure = %v, want propagated non-not-found error", err)
	}
}
