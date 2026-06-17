//go:build integration

package application_test

import (
	"context"
	"os"
	"testing"

	application "github.com/byteBuilderX/stratum/internal/iam/application"
	iampersistence "github.com/byteBuilderX/stratum/internal/iam/infrastructure/persistence"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupOnboardTest(t *testing.T) (*application.OnboardService, *pgxpool.Pool, func()) {
	t.Helper()
	pgURL := os.Getenv("TEST_POSTGRES_URL")
	if pgURL == "" {
		pgURL = "postgres://stratum:stratum@localhost:5432/stratum?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), pgURL)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	svc := application.NewOnboardService(iampersistence.NewOnboardRepo(pool))
	return svc, pool, func() { pool.Close() }
}

func TestCreateTenant_Success(t *testing.T) {
	svc, pool, cleanup := setupOnboardTest(t)
	defer cleanup()
	ctx := context.Background()

	userID := "00000000-0000-0000-0000-000000000010"
	pool.Exec(ctx,
		`INSERT INTO users (id, github_id, github_login) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING`,
		userID, userID, "testuser10",
	)
	// clean up any leftover tenant from prior runs
	pool.Exec(ctx, `DELETE FROM tenant_members WHERE tenant_id IN (SELECT id FROM tenants WHERE slug = 'testcorp')`)
	pool.Exec(ctx, `DELETE FROM tenants WHERE slug = 'testcorp'`)

	result, err := svc.CreateTenant(ctx, application.CreateTenantInput{
		UserID:    userID,
		Name:      "Test Corp",
		GitHubOrg: "testcorp",
	})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if result.TenantID == "" {
		t.Error("expected non-empty TenantID")
	}
	if result.SchemaName != "tenant_"+result.TenantID {
		t.Errorf("schema name mismatch: %s", result.SchemaName)
	}
}

func TestJoinTenant_InvalidToken(t *testing.T) {
	svc, _, cleanup := setupOnboardTest(t)
	defer cleanup()
	ctx := context.Background()

	err := svc.JoinTenant(ctx, application.JoinTenantInput{
		UserID:          "00000000-0000-0000-0000-000000000011",
		InvitationToken: "nonexistent-token",
	})
	if err == nil {
		t.Fatal("expected error for invalid invitation token")
	}
}
