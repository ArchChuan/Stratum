package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/persistence"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

const testSummaryTenant = "tenant_test_summaries"

func setupHistoryIntegration(t *testing.T) (*pgxpool.Pool, *persistence.HistoryRepo) {
	t.Helper()
	pool := NewTestTenantPool(t, testSummaryTenant)
	return pool, persistence.NewHistoryRepo(pool)
}

// TestHistoryRepo_ListUserSummaries_nullablePeriod_Integration 回归：period_start/
// period_end 无 NOT NULL，历史摘要可能为 NULL（如自动聚合无明确结束时间）。
// 旧实现 scan 到非指针 time.Time 使 GET /memory/summaries 500。修复方案：
// historyListUserQuery 对 NULL 列 COALESCE 兜底（period_start/period_end 回退
// created_at，source_ids 回退空数组，aggregation_key 回退空串），NULL 行必须
// 正常返回。此测试走真实 Postgres，pgx 会真实报 "cannot scan NULL"。
func TestHistoryRepo_ListUserSummaries_nullablePeriod_Integration(t *testing.T) {
	pool, repo := setupHistoryIntegration(t)
	ctx := context.Background()

	convID := uuid.NewString()
	// memory_summaries.conversation_id FK → chat_conversations → agents。
	insertIntoTenant(t, pool,
		`INSERT INTO agents (id, name) VALUES ('e2e-hist-agent', 'e2e')`)
	insertIntoTenant(t, pool,
		`INSERT INTO chat_conversations (id, agent_id, user_id) VALUES ($1, 'e2e-hist-agent', 'e2e-user')`, convID)
	// period_start/period_end 显式 NULL；covered_until NOT NULL 需给值。
	insertIntoTenant(t, pool,
		`INSERT INTO memory_summaries
		   (id, conversation_id, user_id, agent_id, summary, tier, period_start, period_end,
		    covered_until, importance, confidence, status, scope, created_at, updated_at)
		 VALUES ($1, $2, 'e2e-user', 'e2e-hist-agent', 'summary with NULL period', 'recent_months',
		    NULL, NULL, now(), 0.8, 0.9, 'active', 'user', now(), now())`,
		uuid.NewString(), convID)

	got, err := repo.ListUserSummaries(ctx, testSummaryTenant, "e2e-user", 10, 0)
	require.NoError(t, err, "NULL period_start/period_end 不应导致 scan 崩溃")
	require.Len(t, got, 1)
	require.True(t, got[0].PeriodEnd.Equal(got[0].CreatedAt), "NULL period_end 应回退为 created_at")
	require.True(t, got[0].PeriodStart.Equal(got[0].CreatedAt), "NULL period_start 应回退为 created_at")
	require.Empty(t, got[0].SourceIDs, "NULL source_ids 应回退为空数组")
	require.Equal(t, "", got[0].AggregationKey, "NULL aggregation_key 应回退为空串")
}

// insertIntoTenantSchema 在指定 tenant schema 内执行语句（schema 名 = tenant_ + tenantID，
// 与 ExecTenantWith 的 search_path 规则一致）。
func insertIntoTenantSchema(t *testing.T, pool *pgxpool.Pool, schema string, sql string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `SET LOCAL search_path TO "`+schema+`"`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, sql, args...)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}

// insertIntoTenant 在 testSummaryTenant 的 tenant schema 内执行语句。
func insertIntoTenant(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	insertIntoTenantSchema(t, pool, "tenant_"+testSummaryTenant, sql, args...)
}
