package postgres_test

import (
	"os"
	"strings"
	"testing"
)

func TestTenantSchemaContainsVersionedSkillTables(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)

	required := []string{
		"ALTER TABLE skills ADD COLUMN IF NOT EXISTS active_version_id TEXT",
		"ALTER TABLE skills ADD COLUMN IF NOT EXISTS draft_version_id TEXT",
		"CREATE TABLE IF NOT EXISTS skill_versions",
		"CREATE TABLE IF NOT EXISTS skill_test_cases",
		"CREATE TABLE IF NOT EXISTS skill_eval_runs",
		"skill_id        TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE",
		"tool_contract   JSONB NOT NULL DEFAULT '{}'",
		"implementation  JSONB NOT NULL DEFAULT '{}'",
	}
	for _, want := range required {
		if !strings.Contains(sql, want) {
			t.Fatalf("tenant_schema.sql missing %q", want)
		}
	}
}

func TestTenantSchemaContainsEvaluationControlPlane(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)

	required := []string{
		"ALTER TABLE skill_versions ADD COLUMN IF NOT EXISTS parent_version_id TEXT",
		"ALTER TABLE skill_versions ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'manual'",
		"ALTER TABLE skill_versions ADD COLUMN IF NOT EXISTS content_hash TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE skill_versions ADD COLUMN IF NOT EXISTS generation_metadata JSONB NOT NULL DEFAULT '{}'",
		"CREATE TABLE IF NOT EXISTS eval_suites",
		"CREATE TABLE IF NOT EXISTS eval_suite_revisions",
		"CREATE TABLE IF NOT EXISTS eval_cases",
		"CREATE TABLE IF NOT EXISTS eval_runs",
		"CREATE TABLE IF NOT EXISTS eval_case_results",
		"CREATE TABLE IF NOT EXISTS optimization_jobs",
		"CREATE TABLE IF NOT EXISTS optimization_candidates",
		"CREATE TABLE IF NOT EXISTS evaluation_experiments",
		"CREATE TABLE IF NOT EXISTS evaluation_deployments",
		"CREATE TABLE IF NOT EXISTS evaluation_feedback",
		"CREATE TABLE IF NOT EXISTS evaluation_jobs",
	}
	for _, want := range required {
		if !strings.Contains(sql, want) {
			t.Fatalf("tenant_schema.sql missing evaluation control-plane DDL %q", want)
		}
	}
}

func TestTenantSchemaBackfillsTraceIDBeforeTraceIndex(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)

	addTraceIDAt := strings.Index(sql, "ALTER TABLE agent_executions ADD COLUMN IF NOT EXISTS trace_id")
	createTraceIndexAt := strings.Index(sql, "CREATE INDEX IF NOT EXISTS idx_agent_exec_trace")
	if addTraceIDAt == -1 {
		t.Fatal("tenant_schema.sql must backfill agent_executions.trace_id for existing tenant tables")
	}
	if createTraceIndexAt == -1 {
		t.Fatal("tenant_schema.sql must create idx_agent_exec_trace")
	}
	if addTraceIDAt > createTraceIndexAt {
		t.Fatalf("agent_executions.trace_id backfill must run before idx_agent_exec_trace: add=%d index=%d",
			addTraceIDAt, createTraceIndexAt)
	}
}

func TestTenantSchemaBackfillsGenericTraceObservationColumns(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)

	required := []string{
		"ALTER TABLE agent_tool_traces ADD COLUMN IF NOT EXISTS provider_type TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE agent_tool_traces ADD COLUMN IF NOT EXISTS provider_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE agent_tool_traces ADD COLUMN IF NOT EXISTS server_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE agent_tool_traces ADD COLUMN IF NOT EXISTS capability_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE agent_tool_traces ADD COLUMN IF NOT EXISTS metadata_json JSONB NOT NULL DEFAULT '{}'",
		"ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS run_type TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS observation_type TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS provider_type TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS provider_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS sequence_no BIGINT NOT NULL DEFAULT 0",
		"ALTER TABLE agent_trace_events ADD COLUMN IF NOT EXISTS metadata_json JSONB NOT NULL DEFAULT '{}'",
	}
	for _, want := range required {
		if !strings.Contains(sql, want) {
			t.Fatalf("tenant_schema.sql missing generic trace backfill %q", want)
		}
	}
}

// knowledge_chunks.tsv must segment Chinese via public.chinese_zh, not the
// default 'simple' parser which cannot tokenize CJK text (near-zero recall).
func TestTenantSchemaChunksUseChineseTSVConfig(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)

	if !strings.Contains(sql,
		"GENERATED ALWAYS AS (to_tsvector('public.chinese_zh', content)) STORED") {
		t.Fatal("knowledge_chunks.tsv must be generated from to_tsvector('public.chinese_zh', content)")
	}
	if strings.Contains(sql, "to_tsvector('simple'") {
		t.Fatal("tenant_schema.sql must not create tsv columns with the 'simple' config")
	}
}

// A GENERATED column's expression cannot be ALTERed in place, so historical
// tenants whose tsv was built with 'simple' need a drop+recreate migration.
// That DO block must run AFTER knowledge_chunks is created, and its guard must
// key off the chinese_zh reference so the rebuild is idempotent.
func TestTenantSchemaMigratesLegacyTSVAfterTableCreate(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)

	createChunksAt := strings.Index(sql, "CREATE TABLE IF NOT EXISTS knowledge_chunks")
	migrateGuardAt := strings.Index(sql, "position('chinese_zh' IN gen_expr) = 0")
	if createChunksAt == -1 {
		t.Fatal("tenant_schema.sql must create knowledge_chunks")
	}
	if migrateGuardAt == -1 {
		t.Fatal("tenant_schema.sql must migrate legacy tsv columns guarded by a chinese_zh check")
	}
	if migrateGuardAt < createChunksAt {
		t.Fatalf("legacy tsv migration must run after knowledge_chunks is created: create=%d migrate=%d",
			createChunksAt, migrateGuardAt)
	}
	if !strings.Contains(sql, "CREATE INDEX idx_kc_tsv ON knowledge_chunks USING GIN(tsv)") {
		t.Fatal("legacy tsv migration must rebuild the idx_kc_tsv GIN index")
	}
}
