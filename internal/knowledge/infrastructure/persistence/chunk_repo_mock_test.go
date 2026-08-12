package persistence

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

func TestChunkRepo_InsertBatch_empty(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewChunkRepo(mock)
	// No SQL expectations: empty batch is a no-op.
	require.NoError(t, repo.InsertBatch(context.Background(), "t1", "ws", nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

// Note: pgxmock v2.12 does not implement SendBatch (returns nil), so the
// batch-execution loop of InsertBatch/InsertParentBatch is covered by the
// integration tests; only the empty-input no-op branch is unit-tested here.

func TestChunkRepo_InsertParentBatch_empty(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewChunkRepo(mock)

	require.NoError(t, repo.InsertParentBatch(context.Background(), "t1", "ws", nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChunkRepo_GetParentByID_success(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewChunkRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("FROM knowledge_parent_chunks WHERE id = \\$1 AND workspace_id = \\$2").
		WithArgs("p1", "ws").
		WillReturnRows(pgxmock.NewRows([]string{"id", "workspace_id", "doc_id", "chunk_index", "content"}).
			AddRow("p1", "ws", "d1", int64(0), "parent text"))
	mock.ExpectCommit()

	p, err := repo.GetParentByID(context.Background(), "t1", "ws", "p1")
	require.NoError(t, err)
	require.Equal(t, "d1", p.DocID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChunkRepo_GetParentByID_notFound(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewChunkRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("FROM knowledge_parent_chunks WHERE id = \\$1 AND workspace_id = \\$2").
		WithArgs("nope", "ws").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	p, err := repo.GetParentByID(context.Background(), "t1", "ws", "nope")
	require.Nil(t, p)
	require.ErrorContains(t, err, "chunk_repo: get parent")
	require.ErrorIs(t, err, pgx.ErrNoRows)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChunkRepo_GetChunksByIDs_empty(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewChunkRepo(mock)

	out, err := repo.GetChunksByIDs(context.Background(), "t1", "ws", nil)
	require.Nil(t, out)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChunkRepo_GetChunksByIDs_success(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewChunkRepo(mock)

	repoBeginTenant(mock)
	parent := "p1" // pgxmock scans by direct assignment, so *string values are needed for *string destinations
	mock.ExpectQuery("FROM knowledge_chunks WHERE workspace_id = \\$1 AND id = ANY\\(\\$2\\)").
		WithArgs("ws", []string{"c1", "c2"}).
		WillReturnRows(pgxmock.NewRows([]string{"id", "doc_id", "chunk_index", "content", "parent_id"}).
			AddRow("c1", "d1", int64(0), "text 1", nil).
			AddRow("c2", "d1", int64(1), "text 2", &parent))
	mock.ExpectCommit()

	out, err := repo.GetChunksByIDs(context.Background(), "t1", "ws", []string{"c1", "c2"})
	require.NoError(t, err)
	require.Len(t, out, 2)
	require.Equal(t, "", out[0].ParentID) // NULL parent_id stays empty
	require.Equal(t, "p1", out[1].ParentID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChunkRepo_GetChunksByIDs_queryFails(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewChunkRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("FROM knowledge_chunks WHERE workspace_id = \\$1 AND id = ANY\\(\\$2\\)").
		WithArgs("ws", []string{"c1"}).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.GetChunksByIDs(context.Background(), "t1", "ws", []string{"c1"})
	require.ErrorContains(t, err, "chunk_repo: get by ids")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChunkRepo_KeywordSearch_success(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewChunkRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("plainto_tsquery\\('public\\.chinese_zh'").
		WithArgs("ws", "检索", 5).
		WillReturnRows(pgxmock.NewRows([]string{"id", "doc_id", "chunk_index", "content"}).
			AddRow("c1", "d1", int64(0), "检索结果一").
			AddRow("c2", "d1", int64(1), "检索结果二"))
	mock.ExpectCommit()

	out, err := repo.KeywordSearch(context.Background(), "t1", "ws", "检索", nil, 5)
	require.NoError(t, err)
	require.Len(t, out, 2)
	require.Equal(t, "检索结果一", out[0].Text)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChunkRepo_KeywordSearch_docWhitelist(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewChunkRepo(mock)

	repoBeginTenant(mock)
	// Non-empty docIDs adds the whitelist predicate: doc_id = ANY($3), LIMIT moves to $4.
	mock.ExpectQuery("doc_id = ANY\\(\\$3\\).*LIMIT \\$4").
		WithArgs("ws", "检索", []string{"d1", "d2"}, 5).
		WillReturnRows(pgxmock.NewRows([]string{"id", "doc_id", "chunk_index", "content"}).
			AddRow("c1", "d1", int64(0), "可见结果"))
	mock.ExpectCommit()

	out, err := repo.KeywordSearch(context.Background(), "t1", "ws", "检索", []string{"d1", "d2"}, 5)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, "可见结果", out[0].Text)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChunkRepo_KeywordSearch_queryFails(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewChunkRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectQuery("plainto_tsquery\\('public\\.chinese_zh'").
		WithArgs("ws", "x", 5).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.KeywordSearch(context.Background(), "t1", "ws", "x", nil, 5)
	require.ErrorContains(t, err, "chunk_repo: keyword search")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChunkRepo_DeleteByWorkspace_success(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewChunkRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("DELETE FROM knowledge_chunks WHERE workspace_id = \\$1").
		WithArgs("ws").
		WillReturnResult(pgxmock.NewResult("DELETE", 2))
	mock.ExpectExec("DELETE FROM knowledge_parent_chunks WHERE workspace_id = \\$1").
		WithArgs("ws").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.DeleteByWorkspace(context.Background(), "t1", "ws"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChunkRepo_DeleteByWorkspace_parentDeleteFails(t *testing.T) {
	mock := newRepoMock(t)
	repo := NewChunkRepo(mock)

	repoBeginTenant(mock)
	mock.ExpectExec("DELETE FROM knowledge_chunks WHERE workspace_id = \\$1").
		WithArgs("ws").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec("DELETE FROM knowledge_parent_chunks WHERE workspace_id = \\$1").
		WithArgs("ws").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.DeleteByWorkspace(context.Background(), "t1", "ws")
	require.ErrorContains(t, err, "chunk_repo: delete parents by workspace")
	require.NoError(t, mock.ExpectationsWereMet())
}
