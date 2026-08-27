package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

func newMockEntityRepo(mock pgxmock.PgxPoolIface) *EntityRepo {
	return &EntityRepo{pool: mock}
}

var entityColumns = []string{"id", "user_id", "agent_id", "scope", "name", "entity_type",
	"fact_count", "last_seen_at", "status", "created_at", "updated_at"}

// Scan targets are **string/*time.Time, so pgxmock row values must be *string/*time.Time (or nil for NULL).
func entityRow(agent *string) []any {
	t := ts()
	return []any{"e1", "u1", agent, "user", "alice", "person",
		5, t, "active", t, t}
}

func TestEntityRepo_Create_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	ag := "ag1"
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("INSERT INTO memory_entities").
		WithArgs("e1", "u1", &ag, "agent", "alice", "person", 3, ts(), "active", ts(), ts()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	e := &domain.MemoryEntity{ID: "e1", UserID: "u1", AgentID: "ag1", Scope: domain.ScopeAgent,
		Name: "alice", EntityType: "person", FactCount: 3,
		LastSeenAt: ts(), Status: "active", CreatedAt: ts(), UpdatedAt: ts()}
	require.NoError(t, repo.Create(context.Background(), "t1", e))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_Create_zeroDerivedFields(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	// No agent -> agentID arg nil. profile/rebuild_after 列已移出 INSERT，依赖 DDL 默认值。
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("INSERT INTO memory_entities").
		WithArgs("e1", "u1", (*string)(nil), "user", "bob", "", 0, time.Time{}, "active", time.Time{}, time.Time{}).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	e := &domain.MemoryEntity{ID: "e1", UserID: "u1", Scope: domain.ScopeUser, Name: "bob", Status: "active"}
	require.NoError(t, repo.Create(context.Background(), "t1", e))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_Create_duplicate(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("INSERT INTO memory_entities").
		WithArgs(anyArgs(11)...).
		WillReturnError(pgErr23505())
	mock.ExpectRollback()

	err := repo.Create(context.Background(), "t1", &domain.MemoryEntity{ID: "e1", UserID: "u1", Scope: domain.ScopeUser})
	require.ErrorContains(t, err, "duplicate entry")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_GetByID_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	ag := "ag1"
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_entities").
		WithArgs("e1").
		WillReturnRows(pgxmock.NewRows(entityColumns).AddRow(entityRow(&ag)...))
	mock.ExpectCommit()

	e, err := repo.GetByID(context.Background(), "t1", "e1")
	require.NoError(t, err)
	require.Equal(t, "ag1", e.AgentID)
	require.Equal(t, domain.ScopeUser, e.Scope)
	require.Equal(t, "alice", e.Name)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_GetByID_nilOptional(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_entities").
		WithArgs("e1").
		WillReturnRows(pgxmock.NewRows(entityColumns).AddRow(entityRow(nil)...))
	mock.ExpectCommit()

	e, err := repo.GetByID(context.Background(), "t1", "e1")
	require.NoError(t, err)
	require.Empty(t, e.AgentID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_GetByID_notFound(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_entities").
		WithArgs("nope").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	e, err := repo.GetByID(context.Background(), "t1", "nope")
	require.Nil(t, e)
	require.ErrorIs(t, err, domain.ErrEntityNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_GetByID_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_entities").
		WithArgs("e1").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.GetByID(context.Background(), "t1", "e1")
	require.ErrorContains(t, err, "get entity by id")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_Update_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("UPDATE memory_entities SET").
		WithArgs("e1", "alice", "person", 6, ts(), "active", ts()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	e := &domain.MemoryEntity{ID: "e1", Name: "alice", EntityType: "person", FactCount: 6,
		LastSeenAt: ts(), Status: "active", UpdatedAt: ts()}
	require.NoError(t, repo.Update(context.Background(), "t1", e))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_Update_notFound(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("UPDATE memory_entities SET").
		WithArgs("nope", "alice", "", 0, time.Time{}, "active", time.Time{}).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectRollback()

	err := repo.Update(context.Background(), "t1", &domain.MemoryEntity{ID: "nope", Name: "alice", Status: "active"})
	require.ErrorIs(t, err, domain.ErrEntityNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_Update_execFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("UPDATE memory_entities SET").
		WithArgs(anyArgs(7)...).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.Update(context.Background(), "t1", &domain.MemoryEntity{ID: "e1"})
	require.ErrorContains(t, err, "update entity")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_FindByNameAndType_userScope(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("similarity\\(name").
		WithArgs("u1", "alic", "person", 0.6).
		WillReturnRows(pgxmock.NewRows(append(entityColumns, "sim")).AddRow(append(entityRow(nil), 0.9)...))
	mock.ExpectCommit()

	filter := domain.ScopeFilter{UserID: "u1", IncludeUserScope: true}
	e, err := repo.FindByNameAndType(context.Background(), "t1", filter, "alic", "person", 0.6)
	require.NoError(t, err)
	require.Equal(t, "alice", e.Name)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_FindByNameAndType_agentScope(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	ag := "ag1"
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("similarity\\(name").
		WithArgs("u1", "alic", "person", 0.6, "ag1").
		WillReturnRows(pgxmock.NewRows(append(entityColumns, "sim")).AddRow(append(entityRow(&ag), 0.9)...))
	mock.ExpectCommit()

	filter := domain.ScopeFilter{UserID: "u1", AgentID: "ag1", IncludeAgentScope: true}
	e, err := repo.FindByNameAndType(context.Background(), "t1", filter, "alic", "person", 0.6)
	require.NoError(t, err)
	require.Equal(t, "ag1", e.AgentID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_FindByNameAndType_notFound(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("similarity\\(name").
		WithArgs("u1", "zzz", "person", 0.6).
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	filter := domain.ScopeFilter{UserID: "u1", IncludeUserScope: true}
	e, err := repo.FindByNameAndType(context.Background(), "t1", filter, "zzz", "person", 0.6)
	require.Nil(t, e)
	require.ErrorIs(t, err, domain.ErrEntityNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_ListUserEntities_filtersActiveUserScope(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_entities").
		WithArgs("u1", 10, 0).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "entity_type", "fact_count", "last_seen_at"}).
			AddRow("e1", "alice", "person", 5, ts()).
			AddRow("e2", "python", "tech", 2, ts()))
	mock.ExpectCommit()

	entities, err := repo.ListUserEntities(context.Background(), "t1", "u1", 10, 0)
	require.NoError(t, err)
	require.Len(t, entities, 2)
	require.Equal(t, "alice", entities[0].Name)
	require.Equal(t, 5, entities[0].FactCount)
	require.Equal(t, "python", entities[1].Name)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_ListUserEntities_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_entities").
		WithArgs(anyArgs(3)...).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.ListUserEntities(context.Background(), "t1", "u1", 10, 0)
	require.ErrorContains(t, err, "list user entities")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_ListUserEntities_scanFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_entities").
		WithArgs(anyArgs(3)...).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "entity_type", "fact_count", "last_seen_at"}).
			AddRow("e1", 42, "person", 5, ts()))
	mock.ExpectRollback()

	_, err := repo.ListUserEntities(context.Background(), "t1", "u1", 10, 0)
	require.ErrorContains(t, err, "scan user entity")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_CountUserEntities(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("COUNT\\(\\*\\) FROM memory_entities").
		WithArgs("u1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(7))
	mock.ExpectCommit()

	n, err := repo.CountUserEntities(context.Background(), "t1", "u1")
	require.NoError(t, err)
	require.Equal(t, 7, n)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_CountUserEntities_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("COUNT\\(\\*\\) FROM memory_entities").
		WithArgs(anyArgs(1)...).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.CountUserEntities(context.Background(), "t1", "u1")
	require.ErrorContains(t, err, "count user entities")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_DeleteAllByUser(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM memory_entities WHERE user_id = \\$1").
		WithArgs("u1").
		WillReturnResult(pgxmock.NewResult("DELETE", 3))
	mock.ExpectCommit()

	require.NoError(t, repo.DeleteAllByUser(context.Background(), "t1", "u1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_DeleteAllByUser_execFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM memory_entities WHERE user_id").
		WithArgs(anyArgs(1)...).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.DeleteAllByUser(context.Background(), "t1", "u1")
	require.ErrorContains(t, err, "delete entities by user")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_DeleteAllByAgent(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM memory_entities WHERE agent_id = \\$1 AND scope = 'agent'").
		WithArgs("ag1").
		WillReturnResult(pgxmock.NewResult("DELETE", 2))
	mock.ExpectCommit()

	require.NoError(t, repo.DeleteAllByAgent(context.Background(), "t1", "ag1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_DeleteAllByAgent_execFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM memory_entities WHERE agent_id").
		WithArgs(anyArgs(1)...).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.DeleteAllByAgent(context.Background(), "t1", "ag1")
	require.ErrorContains(t, err, "delete entities by agent")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestEntityRepo_Delete removes a single entity by id; a zero-row delete maps
// to domain.ErrEntityNotFound (the tenant schema never loses the tx on miss).
func TestEntityRepo_Delete(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM memory_entities WHERE id").
		WithArgs("entity-1").WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()
	require.NoError(t, repo.Delete(context.Background(), "tenant-1", "entity-1"))

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM memory_entities WHERE id").
		WithArgs("entity-2").WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectRollback()
	require.ErrorIs(t, repo.Delete(context.Background(), "tenant-1", "entity-2"), domain.ErrEntityNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_Delete_execFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM memory_entities WHERE id").
		WithArgs("entity-1").WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.Delete(context.Background(), "tenant-1", "entity-1")
	require.ErrorContains(t, err, "delete entity")
	require.NoError(t, mock.ExpectationsWereMet())
}
