package persistence_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/persistence"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

const memNullSessionTenant = "tenant_test_mem_null_session"

func setupMemRepoIntegration(t *testing.T) (*pgxpool.Pool, *persistence.MemoryRepo, string) {
	t.Helper()
	pool := NewTestTenantPool(t, memNullSessionTenant)
	insertIntoTenantSchema(t, pool, "tenant_"+memNullSessionTenant,
		`INSERT INTO agents (id, name) VALUES ('e2e-mem-agent', 'e2e')`)
	return pool, persistence.NewMemoryRepo(pool), "tenant_" + memNullSessionTenant
}

// insertMemEntryNullSession 插入一条 session_id 为 NULL 的 memory entry（role/
// content NOT NULL，user_id/agent_id 给值，仅 session_id 置 NULL）。
func insertMemEntryNullSession(t *testing.T, pool *pgxpool.Pool, schema, id string) {
	t.Helper()
	insertIntoTenantSchema(t, pool, schema,
		`INSERT INTO memory_entries (id, type, role, content, session_id, user_id, agent_id, importance, tags, metadata, expires_at)
		 VALUES ($1, 'short_term', 'user', 'null session entry', NULL, 'e2e-user', 'e2e-mem-agent', 0.5, '{}', '{}', NULL)`,
		id)
}

// insertMemEntryAllNullableNull 插入一条可空列全为 NULL 的 memory entry：覆盖
// agent_id 因 ON DELETE SET NULL 变 NULL、user_id 缺失、expires_at 无 TTL 等
// 真实历史数据形态。
func insertMemEntryAllNullableNull(t *testing.T, pool *pgxpool.Pool, schema, id string) {
	t.Helper()
	insertIntoTenantSchema(t, pool, schema,
		`INSERT INTO memory_entries (id, type, role, content, session_id, user_id, agent_id, importance, tags, metadata, expires_at)
		 VALUES ($1, 'short_term', 'user', 'all nullable null entry', NULL, NULL, NULL, 0.5, '{}', '{}', NULL)`,
		id)
}

// TestMemoryRepo_GetNullSessionID_Integration 回归：memory_entries.session_id 无
// NOT NULL（TEXT），历史/手工数据可能为 NULL。旧实现 Get/Search 把 session_id
// scan 到非指针 string，pgx 报 "cannot scan NULL into *string" → DELETE /entries
// 归属校验 500。修复：SQL 对 session_id 加 COALESCE(session_id, ”)。pgxmock 不
// 模拟真实 NULL scan 错误，此测试走真实 Postgres。
func TestMemoryRepo_GetNullSessionID_Integration(t *testing.T) {
	pool, repo, schema := setupMemRepoIntegration(t)
	ctx := context.Background()

	entryID := uuid.NewString()
	insertMemEntryNullSession(t, pool, schema, entryID)

	got, err := repo.Get(ctx, memNullSessionTenant, entryID)
	require.NoError(t, err, "NULL session_id 不应导致 Get scan 崩溃")
	require.Equal(t, "", got.SessionID, "NULL session_id 应回退为空串")
}

// TestMemoryRepo_SearchNullSessionID_Integration Search 与 Get 共用 session_id
// scan 路径，同样必须容忍 NULL。
func TestMemoryRepo_SearchNullSessionID_Integration(t *testing.T) {
	pool, repo, schema := setupMemRepoIntegration(t)
	ctx := context.Background()

	insertMemEntryNullSession(t, pool, schema, uuid.NewString())

	got, err := repo.Search(ctx, memNullSessionTenant, "e2e-user", "", 10)
	require.NoError(t, err, "NULL session_id 不应导致 Search scan 崩溃")
	require.Len(t, got, 1)
	require.Equal(t, "", got[0].SessionID)
}

// TestMemoryRepo_GetAllNullableColumnsNull_Integration 回归：memory_entries 的
// user_id/session_id/agent_id/expires_at 全部可空。agent 删除后 agent_id 被
// ON DELETE SET NULL，历史数据 user_id 可能缺失，entry 无 TTL 时 expires_at 为
// NULL。任一列 NULL 导致 Get scan 崩溃都会让 DELETE /entries 归属校验 500。
func TestMemoryRepo_GetAllNullableColumnsNull_Integration(t *testing.T) {
	pool, repo, schema := setupMemRepoIntegration(t)
	ctx := context.Background()

	entryID := uuid.NewString()
	insertMemEntryAllNullableNull(t, pool, schema, entryID)

	got, err := repo.Get(ctx, memNullSessionTenant, entryID)
	require.NoError(t, err, "可空列全 NULL 不应导致 Get scan 崩溃")
	require.Equal(t, "", got.SessionID)
	require.Equal(t, "", got.UserID)
	require.Equal(t, "", got.AgentID)
	require.False(t, got.ExpiresAt.IsZero(), "NULL expires_at 应回退为非零时间（epoch）")
}
