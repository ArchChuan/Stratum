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

var entityColumns = []string{"id", "user_id", "agent_id", "scope", "name", "entity_type", "profile",
	"fact_count", "last_seen_at", "rebuild_after", "status", "created_at", "updated_at"}

// Scan targets are **string/**time.Time, so pgxmock row values must be *string/*time.Time (or nil for NULL).
func entityRow(agent *string, rebuild *time.Time) []any {
	t := ts()
	return []any{"e1", "u1", agent, "user", "alice", "person", "profile text",
		5, t, rebuild, "active", t, t}
}

func TestEntityRepo_Create_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	ag, rb := "ag1", ts().Add(7*24*time.Hour)
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("INSERT INTO memory_entities").
		WithArgs("e1", "u1", &ag, "agent", "alice", "person", "p", 3, ts(), &rb, "active", ts(), ts()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	e := &domain.MemoryEntity{ID: "e1", UserID: "u1", AgentID: "ag1", Scope: domain.ScopeAgent,
		Name: "alice", EntityType: "person", Profile: "p", FactCount: 3,
		LastSeenAt: ts(), LastProfileRebuildAt: ts(), Status: "active", CreatedAt: ts(), UpdatedAt: ts()}
	require.NoError(t, repo.Create(context.Background(), "t1", e))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_Create_zeroDerivedFields(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	// No agent, zero LastProfileRebuildAt -> both args nil.
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("INSERT INTO memory_entities").
		WithArgs("e1", "u1", (*string)(nil), "user", "bob", "", "", 0, time.Time{}, (*time.Time)(nil), "active", time.Time{}, time.Time{}).
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
		WithArgs(anyArgs(13)...).
		WillReturnError(pgErr23505())
	mock.ExpectRollback()

	err := repo.Create(context.Background(), "t1", &domain.MemoryEntity{ID: "e1", UserID: "u1", Scope: domain.ScopeUser})
	require.ErrorContains(t, err, "duplicate entry")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_GetByID_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	ag, rb := "ag1", ts()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_entities").
		WithArgs("e1").
		WillReturnRows(pgxmock.NewRows(entityColumns).AddRow(entityRow(&ag, &rb)...))
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
		WillReturnRows(pgxmock.NewRows(entityColumns).AddRow(entityRow(nil, nil)...))
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

	rb := ts().Add(7 * 24 * time.Hour)
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("UPDATE memory_entities SET").
		WithArgs("e1", "alice", "person", "p", 6, ts(), &rb, "active", ts()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	e := &domain.MemoryEntity{ID: "e1", Name: "alice", EntityType: "person", Profile: "p", FactCount: 6,
		LastSeenAt: ts(), LastProfileRebuildAt: ts(), Status: "active", UpdatedAt: ts()}
	require.NoError(t, repo.Update(context.Background(), "t1", e))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_Update_notFound(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("UPDATE memory_entities SET").
		WithArgs("nope", "alice", "", "", 0, time.Time{}, (*time.Time)(nil), "active", time.Time{}).
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
		WithArgs(anyArgs(9)...).
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
		WillReturnRows(pgxmock.NewRows(append(entityColumns, "sim")).AddRow(append(entityRow(nil, nil), 0.9)...))
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
		WillReturnRows(pgxmock.NewRows(append(entityColumns, "sim")).AddRow(append(entityRow(&ag, nil), 0.9)...))
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

func TestEntityRepo_ListProfiles_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	ag := "ag1"
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("profile != ''").
		WithArgs("u1", true, "ag1", true, 10).
		WillReturnRows(pgxmock.NewRows(entityColumns).
			AddRow(entityRow(&ag, nil)...).
			AddRow(entityRow(nil, nil)...))
	mock.ExpectCommit()

	filter := domain.ScopeFilter{TenantID: "t1", UserID: "u1", AgentID: "ag1", IncludeUserScope: true, IncludeAgentScope: true}
	entities, err := repo.ListProfiles(context.Background(), filter, 10)
	require.NoError(t, err)
	require.Len(t, entities, 2)
	require.Equal(t, "ag1", entities[0].AgentID)
	require.Empty(t, entities[1].AgentID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_ListProfiles_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("profile != ''").
		WithArgs(anyArgs(5)...).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	filter := domain.ScopeFilter{TenantID: "t1", UserID: "u1"}
	_, err := repo.ListProfiles(context.Background(), filter, 10)
	require.ErrorContains(t, err, "list entity profiles")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_ListProfiles_scanFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("profile != ''").
		WithArgs(anyArgs(5)...).
		WillReturnRows(pgxmock.NewRows(entityColumns).AddRow("e1", 42, nil, "user", "alice", "person", "p",
			5, ts(), nil, "active", ts(), ts()))
	mock.ExpectRollback()

	filter := domain.ScopeFilter{TenantID: "t1", UserID: "u1"}
	_, err := repo.ListProfiles(context.Background(), filter, 10)
	require.ErrorContains(t, err, "scan entity")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_CountByUser(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("COUNT\\(\\*\\) FROM memory_entities").
		WithArgs("u1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(7))
	mock.ExpectCommit()

	n, err := repo.CountByUser(context.Background(), "t1", "u1")
	require.NoError(t, err)
	require.Equal(t, 7, n)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEntityRepo_CountByUser_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockEntityRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("COUNT\\(\\*\\) FROM memory_entities").
		WithArgs(anyArgs(1)...).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.CountByUser(context.Background(), "t1", "u1")
	require.ErrorContains(t, err, "count entities by user")
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
