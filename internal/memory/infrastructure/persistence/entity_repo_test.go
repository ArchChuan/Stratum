package persistence_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/persistence"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

const testTenantID = "test_entities"

func setupEntityRepoTest(t *testing.T) (*pgxpool.Pool, *persistence.EntityRepo) {
	t.Helper()
	pool := NewTestTenantPool(t, "tenant_test_entities")
	repo := persistence.NewEntityRepo(pool)
	return pool, repo
}

func TestEntityRepo_Insert(t *testing.T) {
	_, repo := setupEntityRepoTest(t)
	ctx := context.Background()

	entity, err := domain.NewEntity("user123", "", "user", "TypeScript", "technology")
	require.NoError(t, err)

	err = repo.Create(ctx, testTenantID, entity)
	require.NoError(t, err)

	retrieved, err := repo.GetByID(ctx, testTenantID, entity.ID)
	require.NoError(t, err)
	require.Equal(t, entity.Name, retrieved.Name)
	require.Equal(t, entity.EntityType, retrieved.EntityType)
}

func TestEntityRepo_FindByNameAndType(t *testing.T) {
	_, repo := setupEntityRepoTest(t)
	ctx := context.Background()

	entity, _ := domain.NewEntity("user123", "", "user", "TypeScript", "technology")
	require.NoError(t, repo.Create(ctx, testTenantID, entity))

	found, err := repo.FindByNameAndType(ctx, testTenantID, domain.ScopeFilter{UserID: "user123", IncludeUserScope: true}, "TypeScript", "technology", 0.8)
	require.NoError(t, err)
	require.Equal(t, entity.ID, found.ID)
}

func TestEntityRepo_FindByNameAndType_FuzzyMatch(t *testing.T) {
	_, repo := setupEntityRepoTest(t)
	ctx := context.Background()

	entity, _ := domain.NewEntity("user123", "", "user", "PostgreSQL", "technology")
	require.NoError(t, repo.Create(ctx, testTenantID, entity))

	found, err := repo.FindByNameAndType(ctx, testTenantID, domain.ScopeFilter{UserID: "user123", IncludeUserScope: true}, "Postgres", "technology", 0.5)
	require.NoError(t, err)
	require.Equal(t, entity.ID, found.ID)
}

func TestEntityRepo_Update(t *testing.T) {
	_, repo := setupEntityRepoTest(t)
	ctx := context.Background()

	entity, _ := domain.NewEntity("user123", "", "user", "React", "technology")
	require.NoError(t, repo.Create(ctx, testTenantID, entity))

	entity.Profile = "A JavaScript library for building user interfaces"
	entity.IncrementFactCount()
	err := repo.Update(ctx, testTenantID, entity)
	require.NoError(t, err)

	retrieved, err := repo.GetByID(ctx, testTenantID, entity.ID)
	require.NoError(t, err)
	require.Equal(t, 1, retrieved.FactCount)
	require.Contains(t, retrieved.Profile, "JavaScript")
}

func TestEntityRepo_ListProfiles(t *testing.T) {
	_, repo := setupEntityRepoTest(t)
	ctx := context.Background()

	e1, _ := domain.NewEntity("user123", "agent1", "user", "Python", "technology")
	e1.Profile = "A programming language"
	e2, _ := domain.NewEntity("user123", "agent1", "agent", "FastAPI", "technology")
	e2.Profile = "A web framework"
	require.NoError(t, repo.Create(ctx, testTenantID, e1))
	require.NoError(t, repo.Create(ctx, testTenantID, e2))

	filter := domain.BuildScopeFilter("", "user123", "agent1", "user")
	entities, err := repo.ListProfiles(ctx, filter, 10)
	require.NoError(t, err)
	require.Len(t, entities, 2)
}

func TestEntityRepo_CountByUser(t *testing.T) {
	_, repo := setupEntityRepoTest(t)
	ctx := context.Background()

	e1, _ := domain.NewEntity("user123", "", "user", "Entity1", "person")
	e2, _ := domain.NewEntity("user123", "", "user", "Entity2", "project")
	require.NoError(t, repo.Create(ctx, testTenantID, e1))
	require.NoError(t, repo.Create(ctx, testTenantID, e2))

	count, err := repo.CountByUser(ctx, testTenantID, "user123")
	require.NoError(t, err)
	require.Equal(t, 2, count)
}
