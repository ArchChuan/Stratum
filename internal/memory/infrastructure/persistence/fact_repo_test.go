package persistence_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/persistence"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

const testFactTenant = "tenant_test_facts"

func setupFactRepoTest(t *testing.T) (*pgxpool.Pool, *persistence.FactRepo) {
	t.Helper()
	pool := NewTestTenantPool(t, testFactTenant)
	repo := persistence.NewFactRepo(pool)
	return pool, repo
}

func TestFactRepo_Insert(t *testing.T) {
	_, repo := setupFactRepoTest(t)
	ctx := context.Background()

	fact, err := domain.NewFactWithMeta(testFactTenant, "user123", "", "11111111-1111-1111-1111-111111111111", "user", "User prefers dark mode", 0.8, 0.9, "preference", domain.FactSourceExplicitUser, []string{"UI", "preference"})
	require.NoError(t, err)

	err = repo.Create(ctx, testFactTenant, fact)
	require.NoError(t, err)

	retrieved, err := repo.GetByID(ctx, testFactTenant, fact.ID)
	require.NoError(t, err)
	require.Equal(t, fact.Content, retrieved.Content)
	require.Equal(t, fact.Importance, retrieved.Importance)
	require.Equal(t, fact.UserID, retrieved.UserID)
	require.Equal(t, fact.ConversationID, retrieved.ConversationID)
	require.Equal(t, fact.Category, retrieved.Category)
	require.Equal(t, fact.Confidence, retrieved.Confidence)
	require.Equal(t, fact.Source, retrieved.Source)
}

func TestFactRepo_Update(t *testing.T) {
	_, repo := setupFactRepoTest(t)
	ctx := context.Background()

	fact, _ := domain.NewFact(testFactTenant, "user123", "", "", "user", "Original content", 0.7, []string{})
	require.NoError(t, repo.Create(ctx, testFactTenant, fact))

	fact.Content = "Updated content"
	fact.Importance = 0.9
	require.NoError(t, repo.Update(ctx, testFactTenant, fact))

	retrieved, err := repo.GetByID(ctx, testFactTenant, fact.ID)
	require.NoError(t, err)
	require.Equal(t, "Updated content", retrieved.Content)
	require.Equal(t, 0.9, retrieved.Importance)
}

func TestFactRepo_ListActive(t *testing.T) {
	_, repo := setupFactRepoTest(t)
	ctx := context.Background()

	f1, _ := domain.NewFact(testFactTenant, "user123", "agent1", "", "user", "Fact 1", 0.8, []string{})
	f2, _ := domain.NewFact(testFactTenant, "user123", "agent1", "", "agent", "Fact 2", 0.7, []string{})
	require.NoError(t, repo.Create(ctx, testFactTenant, f1))
	require.NoError(t, repo.Create(ctx, testFactTenant, f2))

	filter := domain.BuildScopeFilter(testFactTenant, "user123", "agent1", "user")
	facts, err := repo.ListActive(ctx, testFactTenant, filter, 10)
	require.NoError(t, err)
	require.Len(t, facts, 2)
}

func TestFactRepo_SearchByContent(t *testing.T) {
	_, repo := setupFactRepoTest(t)
	ctx := context.Background()

	f1, _ := domain.NewFact(testFactTenant, "user123", "", "", "user", "User prefers dark mode", 0.8, []string{})
	require.NoError(t, repo.Create(ctx, testFactTenant, f1))

	filter := domain.BuildScopeFilter(testFactTenant, "user123", "", "user")
	results, err := repo.SearchByContent(ctx, testFactTenant, filter, "dark", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, f1.ID, results[0].ID)
}

func TestFactRepo_CountByUser(t *testing.T) {
	_, repo := setupFactRepoTest(t)
	ctx := context.Background()

	f1, _ := domain.NewFact(testFactTenant, "user123", "", "", "user", "Fact 1", 0.8, []string{})
	f2, _ := domain.NewFact(testFactTenant, "user123", "", "", "user", "Fact 2", 0.7, []string{})
	require.NoError(t, repo.Create(ctx, testFactTenant, f1))
	require.NoError(t, repo.Create(ctx, testFactTenant, f2))

	count, err := repo.CountByUser(ctx, testFactTenant, "user123")
	require.NoError(t, err)
	require.Equal(t, 2, count)
}
