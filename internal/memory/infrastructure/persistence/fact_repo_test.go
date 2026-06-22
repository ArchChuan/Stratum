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

func TestFactRepo_Update(t *testing.T) {
	_, repo := setupFactRepoTest(t)
	ctx := context.Background()

	fact, _ := domain.NewFact("user123", "", "user", "Original content", 0.7, []string{})
	require.NoError(t, repo.Create(ctx, fact))

	fact.Content = "Updated content"
	fact.Importance = 0.9
	err := repo.Update(ctx, fact)
	require.NoError(t, err)

	retrieved, err := repo.GetByID(ctx, fact.ID)
	require.NoError(t, err)
	require.Equal(t, "Updated content", retrieved.Content)
	require.Equal(t, 0.9, retrieved.Importance)
}

func TestFactRepo_ListActive(t *testing.T) {
	_, repo := setupFactRepoTest(t)
	ctx := context.Background()

	f1, _ := domain.NewFact("user123", "agent1", "user", "Fact 1", 0.8, []string{})
	f2, _ := domain.NewFact("user123", "agent1", "agent", "Fact 2", 0.7, []string{})
	require.NoError(t, repo.Create(ctx, f1))
	require.NoError(t, repo.Create(ctx, f2))

	filter := domain.BuildScopeFilter("user123", "agent1", "user")
	facts, err := repo.ListActive(ctx, filter, 10)
	require.NoError(t, err)
	require.Len(t, facts, 2)
}

func TestFactRepo_SearchByContent(t *testing.T) {
	_, repo := setupFactRepoTest(t)
	ctx := context.Background()

	f1, _ := domain.NewFact("user123", "", "user", "User prefers dark mode", 0.8, []string{})
	require.NoError(t, repo.Create(ctx, f1))

	filter := domain.BuildScopeFilter("user123", "", "user")
	results, err := repo.SearchByContent(ctx, filter, "dark", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, f1.ID, results[0].ID)
}

func TestFactRepo_CountByUser(t *testing.T) {
	_, repo := setupFactRepoTest(t)
	ctx := context.Background()

	f1, _ := domain.NewFact("user123", "", "user", "Fact 1", 0.8, []string{})
	f2, _ := domain.NewFact("user123", "", "user", "Fact 2", 0.7, []string{})
	require.NoError(t, repo.Create(ctx, f1))
	require.NoError(t, repo.Create(ctx, f2))

	count, err := repo.CountByUser(ctx, "user123")
	require.NoError(t, err)
	require.Equal(t, 2, count)
}

func TestFactRepo_DeleteOldSoftDeleted(t *testing.T) {
	_, repo := setupFactRepoTest(t)
	ctx := context.Background()

	f1, _ := domain.NewFact("user123", "", "user", "Old deleted fact", 0.7, []string{})
	require.NoError(t, repo.Create(ctx, f1))

	f1.MarkDeleted()
	require.NoError(t, repo.Update(ctx, f1))

	// This would hard-delete facts deleted more than N days ago
	count, err := repo.DeleteOldSoftDeleted(ctx, 0)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}
