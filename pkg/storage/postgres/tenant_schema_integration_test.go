package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

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
		AND column_name IN ('state_version','recommendation','safety_stopped')`, schema).Scan(&backfilledColumns); err != nil {
		t.Fatal(err)
	}
	if experimentRows != 1 || newTables != 2 || backfilledColumns != 3 {
		t.Fatalf("experiment_rows=%d new_tables=%d backfilled_columns=%d",
			experimentRows, newTables, backfilledColumns)
	}
}
