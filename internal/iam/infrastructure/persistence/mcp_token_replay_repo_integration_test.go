package persistence_test

import (
	"context"
	"testing"
	"time"

	iampersistence "github.com/byteBuilderX/stratum/internal/iam/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres/postgrestest"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestMCPTokenReplayConsumeIsAtomic(t *testing.T) {
	pool := postgrestest.NewPool(t)
	tenantID := "replay_" + uuid.NewString()[:8]
	ctx := context.Background()
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{"tenant_" + tenantID}.Sanitize()
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`); err != nil {
			t.Errorf("drop schema: %v", err)
		}
	})
	repo := iampersistence.NewMCPTokenReplayRepo(pool)
	expiresAt := time.Now().Add(time.Minute)

	first, err := repo.ConsumeInvocationJTI(ctx, tenantID, "jti-1", expiresAt)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.ConsumeInvocationJTI(ctx, tenantID, "jti-1", expiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if !first || second {
		t.Fatalf("consume results = (%v, %v), want (true, false)", first, second)
	}
}
