package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

func newRepoMock(t *testing.T) pgxmock.PgxPoolIface {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	return mock
}

func repoBeginTenant(mock pgxmock.PgxPoolIface) {
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
}

func testConfig() domain.WorkspaceConfig {
	return domain.WorkspaceConfig{
		EmbeddingModel:   "text-embedding-3-small",
		ChunkSize:        512,
		ChunkOverlap:     64,
		QueryMode:        "hybrid",
		TopK:             8,
		ChunkingStrategy: "parent_child",
	}
}

var wsColumns = []string{"id", "name", "description", "config", "system_key", "management_mode", "created_by", "created_at", "updated_at"}

func wsRow(id, name string) []any {
	return []any{
		id, name, "desc", jsonbConfig{
			EmbeddingModel: "text-embedding-3-small", ChunkSize: 512, ChunkOverlap: 64,
			QueryMode: "hybrid", TopK: 8, ChunkingStrategy: "parent_child",
		}, "sys", "tenant_managed", "",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestWorkspaceRepo_Create_success(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewWorkspaceRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("INSERT INTO rag_workspaces").
		WithArgs("ws", "desc", toJSONB(testConfig()), "").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("ws-1"))
	mock.ExpectCommit()

	ws := &domain.Workspace{Name: "ws", Description: "desc", Config: testConfig()}
	require.NoError(t, repo.Create(context.Background(), "t1", ws, nil))
	require.Equal(t, "ws-1", ws.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepo_Create_duplicate(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewWorkspaceRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("INSERT INTO rag_workspaces").
		WithArgs("ws", "", toJSONB(testConfig()), "").
		WillReturnError(&pgconn.PgError{Code: "23505", Message: `duplicate key value violates unique constraint "rag_workspaces_name_key"`})
	mock.ExpectRollback()

	err := repo.Create(context.Background(), "t1", &domain.Workspace{Name: "ws", Config: testConfig()}, nil)
	require.ErrorIs(t, err, domain.ErrWorkspaceConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepo_Create_otherErrorWrapped(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewWorkspaceRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("INSERT INTO rag_workspaces").
		WithArgs("ws", "", toJSONB(testConfig()), "").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.Create(context.Background(), "t1", &domain.Workspace{Name: "ws", Config: testConfig()}, nil)
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.ErrorContains(t, err, "workspace_repo: create")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepo_GetByName_success(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewWorkspaceRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("FROM rag_workspaces WHERE name = \\$1").
		WithArgs("ws").
		WillReturnRows(pgxmock.NewRows(wsColumns).AddRow(wsRow("ws-1", "ws")...))
	mock.ExpectCommit()

	ws, err := repo.GetByName(context.Background(), "t1", "ws")
	require.NoError(t, err)
	require.Equal(t, "ws-1", ws.ID)
	require.Equal(t, testConfig(), ws.Config)
	require.Equal(t, "tenant_managed", ws.ManagementMode)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepo_GetByName_notFound(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewWorkspaceRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("FROM rag_workspaces WHERE name = \\$1").
		WithArgs("nope").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	ws, err := repo.GetByName(context.Background(), "t1", "nope")
	require.Nil(t, ws)
	require.ErrorIs(t, err, domain.ErrWorkspaceNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepo_GetByID_success(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewWorkspaceRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("FROM rag_workspaces WHERE id = \\$1::uuid").
		WithArgs("ws-1").
		WillReturnRows(pgxmock.NewRows(wsColumns).AddRow(wsRow("ws-1", "ws")...))
	mock.ExpectCommit()

	ws, err := repo.GetByID(context.Background(), "t1", "ws-1")
	require.NoError(t, err)
	require.Equal(t, "ws", ws.Name)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepo_GetByID_notFound(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewWorkspaceRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("FROM rag_workspaces WHERE id = \\$1::uuid").
		WithArgs("nope").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	_, err := repo.GetByID(context.Background(), "t1", "nope")
	require.ErrorIs(t, err, domain.ErrWorkspaceNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepo_List_success(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewWorkspaceRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("FROM rag_workspaces ORDER BY created_at DESC").
		WillReturnRows(pgxmock.NewRows(wsColumns).
			AddRow(wsRow("ws-1", "a")...).
			AddRow(wsRow("ws-2", "b")...))
	mock.ExpectCommit()

	out, err := repo.List(context.Background(), "t1")
	require.NoError(t, err)
	require.Len(t, out, 2)
	require.Equal(t, "a", out[0].Name)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepo_List_queryFails(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewWorkspaceRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("FROM rag_workspaces ORDER BY created_at DESC").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.List(context.Background(), "t1")
	require.ErrorContains(t, err, "workspace_repo: list")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepo_UpdateWorkspaceAll_nilRenameAndDescription(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewWorkspaceRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("UPDATE rag_workspaces").
		WithArgs((*string)(nil), (*string)(nil), toJSONB(testConfig()), "ws").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	// nil rename/description exercise the COALESCE($1/$2, column) branches.
	require.NoError(t, repo.UpdateWorkspaceAll(context.Background(), "t1", "ws", nil, nil, testConfig(), nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepo_UpdateWorkspaceAll_rename(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewWorkspaceRepo(mock)

	newName := "new"
	repoBeginTenant(mock)
	mock.ExpectExec("UPDATE rag_workspaces").
		WithArgs(&newName, (*string)(nil), toJSONB(testConfig()), "old").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.UpdateWorkspaceAll(context.Background(), "t1", "old", &newName, nil, testConfig(), nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepo_UpdateWorkspaceAll_notFound(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewWorkspaceRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("UPDATE rag_workspaces").
		WithArgs((*string)(nil), (*string)(nil), toJSONB(testConfig()), "nope").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectCommit()

	err := repo.UpdateWorkspaceAll(context.Background(), "t1", "nope", nil, nil, testConfig(), nil)
	require.ErrorIs(t, err, domain.ErrWorkspaceNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepo_UpdateWorkspaceAll_conflict(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewWorkspaceRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("UPDATE rag_workspaces").
		WithArgs((*string)(nil), (*string)(nil), toJSONB(testConfig()), "old").
		WillReturnError(&pgconn.PgError{Code: "23505"})
	mock.ExpectRollback()

	err := repo.UpdateWorkspaceAll(context.Background(), "t1", "old", nil, nil, testConfig(), nil)
	require.ErrorIs(t, err, domain.ErrWorkspaceConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepo_Delete_success(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewWorkspaceRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("DELETE FROM rag_workspaces WHERE name = \\$1").
		WithArgs("ws").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Delete(context.Background(), "t1", "ws", nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepo_Delete_notFound(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewWorkspaceRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("DELETE FROM rag_workspaces WHERE name = \\$1").
		WithArgs("nope").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectCommit()

	err := repo.Delete(context.Background(), "t1", "nope", nil)
	require.ErrorIs(t, err, domain.ErrWorkspaceNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepo_Delete_linked(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewWorkspaceRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("DELETE FROM rag_workspaces WHERE name = \\$1").
		WithArgs("ws").
		WillReturnError(&pgconn.PgError{Code: "23503"})
	mock.ExpectRollback()

	err := repo.Delete(context.Background(), "t1", "ws", nil)
	require.ErrorIs(t, err, domain.ErrWorkspaceLinked)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepo_GetConfigForUpload_success(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewWorkspaceRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("SELECT config FROM rag_workspaces WHERE name = \\$1").
		WithArgs("ws").
		WillReturnRows(pgxmock.NewRows([]string{"config"}).AddRow(jsonbConfig{
			EmbeddingModel: "bge-m3", ChunkSize: 1024, ChunkOverlap: 128,
			QueryMode: "vector", TopK: 4, ChunkingStrategy: "structure_recursive",
		}))
	mock.ExpectCommit()

	cfg, err := repo.GetConfigForUpload(context.Background(), "t1", "ws")
	require.NoError(t, err)
	require.Equal(t, "bge-m3", cfg.EmbeddingModel)
	require.Equal(t, "structure_recursive", cfg.ChunkingStrategy)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepo_GetConfigForUpload_notFound(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewWorkspaceRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("SELECT config FROM rag_workspaces WHERE name = \\$1").
		WithArgs("nope").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	_, err := repo.GetConfigForUpload(context.Background(), "t1", "nope")
	require.ErrorIs(t, err, domain.ErrWorkspaceNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepo_GetConfigByID_notFound(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewWorkspaceRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("SELECT config FROM rag_workspaces WHERE id = \\$1::uuid").
		WithArgs("nope").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	_, err := repo.GetConfigByID(context.Background(), "t1", "nope")
	require.ErrorIs(t, err, domain.ErrWorkspaceNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}
