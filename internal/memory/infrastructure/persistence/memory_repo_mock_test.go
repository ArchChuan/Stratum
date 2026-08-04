package persistence

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

func newMockMemoryRepo(mock pgxmock.PgxPoolIface) *MemoryRepo {
	return &MemoryRepo{pool: mock}
}

func TestMemoryRepo_Add_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockMemoryRepo(mock)

	entry := &domain.MemoryEntry{ID: "e1", TenantID: "t1", Type: "short_term", Role: "user", Content: "hello",
		SessionID: "s1", UserID: "u1", AgentID: "ag1", Importance: 0.9,
		Tags: []string{"a"}, Metadata: map[string]interface{}{"k": "v"}, ExpiresAt: ts()}
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("INSERT INTO memory_entries").
		WithArgs("e1", "short_term", "user", "hello", "s1", "u1", "ag1", 0.9,
			[]string{"a"}, map[string]interface{}{"k": "v"}, ts()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Add(context.Background(), entry))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryRepo_Add_execFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockMemoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("INSERT INTO memory_entries").
		WithArgs(anyArgs(11)...).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.Add(context.Background(), &domain.MemoryEntry{ID: "e1", TenantID: "t1"})
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryRepo_Get_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockMemoryRepo(mock)

	now := ts()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_entries WHERE id = \\$1").
		WithArgs("e1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "type", "role", "content", "session_id", "user_id",
			"agent_id", "importance", "tags", "metadata", "expires_at"}).
			AddRow("e1", domain.MemoryType("short_term"), "user", "hello", "s1", "u1", "ag1", 0.9, nil, nil, now))
	mock.ExpectCommit()

	e, err := repo.Get(context.Background(), "t1", "e1")
	require.NoError(t, err)
	require.Equal(t, "e1", e.ID)
	require.Equal(t, domain.MemoryType("short_term"), e.Type)
	require.Equal(t, "hello", e.Content)
	require.Equal(t, 0.9, e.Importance)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryRepo_Get_notFound(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockMemoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_entries WHERE id = \\$1").
		WithArgs("nope").WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	e, err := repo.Get(context.Background(), "t1", "nope")
	require.Nil(t, e)
	require.ErrorIs(t, err, domain.ErrEntryNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryRepo_Get_scanFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockMemoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_entries WHERE id = \\$1").
		WithArgs("e1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "type", "role", "content", "session_id", "user_id",
			"agent_id", "importance", "tags", "metadata", "expires_at"}).
			AddRow(42, "short_term", "user", "hello", "s1", "u1", "ag1", 0.9, nil, nil, ts()))
	mock.ExpectRollback()

	_, err := repo.Get(context.Background(), "t1", "e1")
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryRepo_Search_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockMemoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("ORDER BY importance DESC").
		WithArgs("u1", "hello", 10).
		WillReturnRows(pgxmock.NewRows([]string{"id", "type", "role", "content", "session_id", "user_id",
			"agent_id", "importance"}).
			AddRow("e1", domain.MemoryType("short_term"), "user", "hello", "s1", "u1", "ag1", 0.9).
			AddRow("e2", domain.MemoryType("long_term"), "assistant", "world", "s1", "u1", "", 0.4))
	mock.ExpectCommit()

	entries, err := repo.Search(context.Background(), "t1", "u1", "hello", 10)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, "world", entries[1].Content)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryRepo_Search_defaultsLimitTo20(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockMemoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("ORDER BY importance DESC").
		WithArgs("u1", "hello", 20).
		WillReturnRows(pgxmock.NewRows([]string{"id", "type", "role", "content", "session_id", "user_id",
			"agent_id", "importance"}))
	mock.ExpectCommit()

	entries, err := repo.Search(context.Background(), "t1", "u1", "hello", 0)
	require.NoError(t, err)
	require.Empty(t, entries)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryRepo_Search_skipsBadRows(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockMemoryRepo(mock)

	rows := pgxmock.NewRows([]string{"id", "type", "role", "content", "session_id", "user_id",
		"agent_id", "importance"}).
		AddRow("e1", domain.MemoryType("short_term"), "user", "hello", "s1", "u1", "ag1", 0.9).
		AddRow(42, domain.MemoryType("short_term"), "user", "bad", "s1", "u1", "ag1", 0.9) // scan error -> skipped
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("ORDER BY importance DESC").
		WithArgs("u1", "hello", 10).WillReturnRows(rows)
	mock.ExpectCommit()

	entries, err := repo.Search(context.Background(), "t1", "u1", "hello", 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "e1", entries[0].ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryRepo_Search_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockMemoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("ORDER BY importance DESC").
		WithArgs("u1", "hello", 10).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.Search(context.Background(), "t1", "u1", "hello", 10)
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryRepo_Delete(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockMemoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM memory_entries WHERE id = \\$1").
		WithArgs("e1").WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Delete(context.Background(), "t1", "e1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryRepo_Delete_execFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockMemoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM memory_entries WHERE id = \\$1").
		WithArgs(anyArgs(1)...).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.Delete(context.Background(), "t1", "e1")
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryRepo_ClearSession(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockMemoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM memory_entries WHERE session_id = \\$1").
		WithArgs("s1").WillReturnResult(pgxmock.NewResult("DELETE", 2))
	mock.ExpectCommit()

	require.NoError(t, repo.ClearSession(context.Background(), "t1", "s1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryRepo_ClearSession_execFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockMemoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM memory_entries WHERE session_id = \\$1").
		WithArgs(anyArgs(1)...).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.ClearSession(context.Background(), "t1", "s1")
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryRepo_DeleteAllByUser_execFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockMemoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM memory_outbox WHERE user_id = \\$1").
		WithArgs("u1").WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec("DELETE FROM memory_extraction_queue WHERE user_id = \\$1").
		WithArgs("u1").WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.DeleteAllByUser(context.Background(), "t1", "u1")
	require.ErrorContains(t, err, "memory: delete user lifecycle data")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryRepo_Stats_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockMemoryRepo(mock)

	now := ts()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM memory_entries$").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(10)))
	mock.ExpectQuery("enriched_at IS NOT NULL").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(4)))
	mock.ExpectQuery("COUNT\\(\\*\\) FROM memory_entities").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(3)))
	mock.ExpectQuery("COUNT\\(\\*\\) FROM chat_conversations").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(2)))
	mock.ExpectQuery("COUNT\\(DISTINCT user_id\\)").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(5)))
	mock.ExpectQuery("COALESCE\\(MAX\\(created_at\\)").
		WillReturnRows(pgxmock.NewRows([]string{"last"}).AddRow(now))
	mock.ExpectCommit()

	stats, err := repo.Stats(context.Background(), "t1")
	require.NoError(t, err)
	require.Equal(t, int64(10), stats.TotalEntries)
	require.Equal(t, int64(4), stats.LongTermCount)
	require.Equal(t, int64(6), stats.ShortTermCount)
	require.Equal(t, int64(3), stats.EntityCount)
	require.Equal(t, int64(2), stats.SessionsCount)
	require.Equal(t, int64(5), stats.ActiveUsers)
	require.Equal(t, now, stats.LastAccessTime)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryRepo_Stats_entityCountFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockMemoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM memory_entries$").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(10)))
	mock.ExpectQuery("enriched_at IS NOT NULL").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(4)))
	mock.ExpectQuery("COUNT\\(\\*\\) FROM memory_entities").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.Stats(context.Background(), "t1")
	require.ErrorContains(t, err, "memory stats entities")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryRepo_Stats_totalFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockMemoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM memory_entries$").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.Stats(context.Background(), "t1")
	require.ErrorContains(t, err, "memory stats total entries")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryRepo_GetSummary_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockMemoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("SELECT summary FROM memory_summaries WHERE conversation_id").
		WithArgs("s1").
		WillReturnRows(pgxmock.NewRows([]string{"summary"}).AddRow("recap"))
	mock.ExpectCommit()

	summary, err := repo.GetSummary(context.Background(), "t1", "s1")
	require.NoError(t, err)
	require.Equal(t, "recap", summary)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryRepo_GetSummary_notFound(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockMemoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("SELECT summary FROM memory_summaries WHERE conversation_id").
		WithArgs("nope").WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	_, err := repo.GetSummary(context.Background(), "t1", "nope")
	require.ErrorIs(t, err, domain.ErrSessionNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryRepo_GetSummary_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockMemoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("SELECT summary FROM memory_summaries WHERE conversation_id").
		WithArgs(anyArgs(1)...).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.GetSummary(context.Background(), "t1", "s1")
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}
