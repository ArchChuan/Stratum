package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

var docColumns = []string{"id", "workspace_id", "title", "content_hash", "ingest_status", "ingest_error",
	"processed_chunks", "total_chunks", "allowed_user_ids", "allowed_role_ids", "created_by",
	"created_at", "ingest_started_at", "ingest_finished_at"}

func docRow() []any {
	started := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	finished := started.Add(time.Minute)
	return []any{"d1", "ws", "src.txt", "hash1", "completed", "", 3, 4,
		[]string{"u1"}, []string{"member"}, "creator-1",
		started, &started, &finished}
}

func TestDocRepo_ExistsByHash_true(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("ws", "hash1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectCommit()

	exists, err := repo.ExistsByHash(context.Background(), "t1", "ws", "hash1")
	require.NoError(t, err)
	require.True(t, exists)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_ExistsByHash_false(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("ws", "hash2").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectCommit()

	exists, err := repo.ExistsByHash(context.Background(), "t1", "ws", "hash2")
	require.NoError(t, err)
	require.False(t, exists)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_Save_defaultStatus(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("INSERT INTO knowledge_docs").
		WithArgs("d1", "ws", "src.txt", "hash1", "processing", 4, []string{}, []string{}, nil).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	// Empty IngestStatus exercises the default-to-"processing" branch.
	doc := &domain.Document{ID: "d1", KBID: "ws", Source: "src.txt", ContentHash: "hash1", TotalChunks: 4}
	require.NoError(t, repo.Save(context.Background(), "t1", "ws", doc))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_Save_explicitStatus(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("INSERT INTO knowledge_docs").
		WithArgs("d1", "ws", "src.txt", "hash1", "completed", 4,
			[]string{"u1", "u2"}, []string{"member"}, "creator-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	doc := &domain.Document{ID: "d1", KBID: "ws", Source: "src.txt", ContentHash: "hash1",
		IngestStatus: "completed", TotalChunks: 4,
		AllowedUserIDs: []string{"u1", "u2"}, AllowedRoleIDs: []string{"member"}, CreatedBy: "creator-1"}
	require.NoError(t, repo.Save(context.Background(), "t1", "ws", doc))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_Save_execFails(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("INSERT INTO knowledge_docs").
		WithArgs("d1", "ws", "src.txt", "hash1", "processing", 4, []string{}, []string{}, nil).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.Save(context.Background(), "t1", "ws",
		&domain.Document{ID: "d1", KBID: "ws", Source: "src.txt", ContentHash: "hash1", TotalChunks: 4})
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_List_success(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("FROM knowledge_docs").
		WithArgs("ws").
		WillReturnRows(pgxmock.NewRows(docColumns).AddRow(docRow()...))
	mock.ExpectCommit()

	docs, err := repo.List(context.Background(), "t1", "ws")
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, "completed", docs[0].IngestStatus)
	require.NotNil(t, docs[0].IngestStartedAt)
	require.Equal(t, 3, docs[0].ProcessedChunks)
	require.Equal(t, []string{"u1"}, docs[0].AllowedUserIDs)
	require.Equal(t, []string{"member"}, docs[0].AllowedRoleIDs)
	require.Equal(t, "creator-1", docs[0].CreatedBy)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_List_queryFails(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("FROM knowledge_docs").
		WithArgs("ws").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	docs, err := repo.List(context.Background(), "t1", "ws")
	require.Nil(t, docs)
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_CountByWorkspace(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("COUNT\\(\\*\\) FROM knowledge_docs").
		WithArgs("ws").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(7))
	mock.ExpectCommit()

	count, err := repo.CountByWorkspace(context.Background(), "t1", "ws")
	require.NoError(t, err)
	require.Equal(t, 7, count)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_Delete_success(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("DELETE FROM knowledge_chunks WHERE workspace_id=\\$1 AND doc_id=\\$2").
		WithArgs("ws", "d1").
		WillReturnResult(pgxmock.NewResult("DELETE", 3))
	mock.ExpectExec("DELETE FROM knowledge_parent_chunks WHERE workspace_id=\\$1 AND doc_id=\\$2").
		WithArgs("ws", "d1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec("DELETE FROM knowledge_docs WHERE workspace_id=\\$1 AND id=\\$2").
		WithArgs("ws", "d1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Delete(context.Background(), "t1", "ws", "d1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_Delete_chunkDeleteFails(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("DELETE FROM knowledge_chunks WHERE workspace_id=\\$1 AND doc_id=\\$2").
		WithArgs("ws", "d1").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.Delete(context.Background(), "t1", "ws", "d1")
	require.ErrorContains(t, err, "delete knowledge_chunks")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_Delete_notFound(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("DELETE FROM knowledge_chunks WHERE workspace_id=\\$1 AND doc_id=\\$2").
		WithArgs("ws", "nope").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectExec("DELETE FROM knowledge_parent_chunks WHERE workspace_id=\\$1 AND doc_id=\\$2").
		WithArgs("ws", "nope").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectExec("DELETE FROM knowledge_docs WHERE workspace_id=\\$1 AND id=\\$2").
		WithArgs("ws", "nope").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	// ErrDocumentNotFound is returned inside the tx, so ExecTenantWith rolls back.
	mock.ExpectRollback()

	err := repo.Delete(context.Background(), "t1", "ws", "nope")
	require.ErrorIs(t, err, domain.ErrDocumentNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_MarkIngestStarted(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("SET ingest_status='processing'").
		WithArgs("d1", 5).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.MarkIngestStarted(context.Background(), "t1", "d1", 5))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_MarkIngestCompleted(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("SET ingest_status='completed'").
		WithArgs("d1", 5).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.MarkIngestCompleted(context.Background(), "t1", "d1", 5))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_MarkIngestFailed(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("SET ingest_status='failed'").
		WithArgs("d1", "boom").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.MarkIngestFailed(context.Background(), "t1", "d1", "boom"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_RecoverStuckIngests(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("ingest aborted by server restart").
		WithArgs("3600 seconds").
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))
	mock.ExpectCommit()

	count, err := repo.RecoverStuckIngests(context.Background(), "t1", time.Hour)
	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_RecoverStuckIngests_execFails(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("ingest aborted by server restart").
		WithArgs("3600 seconds").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	count, err := repo.RecoverStuckIngests(context.Background(), "t1", time.Hour)
	require.Zero(t, count)
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_VisibleDocIDs_success(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("SELECT id FROM knowledge_docs").
		WithArgs("ws", "u1", "member").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("d1").AddRow("d3"))
	mock.ExpectCommit()

	ids, err := repo.VisibleDocIDs(context.Background(), "t1", "ws", "u1", "member")
	require.NoError(t, err)
	require.Equal(t, []string{"d1", "d3"}, ids)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_VisibleDocIDs_queryFails(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("SELECT id FROM knowledge_docs").
		WithArgs("ws", "u1", "member").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	ids, err := repo.VisibleDocIDs(context.Background(), "t1", "ws", "u1", "member")
	require.Nil(t, ids)
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_GetByID_success(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("FROM knowledge_docs WHERE workspace_id=\\$1 AND id=\\$2").
		WithArgs("ws", "d1").
		WillReturnRows(pgxmock.NewRows(docColumns).AddRow(docRow()...))
	mock.ExpectCommit()

	doc, err := repo.GetByID(context.Background(), "t1", "ws", "d1")
	require.NoError(t, err)
	require.Equal(t, "d1", doc.ID)
	require.Equal(t, "src.txt", doc.Source)
	require.Equal(t, []string{"u1"}, doc.AllowedUserIDs)
	require.Equal(t, []string{"member"}, doc.AllowedRoleIDs)
	require.Equal(t, "creator-1", doc.CreatedBy)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_GetByID_notFound(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("FROM knowledge_docs WHERE workspace_id=\\$1 AND id=\\$2").
		WithArgs("ws", "nope").
		WillReturnRows(pgxmock.NewRows(docColumns)) // no rows -> pgx.ErrNoRows
	mock.ExpectRollback()

	doc, err := repo.GetByID(context.Background(), "t1", "ws", "nope")
	require.Nil(t, doc)
	require.ErrorIs(t, err, domain.ErrDocumentNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_SetDocAccess_success(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("UPDATE knowledge_docs").
		WithArgs("d1", []string{"u1", "u2"}, []string{"member"}).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.SetDocAccess(context.Background(), "t1", "d1", []string{"u1", "u2"}, []string{"member"}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDocRepo_SetDocAccess_notFound(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewDocRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("UPDATE knowledge_docs").
		WithArgs("nope", []string{"u1"}, []string{}).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	// ErrDocumentNotFound is returned inside the tx, so ExecTenantWith rolls back.
	mock.ExpectRollback()

	err := repo.SetDocAccess(context.Background(), "t1", "nope", []string{"u1"}, []string{})
	require.ErrorIs(t, err, domain.ErrDocumentNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}
