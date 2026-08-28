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

func newMockActiveSnapshotRepo(mock pgxmock.PgxPoolIface) *ActiveSnapshotRepo {
	return &ActiveSnapshotRepo{pool: mock}
}

// TestActiveSnapshotRepo_ListUser returns every snapshot row for a user across
// agents (management page shows expired/inactive rows too), decoding the source
// JSONB column back into SnapshotSource.
func TestActiveSnapshotRepo_ListUser_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockActiveSnapshotRepo(mock)
	now := ts()

	// #24：JOIN 附带 agent_name/conversation_name（COALESCE 保证非 NULL）。
	cols := []string{"user_id", "agent_id", "work_context", "personal_context", "top_of_mind",
		"source", "expires_at", "updated_at", "version", "status", "agent_name", "conversation_name"}
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_active_snapshots s").
		WithArgs("user-1").
		WillReturnRows(pgxmock.NewRows(cols).AddRow(
			"user-1", "agent-1", []string{"work"}, []string{}, []string{},
			[]byte(`{"type":"message","reference":"msg-1"}`), now.Add(time.Hour), now, int64(3), domain.SnapshotStatusActive,
			"客服助手", "会话A"))
	mock.ExpectCommit()

	got, err := repo.ListUser(context.Background(), "tenant-1", "user-1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "user-1", got[0].UserID)
	require.Equal(t, "agent-1", got[0].AgentID)
	require.Equal(t, "客服助手", got[0].AgentName)
	require.Equal(t, "会话A", got[0].ConversationName)
	require.Equal(t, "message", got[0].Source.Type)
	require.Equal(t, "msg-1", got[0].Source.Reference)
	require.Equal(t, domain.SnapshotStatusActive, got[0].Status)
	require.Equal(t, "tenant-1", got[0].TenantID)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestActiveSnapshotRepo_ListUser_missingNames 覆盖 agent/conversation 不存在时
// COALESCE 回落空串（快照仍展示，标题回落 agent_id）。
func TestActiveSnapshotRepo_ListUser_missingNames(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockActiveSnapshotRepo(mock)
	now := ts()

	cols := []string{"user_id", "agent_id", "work_context", "personal_context", "top_of_mind",
		"source", "expires_at", "updated_at", "version", "status", "agent_name", "conversation_name"}
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_active_snapshots s").
		WithArgs("user-1").
		WillReturnRows(pgxmock.NewRows(cols).AddRow(
			"user-1", "agent-2", []string{}, []string{}, []string{},
			[]byte(`{"type":"message","reference":"msg-2"}`), now.Add(time.Hour), now, int64(1), domain.SnapshotStatusActive,
			"", ""))
	mock.ExpectCommit()

	got, err := repo.ListUser(context.Background(), "tenant-1", "user-1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Empty(t, got[0].AgentName)
	require.Empty(t, got[0].ConversationName)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestActiveSnapshotRepo_ListUser_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockActiveSnapshotRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_active_snapshots").
		WithArgs("user-1").WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.ListUser(context.Background(), "tenant-1", "user-1")
	require.ErrorContains(t, err, "list user snapshots")
	require.NoError(t, mock.ExpectationsWereMet())
}
