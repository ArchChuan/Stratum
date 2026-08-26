package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const systemAssistantKey = "stratum.platform_assistant"

func TestProvisionTenantSchemaRemovesPlatformMCPIdempotently(t *testing.T) {
	pool, ctx, tenantID := systemAssistantTestPool(t, "platform_mcp")
	require.NoError(t, postgres.ProvisionTenantSchema(ctx, pool, tenantID))
	schema := `"tenant_` + tenantID + `"`
	_, err := pool.Exec(ctx, `INSERT INTO `+schema+`.mcp_configs (id, name, transport, system_key, management_mode)
		VALUES ('stratum-platform-mcp', 'legacy-platform-mcp', 'streamable-http', 'stratum.platform_mcp', 'platform_managed')`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO `+schema+`.agent_mcp_tool_links (agent_id, server_id, tool_name)
		VALUES ('stratum-platform-assistant', 'stratum-platform-mcp', 'stratum_diagnose_tenant')`)
	require.NoError(t, err)

	require.NoError(t, postgres.ProvisionTenantSchema(ctx, pool, tenantID))

	var managedCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM `+schema+`.mcp_configs
		WHERE id='stratum-platform-mcp'`).Scan(&managedCount))
	require.Zero(t, managedCount)

	var bindingCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM `+schema+`.agent_mcp_tool_links
		WHERE server_id='stratum-platform-mcp'`).Scan(&bindingCount))
	require.Zero(t, bindingCount)
}

func TestProvisionTenantSchemaSystemAssistantIsIdempotent(t *testing.T) {
	pool, ctx, tenantID := systemAssistantTestPool(t, "idempotent")
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	assertOneSystemAssistant(t, pool, tenantID)
}

// TestProvisionTenantSchemaToleratesTerminalApprovalStatuses 回归 CD 部署崩溃：存量租户存在
// cancelled/voided/invalidated 失效终态审批行时，重启 provisioning 的 DROP+ADD 约束重建必须幂等
// 通过。曾因重建点白名单漏这三个失效终态而 SQLSTATE 23514 → 事务回滚 → 启动 fail-closed →
// CrashLoopBackOff → helm --atomic 超时回滚。
func TestProvisionTenantSchemaToleratesTerminalApprovalStatuses(t *testing.T) {
	pool, ctx, tenantID := systemAssistantTestPool(t, "terminal_statuses")
	require.NoError(t, postgres.ProvisionTenantSchema(ctx, pool, tenantID))
	schema := `"tenant_` + tenantID + `"`

	// 存量数据：失效终态在 10 值约束（第二处重建点）下合法写入。
	for i, status := range []string{"cancelled", "voided", "invalidated"} {
		_, err := pool.Exec(ctx,
			`INSERT INTO `+schema+`.agent_tool_approvals
				(execution_id, tool_call_id, server_id, tool_name, risk_level, encrypted_payload, status)
			 VALUES ($1, $2, 'server', 'tool', 'destructive', 'enc', $3)`,
			fmt.Sprintf("e2e-terminal-%d", i), fmt.Sprintf("tc-%d", i), status)
		require.NoError(t, err)
	}

	// 模拟重启：再次 provisioning 必须通过，不得因存量失效终态行触发约束校验失败。
	require.NoError(t, postgres.ProvisionTenantSchema(ctx, pool, tenantID))

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM `+schema+`.agent_tool_approvals`).Scan(&n))
	require.Equal(t, 3, n)
}

// TestProvisionTenantSchemaAllowsGrantEditorProposal 回归 grant_editor 申请 500：
// 存量租户的 operation_proposals op_type check 约束缺 grant_editor 枚举时，
// ProposeGrantEditor 插入即 23514。验证历史旧约束确实拒绝、provision 幂等升级后放行。
func TestProvisionTenantSchemaAllowsGrantEditorProposal(t *testing.T) {
	pool, ctx, tenantID := systemAssistantTestPool(t, "grant_editor")
	require.NoError(t, postgres.ProvisionTenantSchema(ctx, pool, tenantID))
	schema := `"tenant_` + tenantID + `"`
	insertGrantEditor := `INSERT INTO ` + schema + `.operation_proposals
		(id, agent_id, op_type, fingerprint, status)
		VALUES ($1, 'agent-1', 'grant_editor', $2, 'proposed')`

	// 1. 模拟历史租户：约束被替换成不含 grant_editor 的旧版，grant_editor 插入被拒。
	_, err := pool.Exec(ctx, `ALTER TABLE `+schema+`.operation_proposals
		DROP CONSTRAINT IF EXISTS operation_proposals_op_type_check`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `ALTER TABLE `+schema+`.operation_proposals
		ADD CONSTRAINT operation_proposals_op_type_check
		CHECK (op_type IN ('revision_apply','cross_agent_delegate','schedule_create','self_modify'))`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, insertGrantEditor, "e2e-historical", "fp-historical")
	require.Error(t, err, "历史旧约束必须拒绝 grant_editor，否则 500 回归无意义")

	// 2. 重启 provisioning：DROP IF EXISTS + ADD CONSTRAINT 把约束升级为含 grant_editor。
	require.NoError(t, postgres.ProvisionTenantSchema(ctx, pool, tenantID))

	// 3. grant_editor 提案可落库（前端「申请编辑权限」链路的核心写路径）。
	_, err = pool.Exec(ctx, insertGrantEditor, "e2e-grant-editor", "fp-e2e-grant-editor")
	require.NoError(t, err)

	// 4. 幂等：再次 provisioning 不因同名约束重建失败。
	require.NoError(t, postgres.ProvisionTenantSchema(ctx, pool, tenantID))
	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM `+schema+`.operation_proposals WHERE op_type='grant_editor'`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestProvisionTenantSchemaSystemAssistantModelBackfillPreservesTenantChoice(t *testing.T) {
	for _, tt := range []struct {
		name, model, wantModel string
	}{
		{name: "empty managed model", model: "  ", wantModel: "glm-5.2"},
		{name: "tenant override", model: "qwen-plus", wantModel: "qwen-plus"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pool, ctx, tenantID := systemAssistantTestPool(t, "model_backfill")
			schema := `tenant_` + tenantID
			if _, err := pool.Exec(ctx, `CREATE TABLE "`+schema+`".agents (
				id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, type TEXT NOT NULL DEFAULT 'react',
				description TEXT NOT NULL DEFAULT '', system_prompt TEXT NOT NULL DEFAULT '',
				llm_model TEXT NOT NULL DEFAULT '', embed_model TEXT NOT NULL DEFAULT '',
				max_iterations INT NOT NULL DEFAULT 10, max_context_tokens INTEGER NOT NULL DEFAULT 8000,
				memory_scope TEXT NOT NULL DEFAULT 'agent', system_key TEXT,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO "`+schema+`".agents (id, name, llm_model, system_key)
				VALUES ('stratum-platform-assistant', '平台使用助手', $1, $2)`, tt.model, systemAssistantKey); err != nil {
				t.Fatal(err)
			}
			if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
				t.Fatal(err)
			}
			var model string
			if err := pool.QueryRow(ctx, `SELECT llm_model FROM "`+schema+`".agents WHERE system_key=$1`, systemAssistantKey).Scan(&model); err != nil {
				t.Fatal(err)
			}
			if model != tt.wantModel {
				t.Fatalf("managed assistant model = %q, want %q", model, tt.wantModel)
			}
		})
	}
}

func TestProvisionTenantSchemaRestoresFeedbackIdempotencyUniqueness(t *testing.T) {
	pool, ctx, tenantID := systemAssistantTestPool(t, "feedback_idempotency")
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	schema := `"tenant_` + tenantID + `"`
	if _, err := pool.Exec(ctx, `ALTER TABLE `+schema+
		`.evaluation_feedback DROP CONSTRAINT evaluation_feedback_idempotency_key_key`); err != nil {
		t.Fatal(err)
	}
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO ` + schema + `.evaluation_feedback
		(id,trace_id,resource_kind,resource_id,revision_id,idempotency_key) VALUES($1,$2,'skill','skill-1','revision-1','same-key')`
	if _, err := pool.Exec(ctx, insert, "feedback-1", "trace-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, insert, "feedback-2", "trace-2"); err == nil {
		t.Fatal("reprovisioned historical tenant accepted duplicate feedback idempotency key")
	}
}

func TestProvisionTenantSchemaSystemAssistantNameCollisionPreservesOrdinaryAgent(t *testing.T) {
	pool, ctx, tenantID := systemAssistantTestPool(t, "name_collision")
	schema := `tenant_` + tenantID
	if _, err := pool.Exec(ctx, `CREATE TABLE "`+schema+`".agents (
		id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, type TEXT NOT NULL DEFAULT 'react',
		description TEXT NOT NULL DEFAULT '', system_prompt TEXT NOT NULL DEFAULT '',
		llm_model TEXT NOT NULL DEFAULT '', embed_model TEXT NOT NULL DEFAULT '',
		max_iterations INT NOT NULL DEFAULT 10, max_context_tokens INTEGER NOT NULL DEFAULT 8000,
		memory_scope TEXT NOT NULL DEFAULT 'agent', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
		INSERT INTO "`+schema+`".agents (id, name, description) VALUES ('ordinary', 'Stratum 系统助手', 'keep me')`); err != nil {
		t.Fatal(err)
	}

	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	var count int
	var description string
	if err := pool.QueryRow(ctx, `SELECT count(*), max(description) FROM "`+schema+
		`".agents WHERE id='ordinary' AND name='Stratum 系统助手'`).Scan(&count, &description); err != nil {
		t.Fatal(err)
	}
	if count != 1 || description != "keep me" {
		t.Fatalf("ordinary agent changed during provision: count=%d description=%q", count, description)
	}
	assertOneSystemAssistant(t, pool, tenantID)
}

func TestProvisionTenantSchemaSystemAssistantIDCollisionFailsWithoutChangingOrdinaryAgent(t *testing.T) {
	pool, ctx, tenantID := systemAssistantTestPool(t, "id_collision")
	schema := `tenant_` + tenantID
	if _, err := pool.Exec(ctx, `CREATE TABLE "`+schema+`".agents (
		id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, type TEXT NOT NULL DEFAULT 'react',
		description TEXT NOT NULL DEFAULT '', system_prompt TEXT NOT NULL DEFAULT '',
		llm_model TEXT NOT NULL DEFAULT '', embed_model TEXT NOT NULL DEFAULT '',
		max_iterations INT NOT NULL DEFAULT 10, max_context_tokens INTEGER NOT NULL DEFAULT 8000,
		memory_scope TEXT NOT NULL DEFAULT 'agent', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
		INSERT INTO "`+schema+`".agents (id, name, description)
		VALUES ('stratum-platform-assistant', 'Ordinary Agent', 'keep me')`); err != nil {
		t.Fatal(err)
	}

	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err == nil {
		t.Fatal("expected ordinary-agent fixed ID collision to fail provisioning")
	}
	var count int
	var description string
	if err := pool.QueryRow(ctx, `SELECT count(*), max(description) FROM "`+schema+
		`".agents WHERE id='stratum-platform-assistant' AND name='Ordinary Agent'`).Scan(&count, &description); err != nil {
		t.Fatal(err)
	}
	if count != 1 || description != "keep me" {
		t.Fatalf("ordinary agent changed after failed provision: count=%d description=%q", count, description)
	}
}

func TestProvisionTenantSchemaSystemAssistantRollsBackOnLaterFailure(t *testing.T) {
	pool, ctx, tenantID := systemAssistantTestPool(t, "rollback")
	schema := `tenant_` + tenantID
	if _, err := pool.Exec(ctx, `CREATE TABLE "`+schema+`".agents (
		id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, type TEXT NOT NULL DEFAULT 'react',
		description TEXT NOT NULL DEFAULT '', system_prompt TEXT NOT NULL DEFAULT '',
		llm_model TEXT NOT NULL DEFAULT '', embed_model TEXT NOT NULL DEFAULT '',
		max_iterations INT NOT NULL DEFAULT 10, max_context_tokens INTEGER NOT NULL DEFAULT 8000,
		memory_scope TEXT NOT NULL DEFAULT 'agent', system_key TEXT,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
		CREATE VIEW "`+schema+`".skills AS SELECT 'blocked'::text AS id`); err != nil {
		t.Fatal(err)
	}

	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err == nil {
		t.Fatal("expected later tenant DDL failure")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM "`+schema+`".agents WHERE system_key=$1`,
		systemAssistantKey).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed provision left %d managed assistant rows", count)
	}
}

func systemAssistantTestPool(t *testing.T, suffix string) (*pgxpool.Pool, context.Context, string) {
	t.Helper()
	url := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("STRATUM_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	tenantID := fmt.Sprintf("tmp_system_assistant_%s_%d", suffix, time.Now().UnixNano())
	schema := `tenant_` + tenantID
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	return pool, ctx, tenantID
}

func assertOneSystemAssistant(t *testing.T, pool *pgxpool.Pool, tenantID string) {
	t.Helper()
	var count int
	ctx := tenantdb.WithTenant(context.Background(), &tenantdb.TenantContext{TenantID: tenantID})
	err := tenantdb.ExecTenant(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM agents WHERE system_key=$1`, systemAssistantKey).Scan(&count)
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("managed assistant count=%d, want 1", count)
	}
}

func TestProvisionTenantSchemaPreservesLegacySkillsAndDropsAgentObservationTables(t *testing.T) {
	url := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("STRATUM_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()); err != nil {
		t.Fatal(err)
	}

	tenantID := fmt.Sprintf("tmp_skill_reset_%d", time.Now().UnixNano())
	schema := `tenant_` + tenantID
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	legacy := `SET search_path = "` + schema + `", public;
		CREATE TABLE skills (id TEXT PRIMARY KEY, name TEXT);
		CREATE TABLE skill_versions (id TEXT PRIMARY KEY, skill_id TEXT, implementation JSONB);
		CREATE TABLE agent_skill_links (agent_id TEXT, skill_id TEXT);
		CREATE TABLE skill_test_cases (id TEXT);
		CREATE TABLE skill_eval_runs (id TEXT);
		CREATE TABLE agent_tool_traces (
		 id UUID PRIMARY KEY DEFAULT public.gen_uuid_v7(), trace_id TEXT NOT NULL DEFAULT '',
		 conversation_id UUID, step_index INT NOT NULL DEFAULT 0, provider_type TEXT NOT NULL DEFAULT '',
		 raw_result_text TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE TABLE agent_executions (
		 id UUID PRIMARY KEY DEFAULT public.gen_uuid_v7(), trace_id TEXT NOT NULL DEFAULT '',
		 status TEXT NOT NULL DEFAULT 'success', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE TABLE agent_trace_events (
		 id UUID PRIMARY KEY DEFAULT public.gen_uuid_v7(), trace_id TEXT NOT NULL DEFAULT '',
		 conversation_id UUID, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		INSERT INTO skills VALUES ('legacy-skill', 'legacy');
		INSERT INTO skill_versions VALUES ('legacy-version', 'legacy-skill', '{"mode":"code"}');
		INSERT INTO agent_skill_links VALUES ('legacy-agent', 'legacy-skill');
		INSERT INTO skill_test_cases VALUES ('legacy-case');
		INSERT INTO skill_eval_runs VALUES ('legacy-run');
		INSERT INTO agent_tool_traces (provider_type, raw_result_text) VALUES ('skill', 'historical');
		INSERT INTO agent_executions DEFAULT VALUES;
		INSERT INTO agent_trace_events DEFAULT VALUES;`
	if _, err := pool.Exec(ctx, legacy); err != nil {
		t.Fatal(err)
	}

	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	var legacyRows, revisions, observationTables int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM "`+schema+`".skills WHERE id='legacy-skill') +
		(SELECT count(*) FROM "`+schema+`".skill_versions WHERE id='legacy-version') +
		(SELECT count(*) FROM "`+schema+`".agent_skill_links WHERE agent_id='legacy-agent' AND skill_id='legacy-skill') +
		(SELECT count(*) FROM "`+schema+`".skill_test_cases WHERE id='legacy-case') +
		(SELECT count(*) FROM "`+schema+`".skill_eval_runs WHERE id='legacy-run')`).Scan(&legacyRows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=$1 AND table_name='skill_revisions'`, schema).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=$1
		AND table_name IN ('agent_executions','agent_tool_traces','agent_trace_events')`, schema).Scan(&observationTables); err != nil {
		t.Fatal(err)
	}
	if legacyRows != 5 || revisions != 1 || observationTables != 0 {
		t.Fatalf("legacy_rows=%d revisions=%d observation_tables=%d",
			legacyRows, revisions, observationTables)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO "`+schema+`".skills (id,name) VALUES ('new-skill','new'); INSERT INTO "`+schema+`".skill_revisions (id,skill_id,instructions) VALUES ('new-revision','new-skill','instructions')`); err != nil {
		t.Fatal(err)
	}
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	var newRows, legacyRowsAfterSecondProvision, observationTablesAfterSecondProvision int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM "`+schema+`".skill_revisions WHERE id='new-revision'`).Scan(&newRows); err != nil {
		t.Fatal(err)
	}
	if newRows != 1 {
		t.Fatalf("second provision deleted new Skill revision")
	}
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM "`+schema+`".skills WHERE id='legacy-skill') +
		(SELECT count(*) FROM "`+schema+`".skill_versions WHERE id='legacy-version') +
		(SELECT count(*) FROM "`+schema+`".agent_skill_links WHERE agent_id='legacy-agent' AND skill_id='legacy-skill') +
		(SELECT count(*) FROM "`+schema+`".skill_test_cases WHERE id='legacy-case') +
		(SELECT count(*) FROM "`+schema+`".skill_eval_runs WHERE id='legacy-run')`).Scan(&legacyRowsAfterSecondProvision); err != nil {
		t.Fatal(err)
	}
	if legacyRowsAfterSecondProvision != 5 {
		t.Fatalf("second provision deleted legacy Skill history: rows=%d", legacyRowsAfterSecondProvision)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=$1
		AND table_name IN ('agent_executions','agent_tool_traces','agent_trace_events')`, schema).Scan(&observationTablesAfterSecondProvision); err != nil {
		t.Fatal(err)
	}
	if observationTablesAfterSecondProvision != 0 {
		t.Fatalf("second provision recreated %d obsolete observation tables", observationTablesAfterSecondProvision)
	}
}

func TestProvisionTenantSchemaBackfillsWorkflowProductColumns(t *testing.T) {
	url := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("STRATUM_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	tenantID := fmt.Sprintf("tmp_workflow_product_%d", time.Now().UnixNano())
	schema := `tenant_` + tenantID
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
	legacy := `CREATE SCHEMA "` + schema + `";
		CREATE TABLE "` + schema + `".workflow_definitions (id UUID PRIMARY KEY);
		CREATE TABLE "` + schema + `".workflow_versions (id UUID PRIMARY KEY);
		CREATE TABLE "` + schema + `".workflow_runs (id UUID PRIMARY KEY);`
	if _, err := pool.Exec(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}

	var columns, indexes int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema=$1 AND ((table_name='workflow_definitions' AND column_name='draft_input_schema_json')
		OR (table_name='workflow_versions' AND column_name='input_schema_json')
		OR (table_name='workflow_runs' AND column_name='created_by'))`, schema).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname=$1
		AND indexname='idx_workflow_runs_created_by_created'`, schema).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if columns != 3 || indexes != 1 {
		t.Fatalf("workflow product backfill incomplete: columns=%d indexes=%d", columns, indexes)
	}
}

func TestProvisionTenantSchemaUpgradesOptimizationIdempotencyWithoutDataLoss(t *testing.T) {
	url := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("STRATUM_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	tenantID := fmt.Sprintf("tmp_optimization_upgrade_%d", time.Now().UnixNano())
	schema := "tenant_" + tenantID
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
	legacy := `CREATE SCHEMA "` + schema + `"; SET search_path = "` + schema + `", public;
		CREATE TABLE eval_suites (
		 id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT NOT NULL DEFAULT '',
		 active_revision_id TEXT, draft_revision_id TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE TABLE eval_suite_revisions (
		 id TEXT PRIMARY KEY, suite_id TEXT NOT NULL REFERENCES eval_suites(id), parent_id TEXT,
		 version_no INT, status TEXT NOT NULL DEFAULT 'draft', resource_kind TEXT NOT NULL,
		 created_by TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), published_at TIMESTAMPTZ
		);
		CREATE TABLE optimization_jobs (
		 id TEXT PRIMARY KEY, resource_kind TEXT NOT NULL, resource_id TEXT NOT NULL,
		 baseline_revision_id TEXT NOT NULL, suite_revision_id TEXT NOT NULL REFERENCES eval_suite_revisions(id),
		 status TEXT NOT NULL, search_space JSONB NOT NULL DEFAULT '{}', rewrite_config JSONB NOT NULL DEFAULT '{}',
		 error_message TEXT NOT NULL DEFAULT '', created_by TEXT NOT NULL DEFAULT '',
		 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), completed_at TIMESTAMPTZ
		);
		INSERT INTO eval_suites(id,name) VALUES ('suite','legacy');
		INSERT INTO eval_suite_revisions(id,suite_id,resource_kind) VALUES ('revision','suite','skill');
		INSERT INTO optimization_jobs(id,resource_kind,resource_id,baseline_revision_id,suite_revision_id,status)
		 VALUES ('legacy-job','skill','skill-1','revision-1','revision','succeeded');`
	if _, err := pool.Exec(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
			t.Fatal(err)
		}
	}
	var rows, columns, indexes int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM "`+schema+`".optimization_jobs WHERE id='legacy-job'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema=$1 AND table_name='optimization_jobs'
		AND column_name IN ('idempotency_key','request_fingerprint')`, schema).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname=$1
		AND indexname='idx_optimization_jobs_idempotency'`, schema).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || columns != 2 || indexes != 1 {
		t.Fatalf("rows=%d columns=%d indexes=%d", rows, columns, indexes)
	}
}

func TestProvisionTenantSchemaAddsFactSourceIdentityWithoutBackfillingLegacyFacts(t *testing.T) {
	url := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("STRATUM_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	tenantID := fmt.Sprintf("tmp_fact_source_%d", time.Now().UnixNano())
	schema := `tenant_` + tenantID
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO "`+schema+`".memory_facts (user_id,scope,content) VALUES ('legacy-user','user','legacy fact')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		DROP INDEX "`+schema+`".uq_memory_facts_source_user;
		DROP INDEX "`+schema+`".uq_memory_facts_source_agent;
		ALTER TABLE "`+schema+`".memory_facts DROP CONSTRAINT memory_facts_source_identity_complete;
		ALTER TABLE "`+schema+`".memory_facts DROP COLUMN source_message_id, DROP COLUMN source_task_id,
			DROP COLUMN source_ordinal, DROP COLUMN source_payload_hash`); err != nil {
		t.Fatal(err)
	}
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}

	var columns, indexes, legacyNulls int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name='memory_facts'
		AND column_name IN ('source_message_id','source_task_id','source_ordinal','source_payload_hash')`, schema).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname=$1
		AND indexname IN ('uq_memory_facts_source_user','uq_memory_facts_source_agent')`, schema).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM "`+schema+`".memory_facts WHERE content='legacy fact'
		AND source_message_id IS NULL AND source_task_id IS NULL AND source_ordinal IS NULL AND source_payload_hash IS NULL`).Scan(&legacyNulls); err != nil {
		t.Fatal(err)
	}
	if columns != 4 || indexes != 2 || legacyNulls != 1 {
		t.Fatalf("columns=%d indexes=%d legacy_null_rows=%d", columns, indexes, legacyNulls)
	}
}

func TestTenantSchemaUpgradePreservesSkillEvaluationRowsAcrossReprovision(t *testing.T) {
	url := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("STRATUM_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()); err != nil {
		t.Fatal(err)
	}

	tenantID := fmt.Sprintf("tmp_evaluation_upgrade_%d", time.Now().UnixNano())
	schema := `tenant_` + tenantID
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	legacy := `SET search_path = "` + schema + `", public;
		CREATE TABLE skills (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE);
		CREATE TABLE skill_versions (id TEXT PRIMARY KEY, skill_id TEXT, implementation JSONB);
		CREATE TABLE eval_suites (
		 id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT NOT NULL DEFAULT '',
		 active_revision_id TEXT, draft_revision_id TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE TABLE eval_suite_revisions (
		 id TEXT PRIMARY KEY, suite_id TEXT NOT NULL REFERENCES eval_suites(id) ON DELETE CASCADE,
		 parent_id TEXT REFERENCES eval_suite_revisions(id) ON DELETE SET NULL, version_no INT,
		 status TEXT NOT NULL DEFAULT 'draft', resource_kind TEXT NOT NULL, created_by TEXT NOT NULL DEFAULT '',
		 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), published_at TIMESTAMPTZ
		);
		CREATE TABLE evaluation_experiments (
		 id TEXT PRIMARY KEY, resource_kind TEXT NOT NULL, resource_id TEXT NOT NULL,
		 stable_revision_id TEXT NOT NULL, canary_revision_id TEXT NOT NULL,
		 suite_revision_id TEXT NOT NULL REFERENCES eval_suite_revisions(id) ON DELETE RESTRICT,
		 status TEXT NOT NULL, stage_percent INT NOT NULL DEFAULT 5, policy JSONB NOT NULL DEFAULT '{}',
		 decision_snapshot JSONB NOT NULL DEFAULT '{}', created_by TEXT NOT NULL DEFAULT '',
		 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		 completed_at TIMESTAMPTZ
		);
		INSERT INTO skills (id,name) VALUES ('skill-existing','existing');
		INSERT INTO skill_versions (id,skill_id,implementation) VALUES ('skill-version-existing','skill-existing','{}');
		INSERT INTO eval_suites (id,name) VALUES ('suite-existing','existing');
		INSERT INTO eval_suite_revisions (id,suite_id,resource_kind) VALUES ('suite-revision-existing','suite-existing','skill');
		INSERT INTO evaluation_experiments
		 (id,resource_kind,resource_id,stable_revision_id,canary_revision_id,suite_revision_id,status)
		 VALUES ('experiment-existing','skill','skill-existing','stable-existing','canary-existing',
		 'suite-revision-existing','running');`
	if _, err := pool.Exec(ctx, legacy); err != nil {
		t.Fatal(err)
	}

	for provision := 1; provision <= 2; provision++ {
		if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
			t.Fatalf("provision %d: %v", provision, err)
		}
	}

	var experimentRows, newTables, backfilledColumns int
	var stageBoundaryMatches bool
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM "`+schema+`".evaluation_experiments
		WHERE id='experiment-existing' AND resource_kind='skill'`).Scan(&experimentRows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=$1
		AND table_name IN ('resource_revisions','experiment_decisions')`, schema).Scan(&newTables); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=$1
		AND table_name='evaluation_experiments'
		AND column_name IN ('state_version','recommendation','safety_stopped','stage_started_at')`, schema).Scan(&backfilledColumns); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT stage_started_at=updated_at FROM "`+schema+`".evaluation_experiments
		WHERE id='experiment-existing'`).Scan(&stageBoundaryMatches); err != nil {
		t.Fatal(err)
	}
	if experimentRows != 1 || newTables != 2 || backfilledColumns != 4 || !stageBoundaryMatches {
		t.Fatalf("experiment_rows=%d new_tables=%d backfilled_columns=%d stage_boundary_matches=%t",
			experimentRows, newTables, backfilledColumns, stageBoundaryMatches)
	}
}
