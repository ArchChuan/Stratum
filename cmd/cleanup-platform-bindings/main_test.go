package main

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/storage/postgres/postgrestest"
)

func nopCleanup(ctx context.Context, pool pgxPool, logger *zap.Logger, apply bool) error {
	return nil
}

func TestRun_requiresPostgresURL(t *testing.T) {
	logger := zap.NewNop()
	err := run([]string{}, func(string) string { return "" }, logger, nopCleanup)
	require.ErrorContains(t, err, "POSTGRES_URL is required")
}

func TestRun_flagParseError(t *testing.T) {
	logger := zap.NewNop()
	err := run([]string{"-nope"}, func(string) string { return "postgres://test" }, logger, nopCleanup)
	require.ErrorContains(t, err, "parse flags")
}

// TestCleanup_tenantsTableMissing 验证 public.tenants 缺失（relation does not
// exist）时视为 0 租户正常退出，不阻塞全新环境部署。
func TestCleanup_tenantsTableMissing(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)

	mock.ExpectQuery("SELECT id FROM public.tenants").
		WillReturnError(&pgconn.PgError{Code: pgCodeUndefinedTable})

	err = cleanupPlatformBindings(context.Background(), mock, zap.NewNop(), false)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestCleanup_tenantListFails 验证 public.tenants 查询的其他错误必须向上
// 传播（fail closed），禁止静默漏清。
func TestCleanup_tenantListFails(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)

	mock.ExpectQuery("SELECT id FROM public.tenants").
		WillReturnError(context.DeadlineExceeded)

	err = cleanupPlatformBindings(context.Background(), mock, zap.NewNop(), false)
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestCleanup_tenantSchemaMissing 验证未 provision 的租户 schema（relation
// does not exist）跳过该租户，不中断其他租户。
func TestCleanup_tenantSchemaMissing(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)

	mock.ExpectQuery("SELECT id FROM public.tenants").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("t-unprovisioned"))
	mock.ExpectQuery(`FROM "tenant_t-unprovisioned".agent_skill_links`).
		WillReturnError(&pgconn.PgError{Code: pgCodeUndefinedTable})

	err = cleanupPlatformBindings(context.Background(), mock, zap.NewNop(), false)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestCleanup_deleteFails 验证 apply 模式下 DELETE 失败必须中止并传播错误。
func TestCleanup_deleteFails(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)

	mock.ExpectQuery("SELECT id FROM public.tenants").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("t1"))
	mock.ExpectExec(`DELETE FROM "tenant_t1".agent_skill_links`).
		WillReturnError(context.DeadlineExceeded)

	err = cleanupPlatformBindings(context.Background(), mock, zap.NewNop(), true)
	require.Error(t, err)
	require.ErrorContains(t, err, "delete skill bindings")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, mock.ExpectationsWereMet())
}

// countBindings 统计某张绑定表满足 cond 的行数。
func countBindings(t *testing.T, pool *pgxpool.Pool, schema, table, cond string) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM "`+schema+`".`+table+` WHERE `+cond).Scan(&n))
	return n
}

// seedLegacyBindings 构造存量。provision 已 seed：内置 skill
// （builtin:platform-guide）、stratum_docs（platform_managed）及系统助手对
// 二者的绑定；本 helper 追加普通 agent 的存量绑定（platform 与普通对照）与
// 系统助手的额外 platform 绑定（验证 NOT EXISTS 保护对非 provision seed
// 资源同样生效）。
func seedLegacyBindings(t *testing.T, pool *pgxpool.Pool, schema string) {
	t.Helper()
	ctx := context.Background()
	must := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}

	must(`INSERT INTO "`+schema+`".agents (id, name) VALUES ($1,$2)`, "agent-1", "agent-1")
	must(`INSERT INTO "`+schema+`".skills (id, name) VALUES ($1,$2)`, "skill-1", "Skill One")
	must(`INSERT INTO "`+schema+`".rag_workspaces (id, name, management_mode) VALUES ($1,$2,$3)`,
		"00000000-0000-0000-0000-000000000099", "platform_ws", "platform_managed")
	must(`INSERT INTO "`+schema+`".rag_workspaces (id, name) VALUES ($1,$2)`,
		"00000000-0000-0000-0000-000000000002", "team_notes")
	must(`INSERT INTO "`+schema+`".mcp_configs (id, name, transport, management_mode) VALUES ($1,$2,$3,$4)`,
		"mcp-platform-1", "platform mcp", "stdio", "platform_managed")
	must(`INSERT INTO "`+schema+`".mcp_configs (id, name, transport) VALUES ($1,$2,$3)`,
		"mcp-tenant-1", "tenant mcp", "stdio")

	// 普通 agent 的 platform 绑定（应解绑）：builtin skill + 两个 platform
	// workspace（provision seed 的 stratum_docs 与自建 platform_ws）+ platform mcp。
	must(`INSERT INTO "`+schema+`".agent_skill_links (agent_id, skill_id) VALUES ($1,$2)`, "agent-1", "builtin:platform-guide")
	must(`INSERT INTO "`+schema+`".agent_workspaces (agent_id, workspace_id) VALUES ($1,$2)`, "agent-1", "a0a0a0a0-0000-0000-0000-000000000001")
	must(`INSERT INTO "`+schema+`".agent_workspaces (agent_id, workspace_id) VALUES ($1,$2)`, "agent-1", "00000000-0000-0000-0000-000000000099")
	must(`INSERT INTO "`+schema+`".agent_mcp_tool_links (agent_id, server_id, tool_name) VALUES ($1,$2,$3)`, "agent-1", "mcp-platform-1", "tool_a")
	// 普通 agent 的普通绑定（保留）。
	must(`INSERT INTO "`+schema+`".agent_skill_links (agent_id, skill_id) VALUES ($1,$2)`, "agent-1", "skill-1")
	must(`INSERT INTO "`+schema+`".agent_workspaces (agent_id, workspace_id) VALUES ($1,$2)`, "agent-1", "00000000-0000-0000-0000-000000000002")
	must(`INSERT INTO "`+schema+`".agent_mcp_tool_links (agent_id, server_id, tool_name) VALUES ($1,$2,$3)`, "agent-1", "mcp-tenant-1", "tool_b")
	// 系统助手的额外 platform 绑定（保留）。
	must(`INSERT INTO "`+schema+`".agent_workspaces (agent_id, workspace_id) VALUES ($1,$2)`, "stratum-platform-assistant", "00000000-0000-0000-0000-000000000099")
	must(`INSERT INTO "`+schema+`".agent_mcp_tool_links (agent_id, server_id, tool_name) VALUES ($1,$2,$3)`, "stratum-platform-assistant", "mcp-platform-1", "tool_c")
}

// TestCleanupPlatformBindings_realRoundTrip 验证真实库链路：dry-run 只预览
// 不写、apply 只解绑普通 agent 的 platform 绑定、系统助手与普通绑定保留、
// 重复执行幂等。
func TestCleanupPlatformBindings_realRoundTrip(t *testing.T) {
	pool := postgrestest.NewPool(t)
	ctx := context.Background()
	tenantID := postgrestest.CreateTestTenant(t, pool)
	schema := "tenant_" + tenantID
	seedLegacyBindings(t, pool, schema)

	logger := zap.NewNop()

	// dry-run：普通 agent 的 platform 绑定全部存在，不写入。
	require.NoError(t, cleanupPlatformBindings(ctx, pool, logger, false))
	require.Equal(t, 1, countBindings(t, pool, schema, "agent_skill_links", `agent_id='agent-1' AND skill_id='builtin:platform-guide'`))
	require.Equal(t, 1, countBindings(t, pool, schema, "agent_workspaces", `agent_id='agent-1' AND workspace_id='a0a0a0a0-0000-0000-0000-000000000001'`))
	require.Equal(t, 1, countBindings(t, pool, schema, "agent_mcp_tool_links", `agent_id='agent-1' AND server_id='mcp-platform-1'`))

	// apply：只解绑普通 agent 的 platform 绑定。
	require.NoError(t, cleanupPlatformBindings(ctx, pool, logger, true))
	require.Equal(t, 0, countBindings(t, pool, schema, "agent_skill_links", `agent_id='agent-1' AND skill_id='builtin:platform-guide'`))
	require.Equal(t, 0, countBindings(t, pool, schema, "agent_workspaces", `agent_id='agent-1' AND workspace_id='a0a0a0a0-0000-0000-0000-000000000001'`))
	require.Equal(t, 0, countBindings(t, pool, schema, "agent_workspaces", `agent_id='agent-1' AND workspace_id='00000000-0000-0000-0000-000000000099'`))
	require.Equal(t, 0, countBindings(t, pool, schema, "agent_mcp_tool_links", `agent_id='agent-1' AND server_id='mcp-platform-1'`))

	// 普通绑定保留。
	require.Equal(t, 1, countBindings(t, pool, schema, "agent_skill_links", `agent_id='agent-1' AND skill_id='skill-1'`))
	require.Equal(t, 1, countBindings(t, pool, schema, "agent_workspaces", `agent_id='agent-1' AND workspace_id='00000000-0000-0000-0000-000000000002'`))
	require.Equal(t, 1, countBindings(t, pool, schema, "agent_mcp_tool_links", `agent_id='agent-1' AND server_id='mcp-tenant-1'`))

	// 系统助手绑定全部保留（含 provision seed 的 builtin skill / stratum_docs）。
	require.Equal(t, 1, countBindings(t, pool, schema, "agent_skill_links", `agent_id='stratum-platform-assistant' AND skill_id='builtin:platform-guide'`))
	require.Equal(t, 1, countBindings(t, pool, schema, "agent_workspaces", `agent_id='stratum-platform-assistant' AND workspace_id='a0a0a0a0-0000-0000-0000-000000000001'`))
	require.Equal(t, 1, countBindings(t, pool, schema, "agent_workspaces", `agent_id='stratum-platform-assistant' AND workspace_id='00000000-0000-0000-0000-000000000099'`))
	require.Equal(t, 1, countBindings(t, pool, schema, "agent_mcp_tool_links", `agent_id='stratum-platform-assistant' AND server_id='mcp-platform-1'`))

	// 幂等：第二次 apply 无行可删。普通 agent 不再持有任何 platform 绑定，
	// 系统助手绑定保持 apply 前的数量（不依赖 provision seed 的具体内置数）。
	require.NoError(t, cleanupPlatformBindings(ctx, pool, logger, true))
	require.Equal(t, 0, countBindings(t, pool, schema, "agent_skill_links", `agent_id='agent-1' AND skill_id LIKE 'builtin:%'`))
	require.Equal(t, 1, countBindings(t, pool, schema, "agent_skill_links", `agent_id='stratum-platform-assistant' AND skill_id='builtin:platform-guide'`))
	require.Equal(t, 2, countBindings(t, pool, schema, "agent_workspaces", `agent_id='stratum-platform-assistant' AND workspace_id IN ('a0a0a0a0-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000099')`))
	require.Equal(t, 1, countBindings(t, pool, schema, "agent_mcp_tool_links", `agent_id='stratum-platform-assistant' AND server_id='mcp-platform-1'`))
}
