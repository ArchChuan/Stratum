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
	// NewTestTenantPool 会 provision "tenant_"+tenantID，必须与 repo 方法里的
	// tenantID（testTenantID="test_entities" → search_path "tenant_test_entities"）一致；
	// 不能重复加前缀，否则 provision 到 tenant_tenant_test_entities 而查询落到空 schema。
	pool := NewTestTenantPool(t, testTenantID)
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

	entity.IncrementFactCount()
	err := repo.Update(ctx, testTenantID, entity)
	require.NoError(t, err)

	retrieved, err := repo.GetByID(ctx, testTenantID, entity.ID)
	require.NoError(t, err)
	require.Equal(t, 1, retrieved.FactCount)
}

func TestEntityRepo_ListUserEntities_ordersByLastSeenAndPaginates(t *testing.T) {
	_, repo := setupEntityRepoTest(t)
	ctx := context.Background()

	e1, _ := domain.NewEntity("user123", "", "user", "Python", "technology")
	e2, _ := domain.NewEntity("user123", "", "user", "FastAPI", "technology")
	e3, _ := domain.NewEntity("user123", "agent1", "agent", "AgentScopeOnly", "technology")
	require.NoError(t, repo.Create(ctx, testTenantID, e1))
	require.NoError(t, repo.Create(ctx, testTenantID, e2))
	require.NoError(t, repo.Create(ctx, testTenantID, e3))

	// 只返回 user scope；agent scope 实体不得混入用户级列表。
	page1, err := repo.ListUserEntities(ctx, testTenantID, "user123", 1, 0)
	require.NoError(t, err)
	require.Len(t, page1, 1)
	require.NotEqual(t, "AgentScopeOnly", page1[0].Name)

	page2, err := repo.ListUserEntities(ctx, testTenantID, "user123", 1, 1)
	require.NoError(t, err)
	require.Len(t, page2, 1)
	require.NotEqual(t, page2[0].Name, page1[0].Name)

	empty, err := repo.ListUserEntities(ctx, testTenantID, "user123", 10, 5)
	require.NoError(t, err)
	require.Empty(t, empty)
}

func TestEntityRepo_CountUserEntities_countsOnlyUserScope(t *testing.T) {
	_, repo := setupEntityRepoTest(t)
	ctx := context.Background()

	e1, _ := domain.NewEntity("user123", "", "user", "Entity1", "person")
	e2, _ := domain.NewEntity("user123", "", "user", "Entity2", "project")
	e3, _ := domain.NewEntity("user123", "agent1", "agent", "Entity3", "tech")
	require.NoError(t, repo.Create(ctx, testTenantID, e1))
	require.NoError(t, repo.Create(ctx, testTenantID, e2))
	require.NoError(t, repo.Create(ctx, testTenantID, e3))

	count, err := repo.CountUserEntities(ctx, testTenantID, "user123")
	require.NoError(t, err)
	require.Equal(t, 2, count)
}
