package persistence_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/persistence"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func setupFactRepoTest(t *testing.T) (*pgxpool.Pool, *persistence.FactRepo) {
	t.Helper()
	pool := NewTestTenantPool(t, "tenant_test_facts")
	repo := persistence.NewFactRepo(pool)
	return pool, repo
}

func TestFactRepo_Insert(t *testing.T) {
	_, repo := setupFactRepoTest(t)
	ctx := context.Background()

	fact, err := domain.NewFact("user123", "", "user", "User prefers dark mode", 0.8, []string{"UI", "preference"})
	require.NoError(t, err)
	require.NotEmpty(t, fact.ID)

	err = repo.Create(ctx, fact)
	require.NoError(t, err)

	// Verify insert by retrieving
	retrieved, err := repo.GetByID(ctx, fact.ID)
	require.NoError(t, err)
	require.Equal(t, fact.Content, retrieved.Content)
	require.Equal(t, fact.Importance, retrieved.Importance)
	require.Equal(t, fact.UserID, retrieved.UserID)
}
