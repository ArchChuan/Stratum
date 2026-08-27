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

func newMockHistoryRepo(mock pgxmock.PgxPoolIface) *HistoryRepo {
	return &HistoryRepo{pool: mock}
}

func validSegment() *domain.HistorySegment {
	start := ts()
	return &domain.HistorySegment{
		ID: "s1", UserID: "u1", AgentID: "ag1", ConversationID: "c1",
		Tier: domain.HistoryTierRecent, Summary: "sum", SourceStart: "2026-01-01", SourceEnd: "2026-01-31",
		Scope: domain.ScopeAgent, AggregationKey: "k1", Status: domain.HistoryStatusActive,
		PeriodStart: start, PeriodEnd: start.Add(time.Hour), Importance: 0.5, Confidence: 0.7,
		SourceIDs: []string{"e1"},
	}
}

func TestHistoryRepo_NextBatch_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	now := ts()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("WITH eligible").
		WithArgs(2, 10).
		WillReturnRows(pgxmock.NewRows([]string{"conversation_id", "user_id", "agent_id", "scope", "id", "content", "created_at"}).
			AddRow("c1", "u1", "ag1", "agent", "e1", "content1", now).
			AddRow("c1", "u1", "ag1", "agent", "e2", "content2", now))
	mock.ExpectCommit()

	batch, err := repo.NextBatch(context.Background(), "t1", 2, 10)
	require.NoError(t, err)
	require.NotNil(t, batch)
	require.Equal(t, "c1", batch.ConversationID)
	require.Equal(t, domain.ScopeAgent, batch.Scope)
	require.Len(t, batch.Entries, 2)
	require.Equal(t, "content2", batch.Entries[1].Content)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_NextBatch_noRows(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("enriched_at IS NOT NULL").
		WithArgs(2, 10).
		WillReturnRows(pgxmock.NewRows([]string{"conversation_id", "user_id", "agent_id", "scope", "id", "content", "created_at"}))
	mock.ExpectCommit()

	batch, err := repo.NextBatch(context.Background(), "t1", 2, 10)
	require.NoError(t, err)
	require.Nil(t, batch)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_NextBatch_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("enriched_at IS NOT NULL").
		WithArgs(2, 10).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.NextBatch(context.Background(), "t1", 2, 10)
	require.ErrorContains(t, err, "query history batch")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_NextBatch_scanFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("enriched_at IS NOT NULL").
		WithArgs(2, 10).
		WillReturnRows(pgxmock.NewRows([]string{"conversation_id", "user_id", "agent_id", "scope", "id", "content", "created_at"}).
			AddRow("c1", "u1", "ag1", "agent", 42, "content1", ts()))
	mock.ExpectRollback()

	_, err := repo.NextBatch(context.Background(), "t1", 2, 10)
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_NextBatch_failOpenOnNilPool(t *testing.T) {
	repo := NewHistoryRepo(nil)
	batch, err := repo.NextBatch(context.Background(), "t1", 2, 10)
	require.NoError(t, err)
	require.Nil(t, batch)
}

func TestHistoryRepo_NextBatch_failOpenOnEmptyTenant(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	batch, err := repo.NextBatch(context.Background(), "", 2, 10)
	require.NoError(t, err)
	require.Nil(t, batch)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_Upsert_invalidSegment(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	h := validSegment()
	h.Summary = ""
	err := repo.Upsert(context.Background(), "t1", h)
	require.ErrorIs(t, err, domain.ErrInvalidHistorySegment)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_Upsert_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	h := validSegment()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("INSERT INTO memory_summaries").
		WithArgs(h.ConversationID, h.UserID, h.AgentID, string(h.Scope), h.Summary, h.PeriodEnd,
			h.Tier, h.PeriodStart, h.PeriodEnd, h.SourceStart, h.SourceEnd, h.AggregationKey,
			h.Importance, h.Confidence, h.Status, h.SourceIDs).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Upsert(context.Background(), "t1", h))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_Upsert_execFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	h := validSegment()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("INSERT INTO memory_summaries").
		WithArgs(anyArgs(16)...).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.Upsert(context.Background(), "t1", h)
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_NextOverflow_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	start := ts()
	cols := []string{"id", "conversation_id", "user_id", "agent_id", "scope", "tier", "summary",
		"period_start", "period_end", "source_start", "source_end", "source_ids", "importance", "confidence"}
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("row_number").
		WithArgs(2, 5, 20).
		WillReturnRows(pgxmock.NewRows(cols).
			AddRow("s1", "c1", "u1", "ag1", "user", "recent_months", "sum1", start, start.Add(time.Hour), "2026-01-01", "2026-01-31", []string{"e1"}, 0.8, 0.9).
			AddRow("s2", "c1", "u1", "ag1", "user", "recent_months", "sum2", start, start.Add(time.Hour), "2026-01-01", "2026-01-31", []string{"e2"}, 0.7, 0.8))
	mock.ExpectCommit()

	group, err := repo.NextOverflow(context.Background(), "t1", 2, 5, 20)
	require.NoError(t, err)
	require.NotNil(t, group)
	require.Equal(t, "c1", group.ConversationID)
	require.Equal(t, "recent_months", group.Tier)
	require.Len(t, group.Sources, 2)
	require.Equal(t, "sum2", group.Sources[1].Summary)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_NextOverflow_noRows(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	cols := []string{"id", "conversation_id", "user_id", "agent_id", "scope", "tier", "summary",
		"period_start", "period_end", "source_start", "source_end", "source_ids", "importance", "confidence"}
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("row_number").
		WithArgs(2, 5, 20).
		WillReturnRows(pgxmock.NewRows(cols))
	mock.ExpectCommit()

	group, err := repo.NextOverflow(context.Background(), "t1", 2, 5, 20)
	require.NoError(t, err)
	require.Nil(t, group)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_NextOverflow_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("row_number").
		WithArgs(2, 5, 20).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.NextOverflow(context.Background(), "t1", 2, 5, 20)
	require.ErrorContains(t, err, "query history overflow")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_ReplaceOverflow_invalidSegment(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	h := validSegment()
	h.Tier = "bogus"
	err := repo.ReplaceOverflow(context.Background(), "t1", h, []string{"e1"})
	require.ErrorIs(t, err, domain.ErrInvalidHistorySegment)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_ReplaceOverflow_emptySourceIDs(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	err := repo.ReplaceOverflow(context.Background(), "t1", validSegment(), nil)
	require.ErrorIs(t, err, domain.ErrInvalidHistorySegment)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_ReplaceOverflow_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	h := validSegment()
	sourceIDs := []string{"e1", "e2"}
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("WITH inserted").
		WithArgs(h.ConversationID, h.UserID, h.AgentID, string(h.Scope), h.Summary, h.PeriodEnd,
			h.Tier, h.PeriodStart, h.PeriodEnd, h.SourceStart, h.SourceEnd, h.AggregationKey,
			h.Importance, h.Confidence, h.Status, h.SourceIDs, sourceIDs).
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))
	mock.ExpectCommit()

	require.NoError(t, repo.ReplaceOverflow(context.Background(), "t1", h, sourceIDs))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_ReplaceOverflow_execFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("WITH inserted").
		WithArgs(anyArgs(17)...).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.ReplaceOverflow(context.Background(), "t1", validSegment(), []string{"e1"})
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_Maintain_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("UPDATE memory_summaries SET tier").
		WithArgs(domain.HistoryTierRecent, pgxmock.AnyArg(), domain.HistoryTierEarlier).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE memory_summaries SET tier").
		WithArgs(domain.HistoryTierEarlier, pgxmock.AnyArg(), domain.HistoryTierBackground).
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))
	mock.ExpectCommit()

	require.NoError(t, repo.Maintain(context.Background(), "t1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_Maintain_promotionFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("UPDATE memory_summaries SET tier").
		WithArgs(domain.HistoryTierRecent, pgxmock.AnyArg(), domain.HistoryTierEarlier).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.Maintain(context.Background(), "t1")
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_ArchiveColdFacts_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("UPDATE memory_facts SET status='archived'").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("fact-1").AddRow("fact-2"))
	mock.ExpectCommit()

	ids, err := repo.ArchiveColdFacts(context.Background(), "t1")
	require.NoError(t, err)
	require.Equal(t, []string{"fact-1", "fact-2"}, ids)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_ArchiveColdFacts_execFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("UPDATE memory_facts SET status='archived'").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.ArchiveColdFacts(context.Background(), "t1")
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_SearchRelevant_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("ORDER BY similarity").
		WithArgs("u1", "ag1", "hello", 5).
		WillReturnRows(pgxmock.NewRows([]string{"summary", "tier"}).
			AddRow("sum1", "recent_months").
			AddRow("sum2", "long_term_background"))
	mock.ExpectCommit()

	segments, err := repo.SearchRelevant(context.Background(), "t1", "u1", "ag1", "hello", 5)
	require.NoError(t, err)
	require.Len(t, segments, 2)
	require.Equal(t, "sum1", segments[0].Summary)
	require.Equal(t, domain.HistoryTierBackground, segments[1].Tier)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_SearchRelevant_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("ORDER BY similarity").
		WithArgs("u1", "ag1", "hello", 5).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.SearchRelevant(context.Background(), "t1", "u1", "ag1", "hello", 5)
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- ListUserSummaries / CountUserSummaries / Delete (management page) ---

var summaryListColumns = []string{"id", "conversation_id", "user_id", "agent_id", "scope", "summary", "tier",
	"period_start", "period_end", "source_start", "source_end", "source_ids",
	"importance", "confidence", "aggregation_key", "status", "created_at", "updated_at"}

func summaryListRow() []any {
	now := ts()
	return []any{"s1", "c1", "user-1", "ag1", "user", "summary", "recent_months",
		now, now.Add(time.Hour), "2026-01-01", "2026-01-31", []string{"e1"},
		0.8, 0.9, "k1", "active", now, now}
}

func TestHistoryRepo_ListUserSummaries_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_summaries").
		WithArgs("user-1", 10, 0).
		WillReturnRows(pgxmock.NewRows(summaryListColumns).AddRow(summaryListRow()...))
	mock.ExpectCommit()

	got, err := repo.ListUserSummaries(context.Background(), "tenant-1", "user-1", 10, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "s1", got[0].ID)
	require.Equal(t, "c1", got[0].ConversationID)
	require.Equal(t, "tenant-1", got[0].TenantID)
	require.Equal(t, domain.ScopeUser, got[0].Scope)
	require.Equal(t, []string{"e1"}, got[0].SourceIDs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_ListUserSummaries_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_summaries").
		WithArgs("user-1", 10, 0).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.ListUserSummaries(context.Background(), "tenant-1", "user-1", 10, 0)
	require.ErrorContains(t, err, "list user summaries")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_CountUserSummaries(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("count\\(\\*\\) FROM memory_summaries").
		WithArgs("user-1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(3))
	mock.ExpectCommit()

	total, err := repo.CountUserSummaries(context.Background(), "tenant-1", "user-1")
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_Delete(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM memory_summaries WHERE id").
		WithArgs("s1", "user-1").WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()
	require.NoError(t, repo.Delete(context.Background(), "tenant-1", "user-1", "s1"))

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM memory_summaries WHERE id").
		WithArgs("s2", "user-1").WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectRollback()
	require.ErrorIs(t, repo.Delete(context.Background(), "tenant-1", "user-1", "s2"), domain.ErrSummaryNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHistoryRepo_Delete_execFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM memory_summaries WHERE id").
		WithArgs("s1", "user-1").WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.Delete(context.Background(), "tenant-1", "user-1", "s1")
	require.ErrorContains(t, err, "delete summary")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestHistoryRepo_ListUserSummaries_nullPeriodEnd 回归：memory_summaries.period_end
// 无 NOT NULL，历史摘要可能 period_start/period_end 为 NULL（如自动聚合无明确
// 结束时间）。scan 到非指针 time.Time 曾导致 pgx "cannot scan NULL" 使
// GET /memory/summaries 整体 500（skill 可空列对齐反例）。修复方案：SQL 层
// COALESCE(period_start, created_at) / COALESCE(period_end, created_at) 兜底，
// repo 收到的永远是回退值。pgxmock 不模拟真实 NULL scan（nil → 零值不报错），
// 因此本测试模拟 COALESCE 后的值，真实 NULL 路径由
// TestHistoryRepo_ListUserSummaries_nullablePeriod_Integration 覆盖。
func TestHistoryRepo_ListUserSummaries_nullPeriodEnd(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockHistoryRepo(mock)

	now := ts()
	row := []any{"s-null", "c1", "user-1", "ag1", "user", "summary-with-null", "recent_months",
		now, now, "2026-01-01", "2026-01-31", []string{"e1"},
		0.8, 0.9, "", "active", now, now}

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM memory_summaries").
		WithArgs("user-1", 10, 0).
		WillReturnRows(pgxmock.NewRows(summaryListColumns).AddRow(row...))
	mock.ExpectCommit()

	got, err := repo.ListUserSummaries(context.Background(), "tenant-1", "user-1", 10, 0)
	require.NoError(t, err, "period 回退值不应导致 scan 崩溃")
	require.Len(t, got, 1)
	require.Equal(t, "s-null", got[0].ID)
	require.True(t, got[0].PeriodEnd.Equal(now), "COALESCE 后 period_end 应等于 created_at")
	require.True(t, got[0].PeriodStart.Equal(now), "COALESCE 后 period_start 应等于 created_at")
	require.Equal(t, "", got[0].AggregationKey, "NULL aggregation_key 应回退为空串")
	require.NoError(t, mock.ExpectationsWereMet())
}
