//go:build integration

package persistence_test

import (
	"context"
	"os"
	"testing"
	"time"

	persistence "github.com/byteBuilderX/stratum/internal/iam/infrastructure/persistence"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func setupTokenStoreTest(t *testing.T) (*persistence.TokenStore, func()) {
	t.Helper()
	ctx := context.Background()

	pgURL := os.Getenv("TEST_POSTGRES_URL")
	if pgURL == "" {
		pgURL = "postgres://stratum:stratum@localhost:5432/stratum?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}

	// Insert test users required by FK constraints
	testUsers := []string{
		"00000000-0000-0000-0000-000000000001",
		"00000000-0000-0000-0000-000000000003",
	}
	for _, uid := range testUsers {
		pool.Exec(ctx,
			`INSERT INTO users (id, github_id, github_login) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING`,
			uid, uid, "test-user-"+uid[len(uid)-4:],
		)
	}

	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379/0"
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("redis parse url: %v", err)
	}
	rdb := redis.NewClient(opt)

	store := persistence.NewTokenStore(pool, rdb)
	return store, func() {
		pool.Close()
		rdb.Close()
	}
}

func TestTokenStore_CreateAndRotate(t *testing.T) {
	store, cleanup := setupTokenStoreTest(t)
	defer cleanup()
	ctx := context.Background()

	userID := "00000000-0000-0000-0000-000000000001"
	rawToken := "raw-refresh-token-abc"

	err := store.Create(ctx, userID, "", rawToken, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newRaw := "new-refresh-token-xyz"
	err = store.Rotate(ctx, rawToken, newRaw, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	blacklisted, err := store.IsBlacklisted(ctx, rawToken)
	if err != nil {
		t.Fatalf("IsBlacklisted: %v", err)
	}
	if !blacklisted {
		t.Error("old token should be blacklisted after rotation")
	}
}

func TestTokenStore_Revoke(t *testing.T) {
	store, cleanup := setupTokenStoreTest(t)
	defer cleanup()
	ctx := context.Background()

	rawToken := "revoke-test-token-123"
	err := store.Create(ctx, "00000000-0000-0000-0000-000000000003", "", rawToken, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	err = store.Revoke(ctx, rawToken)
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	blacklisted, err := store.IsBlacklisted(ctx, rawToken)
	if err != nil {
		t.Fatalf("IsBlacklisted: %v", err)
	}
	if !blacklisted {
		t.Error("revoked token should be blacklisted")
	}
}
