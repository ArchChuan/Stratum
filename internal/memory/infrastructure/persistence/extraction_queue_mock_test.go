package persistence

import (
	"context"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

func newMockExtractionQueue(mock pgxmock.PgxPoolIface) *ExtractionQueue {
	return &ExtractionQueue{pool: mock}
}

func extractionTaskFixture() *port.ExtractionTask {
	return &port.ExtractionTask{MessageID: "m1", UserID: "u1", AgentID: "ag1",
		ConversationID: "c1", Scope: "agent", Content: "hi"}
}

func TestExtractionQueue_Enqueue_new(t *testing.T) {
	mock := newFactMock(t)
	q := newMockExtractionQueue(mock)

	task := extractionTaskFixture()
	ag, cv := "ag1", "c1"
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("pg_advisory_xact_lock").WithArgs("m1").WillReturnResult(pgxmock.NewResult("SELECT", 0))
	mock.ExpectQuery("SELECT id FROM memory_extraction_queue WHERE message_id").
		WithArgs("m1").WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("INSERT INTO memory_extraction_queue").
		WithArgs("m1", "u1", &ag, &cv, "agent", "hi", pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(int64(42)))
	mock.ExpectCommit()

	require.NoError(t, q.Enqueue(context.Background(), "t1", task))
	require.Equal(t, int64(42), task.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExtractionQueue_Enqueue_existing(t *testing.T) {
	mock := newFactMock(t)
	q := newMockExtractionQueue(mock)

	task := extractionTaskFixture()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("pg_advisory_xact_lock").WithArgs("m1").WillReturnResult(pgxmock.NewResult("SELECT", 0))
	mock.ExpectQuery("SELECT id FROM memory_extraction_queue WHERE message_id").
		WithArgs("m1").WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(int64(99)))
	mock.ExpectCommit()

	require.NoError(t, q.Enqueue(context.Background(), "t1", task))
	require.Equal(t, int64(99), task.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExtractionQueue_Enqueue_nilOptionalIDs(t *testing.T) {
	mock := newFactMock(t)
	q := newMockExtractionQueue(mock)

	task := &port.ExtractionTask{MessageID: "m2", UserID: "u2", Scope: "user", Content: "c"}
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("pg_advisory_xact_lock").WithArgs("m2").WillReturnResult(pgxmock.NewResult("SELECT", 0))
	mock.ExpectQuery("SELECT id FROM memory_extraction_queue WHERE message_id").
		WithArgs("m2").WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("INSERT INTO memory_extraction_queue").
		WithArgs("m2", "u2", (*string)(nil), (*string)(nil), "user", "c", pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(int64(7)))
	mock.ExpectCommit()

	require.NoError(t, q.Enqueue(context.Background(), "t1", task))
	require.Equal(t, int64(7), task.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExtractionQueue_Enqueue_staleConversationFkeyRetry(t *testing.T) {
	mock := newFactMock(t)
	q := newMockExtractionQueue(mock)

	task := extractionTaskFixture()
	fkErr := &pgconn.PgError{Code: "23503", ConstraintName: "memory_extraction_queue_conversation_id_fkey"}
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("pg_advisory_xact_lock").WithArgs("m1").WillReturnResult(pgxmock.NewResult("SELECT", 0))
	mock.ExpectQuery("SELECT id FROM memory_extraction_queue WHERE message_id").
		WithArgs("m1").WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("INSERT INTO memory_extraction_queue").
		WithArgs("m1", "u1", &task.AgentID, &task.ConversationID, "agent", "hi", pgxmock.AnyArg()).
		WillReturnError(fkErr)
	mock.ExpectRollback()
	// Second attempt drops the stale conversation reference.
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("pg_advisory_xact_lock").WithArgs("m1").WillReturnResult(pgxmock.NewResult("SELECT", 0))
	mock.ExpectQuery("SELECT id FROM memory_extraction_queue WHERE message_id").
		WithArgs("m1").WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("INSERT INTO memory_extraction_queue").
		WithArgs("m1", "u1", &task.AgentID, (*string)(nil), "agent", "hi", pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(int64(5)))
	mock.ExpectCommit()

	require.NoError(t, q.Enqueue(context.Background(), "t1", task))
	require.Equal(t, int64(5), task.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExtractionQueue_Enqueue_lockFails(t *testing.T) {
	mock := newFactMock(t)
	q := newMockExtractionQueue(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("pg_advisory_xact_lock").
		WithArgs("m1").WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := q.Enqueue(context.Background(), "t1", extractionTaskFixture())
	require.ErrorContains(t, err, "lock extraction message")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExtractionQueue_Enqueue_findFails(t *testing.T) {
	mock := newFactMock(t)
	q := newMockExtractionQueue(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("pg_advisory_xact_lock").
		WithArgs("m1").WillReturnResult(pgxmock.NewResult("SELECT", 0))
	mock.ExpectQuery("SELECT id FROM memory_extraction_queue WHERE message_id").
		WithArgs("m1").WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := q.Enqueue(context.Background(), "t1", extractionTaskFixture())
	require.ErrorContains(t, err, "find extraction message")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExtractionQueue_Enqueue_insertFails(t *testing.T) {
	mock := newFactMock(t)
	q := newMockExtractionQueue(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("pg_advisory_xact_lock").
		WithArgs("m1").WillReturnResult(pgxmock.NewResult("SELECT", 0))
	mock.ExpectQuery("SELECT id FROM memory_extraction_queue WHERE message_id").
		WithArgs("m1").WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("INSERT INTO memory_extraction_queue").
		WithArgs(anyArgs(7)...).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	task := extractionTaskFixture()
	err := q.Enqueue(context.Background(), "t1", task)
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExtractionQueue_Dequeue_success(t *testing.T) {
	mock := newFactMock(t)
	q := newMockExtractionQueue(mock)

	now := ts()
	ag, cv := "ag1", "c1"
	trace := "trace-1"
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("UPDATE memory_extraction_queue").
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "message_id", "user_id", "agent_id", "conversation_id",
			"scope", "content", "status", "retry_count", "error_msg", "trace_id", "created_at", "updated_at"}).
			AddRow(int64(42), "m1", "u1", &ag, &cv, "user", "content", "processing", 1, nil, &trace, now, now))
	mock.ExpectCommit()

	task, err := q.Dequeue(context.Background(), "t1")
	require.NoError(t, err)
	require.NotNil(t, task)
	require.Equal(t, int64(42), task.ID)
	require.Equal(t, "ag1", task.AgentID)
	require.Equal(t, "c1", task.ConversationID)
	require.Equal(t, "user", task.Scope)
	require.Equal(t, 1, task.RetryCount)
	require.Equal(t, "trace-1", task.TraceID)
	require.Equal(t, "t1", task.TenantID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExtractionQueue_Dequeue_successNilPointers(t *testing.T) {
	mock := newFactMock(t)
	q := newMockExtractionQueue(mock)

	now := ts()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("UPDATE memory_extraction_queue").
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "message_id", "user_id", "agent_id", "conversation_id",
			"scope", "content", "status", "retry_count", "error_msg", "trace_id", "created_at", "updated_at"}).
			AddRow(int64(1), "m1", "u1", nil, nil, "user", "content", "pending", 0, nil, nil, now, now))
	mock.ExpectCommit()

	task, err := q.Dequeue(context.Background(), "t1")
	require.NoError(t, err)
	require.Empty(t, task.AgentID)
	require.Empty(t, task.ConversationID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExtractionQueue_Dequeue_noRows(t *testing.T) {
	mock := newFactMock(t)
	q := newMockExtractionQueue(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("UPDATE memory_extraction_queue").
		WithArgs(pgxmock.AnyArg()).WillReturnError(pgx.ErrNoRows)
	mock.ExpectCommit()

	task, err := q.Dequeue(context.Background(), "t1")
	require.NoError(t, err)
	require.Nil(t, task)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExtractionQueue_Dequeue_otherError(t *testing.T) {
	mock := newFactMock(t)
	q := newMockExtractionQueue(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("UPDATE memory_extraction_queue").
		WithArgs(pgxmock.AnyArg()).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := q.Dequeue(context.Background(), "t1")
	require.ErrorContains(t, err, "dequeue")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExtractionQueue_MarkCompleted_success(t *testing.T) {
	mock := newFactMock(t)
	q := newMockExtractionQueue(mock)

	claimed := ts()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("UPDATE memory_extraction_queue SET status='completed'").
		WithArgs(int64(42), claimed).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, q.MarkCompleted(context.Background(), "t1", 42, claimed))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExtractionQueue_MarkCompleted_claimExpired(t *testing.T) {
	mock := newFactMock(t)
	q := newMockExtractionQueue(mock)

	claimed := ts()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("UPDATE memory_extraction_queue SET status='completed'").
		WithArgs(int64(42), claimed).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectRollback()

	err := q.MarkCompleted(context.Background(), "t1", 42, claimed)
	require.ErrorContains(t, err, "claim expired")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExtractionQueue_MarkCompleted_execFails(t *testing.T) {
	mock := newFactMock(t)
	q := newMockExtractionQueue(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("UPDATE memory_extraction_queue SET status='completed'").
		WithArgs(anyArgs(2)...).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := q.MarkCompleted(context.Background(), "t1", 42, ts())
	require.ErrorContains(t, err, "mark completed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExtractionQueue_MarkFailed_success(t *testing.T) {
	mock := newFactMock(t)
	q := newMockExtractionQueue(mock)

	claimed := ts()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("retry_count < 2").
		WithArgs(int64(42), claimed, "unknown_error").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, q.MarkFailed(context.Background(), "t1", 42, claimed, "unknown_error"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExtractionQueue_MarkFailed_claimExpired(t *testing.T) {
	mock := newFactMock(t)
	q := newMockExtractionQueue(mock)

	claimed := ts()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("retry_count < 2").
		WithArgs(int64(42), claimed, "extraction_panic").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectRollback()

	err := q.MarkFailed(context.Background(), "t1", 42, claimed, "extraction_panic")
	require.ErrorContains(t, err, "claim expired")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExtractionQueue_MarkFailed_execFails(t *testing.T) {
	mock := newFactMock(t)
	q := newMockExtractionQueue(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("retry_count < 2").
		WithArgs(anyArgs(3)...).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := q.MarkFailed(context.Background(), "t1", 42, ts(), "extraction_failed")
	require.ErrorContains(t, err, "mark failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTruncateExtractionError(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{name: "short kept verbatim", in: "llm timeout", want: "llm timeout"},
		{name: "empty stays empty", in: "", want: ""},
		{name: "whitespace trimmed", in: "  llm timeout  ", want: "llm timeout"},
		{name: "over 200 runes truncated", in: strings.Repeat("x", 300), want: strings.Repeat("x", 200)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, truncateExtractionError(tc.in))
		})
	}
}

func TestExtractionQueue_DeleteOldCompleted_success(t *testing.T) {
	mock := newFactMock(t)
	q := newMockExtractionQueue(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM memory_extraction_queue").
		WithArgs(30).
		WillReturnResult(pgxmock.NewResult("DELETE", 3))
	mock.ExpectCommit()

	n, err := q.DeleteOldCompleted(context.Background(), "t1", 30)
	require.NoError(t, err)
	require.Equal(t, 3, n)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExtractionQueue_DeleteOldCompleted_execFails(t *testing.T) {
	mock := newFactMock(t)
	q := newMockExtractionQueue(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM memory_extraction_queue").
		WithArgs(anyArgs(1)...).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := q.DeleteOldCompleted(context.Background(), "t1", 30)
	require.ErrorContains(t, err, "delete old completed")
	require.NoError(t, mock.ExpectationsWereMet())
}
