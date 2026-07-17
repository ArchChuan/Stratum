package postgres_test

import (
	"os"
	"strings"
	"testing"
)

func TestTenantSchemaBackfillsStructuredMemoryFacts(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	createAt := strings.Index(sql, "CREATE TABLE IF NOT EXISTS memory_facts")
	for _, want := range []string{
		"category        TEXT NOT NULL DEFAULT 'other'",
		"confidence      FLOAT8 NOT NULL DEFAULT 0.5",
		"source          TEXT NOT NULL DEFAULT 'llm_extraction'",
		"ALTER TABLE memory_facts ADD COLUMN IF NOT EXISTS category",
		"ALTER TABLE memory_facts ADD COLUMN IF NOT EXISTS confidence",
		"ALTER TABLE memory_facts ADD COLUMN IF NOT EXISTS source",
	} {
		at := strings.Index(sql, want)
		if at == -1 {
			t.Fatalf("tenant_schema.sql missing structured fact DDL %q", want)
		}
		if strings.HasPrefix(want, "ALTER TABLE") && at < createAt {
			t.Fatalf("structured fact backfill must follow table creation: %q", want)
		}
	}
}

func TestStructuredMemoryFactsMigrationMarkerIsPaired(t *testing.T) {
	for _, path := range []string{
		"../../migration/sql/020_memory_facts_quality.up.sql",
		"../../migration/sql/020_memory_facts_quality.down.sql",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read marker %s: %v", path, err)
		}
		if !strings.Contains(string(data), "tenant_schema.sql") {
			t.Fatalf("marker %s must identify tenant_schema.sql as canonical DDL", path)
		}
	}
}

func TestTenantSchemaContainsIdempotentActiveSnapshotDDL(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	createAt := strings.Index(sql, "CREATE TABLE IF NOT EXISTS memory_active_snapshots")
	if createAt == -1 {
		t.Fatal("tenant schema missing active snapshot table")
	}
	for _, want := range []string{
		"work_context     TEXT[] NOT NULL DEFAULT '{}'",
		"personal_context TEXT[] NOT NULL DEFAULT '{}'",
		"top_of_mind      TEXT[] NOT NULL DEFAULT '{}'",
		"source           JSONB NOT NULL DEFAULT '{}'",
		"expires_at       TIMESTAMPTZ NOT NULL",
		"version          BIGINT NOT NULL DEFAULT 1",
		"status           TEXT NOT NULL DEFAULT 'active'",
		"UNIQUE (user_id, agent_id)",
		"ALTER TABLE memory_active_snapshots ADD COLUMN IF NOT EXISTS work_context",
		"CREATE INDEX IF NOT EXISTS idx_memory_active_snapshots_scope_expiry",
	} {
		at := strings.Index(sql, want)
		if at == -1 {
			t.Fatalf("tenant schema missing active snapshot DDL %q", want)
		}
		if strings.HasPrefix(want, "ALTER TABLE") && at < createAt {
			t.Fatalf("active snapshot backfill must follow table creation: %q", want)
		}
	}
}

func TestActiveSnapshotMigrationMarkerIsPairedAndTenantOnly(t *testing.T) {
	for _, path := range []string{
		"../../migration/sql/021_memory_active_snapshots.up.sql",
		"../../migration/sql/021_memory_active_snapshots.down.sql",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read marker %s: %v", path, err)
		}
		text := string(data)
		if !strings.Contains(text, "tenant_schema.sql") {
			t.Fatalf("marker %s must identify tenant_schema.sql as canonical DDL", path)
		}
		if strings.Contains(strings.ToUpper(text), "CREATE TABLE") {
			t.Fatalf("marker %s must not duplicate tenant DDL", path)
		}
	}
}

func TestTenantSchemaEvolvesMemorySummariesForTieredHistory(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	for _, want := range []string{
		"tier TEXT NOT NULL DEFAULT 'recent_months'",
		"period_start TIMESTAMPTZ",
		"period_end TIMESTAMPTZ",
		"source_start TEXT NOT NULL DEFAULT ''",
		"source_end TEXT NOT NULL DEFAULT ''",
		"aggregation_key TEXT",
		"importance FLOAT8 NOT NULL DEFAULT 0.5",
		"confidence FLOAT8 NOT NULL DEFAULT 0.5",
		"status TEXT NOT NULL DEFAULT 'active'",
		"updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()",
		"source_ids UUID[]",
		"ALTER TABLE memory_summaries ADD COLUMN IF NOT EXISTS source_ids UUID[]",
		"ALTER TABLE memory_summaries ADD COLUMN IF NOT EXISTS tier",
		"CREATE UNIQUE INDEX IF NOT EXISTS uq_memory_summaries_aggregation_key",
		"idx_memory_summaries_history_scope",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("tenant schema missing History DDL %q", want)
		}
	}
}

func TestHistorySourceIDsMigrationMarkerIsPairedAndTenantOnly(t *testing.T) {
	for _, path := range []string{
		"../../migration/sql/023_memory_history_source_ids.up.sql",
		"../../migration/sql/023_memory_history_source_ids.down.sql",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read marker %s: %v", path, err)
		}
		text := string(data)
		if !strings.Contains(text, "tenant_schema.sql") || strings.Contains(strings.ToUpper(text), "ALTER TABLE") || strings.Contains(strings.ToUpper(text), "CREATE TABLE") {
			t.Fatalf("marker %s must be marker-only tenant DDL guidance", path)
		}
	}
}

func TestHistoryMigrationMarkerIsPairedAndTenantOnly(t *testing.T) {
	for _, path := range []string{
		"../../migration/sql/022_memory_history_tiers.up.sql",
		"../../migration/sql/022_memory_history_tiers.down.sql",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read marker %s: %v", path, err)
		}
		text := string(data)
		if !strings.Contains(text, "tenant_schema.sql") || strings.Contains(strings.ToUpper(text), "CREATE TABLE") {
			t.Fatalf("marker %s must be tenant-only comment marker", path)
		}
	}
}

func TestTenantSchemaPreservesMemoryEntrySessionRouting(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)

	if strings.Contains(sql, "ALTER TABLE IF EXISTS memory_entries DROP COLUMN IF EXISTS session_id") {
		t.Fatal("tenant reprovisioning must not drop memory_entries.session_id while the repository uses it")
	}
	if !strings.Contains(sql, "session_id   TEXT") {
		t.Fatal("canonical memory_entries schema must include session_id")
	}
	if !strings.Contains(sql, "ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS session_id TEXT") {
		t.Fatal("historical tenant schemas must backfill memory_entries.session_id")
	}
}

func TestTenantSchemaSupportsOwnedMemoryLifecycleCleanup(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)

	for _, want := range []string{
		"ALTER TABLE memory_outbox ADD COLUMN IF NOT EXISTS user_id TEXT",
		"ALTER TABLE memory_outbox ADD COLUMN IF NOT EXISTS agent_id TEXT",
		"CREATE INDEX IF NOT EXISTS idx_memory_outbox_user_id",
		"CREATE INDEX IF NOT EXISTS idx_memory_outbox_agent_id",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("tenant schema missing lifecycle ownership DDL %q", want)
		}
	}
}

func TestTenantSchemaResetsLegacyExecutableSkillsBeforeCreatingInstructionSkills(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)

	legacyReset := []string{
		"column_name = 'implementation'",
		"DROP TABLE IF EXISTS agent_skill_links",
		"DROP TABLE IF EXISTS skill_eval_runs",
		"DROP TABLE IF EXISTS skill_test_cases",
		"DROP TABLE IF EXISTS skill_versions",
		"DROP TABLE IF EXISTS skills",
	}
	for _, want := range legacyReset {
		if !strings.Contains(sql, want) {
			t.Fatalf("tenant_schema.sql missing legacy Skill reset %q", want)
		}
	}

	resetAt := strings.Index(sql, "column_name = 'implementation'")
	createAt := strings.Index(sql, "CREATE TABLE IF NOT EXISTS skills")
	if resetAt == -1 || createAt == -1 || resetAt > createAt {
		t.Fatalf("legacy Skill reset must precede new Skill schema: reset=%d create=%d", resetAt, createAt)
	}
}

func TestTenantSchemaContainsInstructionSkillTables(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)

	required := []string{
		"CREATE TABLE IF NOT EXISTS skills",
		"active_revision_id TEXT",
		"draft_revision_id  TEXT",
		"CREATE TABLE IF NOT EXISTS skill_revisions",
		"activation_contract JSONB NOT NULL DEFAULT '{}'",
		"instructions        TEXT NOT NULL DEFAULT ''",
		"requirements        JSONB NOT NULL DEFAULT '{}'",
		"CREATE TABLE IF NOT EXISTS agent_skill_links",
		"revision_id TEXT REFERENCES skill_revisions(id) ON DELETE SET NULL",
	}
	for _, want := range required {
		if !strings.Contains(sql, want) {
			t.Fatalf("tenant_schema.sql missing instruction Skill DDL %q", want)
		}
	}

	for _, forbidden := range []string{
		"CREATE TABLE IF NOT EXISTS skill_versions",
		"CREATE TABLE IF NOT EXISTS skill_test_cases",
		"CREATE TABLE IF NOT EXISTS skill_eval_runs",
		"tool_contract   JSONB",
		"implementation  JSONB",
	} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("tenant_schema.sql still creates legacy executable Skill storage %q", forbidden)
		}
	}
}

func TestTenantSchemaPurgesSkillEvaluationDataButKeepsHistoricalAgentTraces(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)

	for _, want := range []string{
		"DELETE FROM evaluation_deployments WHERE resource_kind = 'skill'",
		"DELETE FROM evaluation_experiments WHERE resource_kind = 'skill'",
		"DELETE FROM optimization_jobs WHERE resource_kind = 'skill'",
		"DELETE FROM eval_runs WHERE resource_kind = 'skill'",
		"DELETE FROM evaluation_feedback WHERE resource_kind = 'skill'",
		"DELETE FROM evaluation_jobs WHERE payload->>'resource_kind' = 'skill'",
		"DELETE FROM eval_suite_revisions WHERE resource_kind = 'skill'",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("tenant_schema.sql missing Skill evaluation purge %q", want)
		}
	}

	if strings.Contains(sql, "DELETE FROM agent_tool_traces WHERE provider_type = 'skill'") ||
		strings.Contains(sql, "DELETE FROM agent_trace_events WHERE provider_type = 'skill'") {
		t.Fatal("historical Agent Skill traces must be retained as immutable audit records")
	}
}

func TestTenantSchemaContainsMCPToolPolicyAndEncryptedApprovals(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS mcp_tool_policies",
		"risk_level TEXT NOT NULL DEFAULT 'unclassified'",
		"CHECK (risk_level IN ('read', 'write_reversible', 'destructive', 'unclassified'))",
		"CREATE TABLE IF NOT EXISTS agent_tool_approvals",
		"encrypted_payload TEXT NOT NULL",
		"status TEXT NOT NULL DEFAULT 'pending'",
		"'waiting_approval'",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("tenant schema missing %q", want)
		}
	}
	approvalAt := strings.Index(sql, "CREATE TABLE IF NOT EXISTS agent_tool_approvals")
	approvalEnd := strings.Index(sql[approvalAt:], ");")
	if approvalAt >= 0 && approvalEnd >= 0 && strings.Contains(sql[approvalAt:approvalAt+approvalEnd], "arguments_json") {
		t.Fatal("approval payload must not be stored as plaintext JSONB")
	}
}

func TestTenantSchemaContainsEvaluationControlPlane(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)

	required := []string{
		"CREATE TABLE IF NOT EXISTS skill_revisions",
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
