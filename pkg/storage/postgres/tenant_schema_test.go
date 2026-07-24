package postgres_test

import (
	"os"
	"strings"
	"testing"
)

func TestTenantSchemaContainsSystemAssistantIdentityAndSeed(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	createAt := strings.Index(sql, "CREATE TABLE IF NOT EXISTS agents")
	columnAt := strings.Index(sql, "ALTER TABLE agents ADD COLUMN IF NOT EXISTS system_key TEXT")
	indexAt := strings.Index(sql, "CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_system_key")
	seedAt := strings.Index(sql, "'stratum.platform_assistant'")
	if createAt == -1 || columnAt == -1 || indexAt == -1 || seedAt == -1 {
		t.Fatalf("tenant schema missing managed assistant DDL: create=%d column=%d index=%d seed=%d",
			createAt, columnAt, indexAt, seedAt)
	}
	if createAt >= columnAt || columnAt >= indexAt || indexAt >= seedAt {
		t.Fatalf("managed assistant DDL must follow create/alter/index/seed order: create=%d column=%d index=%d seed=%d",
			createAt, columnAt, indexAt, seedAt)
	}
	for _, want := range []string{
		"ON agents(system_key) WHERE system_key IS NOT NULL",
		"'stratum-platform-assistant'",
		"'__stratum_platform_assistant__'",
		"WHILE EXISTS",
		"'基于官方资料指导平台使用并诊断当前租户应用状态'",
		"'', '', '', 10, 8000, 'user', 'stratum.platform_assistant'",
		"ON CONFLICT (id) DO NOTHING",
		"stratum platform assistant identity conflict requires operator action",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("tenant schema missing managed assistant contract %q", want)
		}
	}
}

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

func TestTenantSchemaUpgradePreservesLegacySkillHistory(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)

	for _, forbidden := range []string{
		"DROP TABLE IF EXISTS agent_skill_links",
		"DROP TABLE IF EXISTS skill_eval_runs",
		"DROP TABLE IF EXISTS skill_test_cases",
		"DROP TABLE IF EXISTS skill_versions",
		"DROP TABLE IF EXISTS skills",
	} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("tenant provisioning must preserve legacy Skill history: %q", forbidden)
		}
	}

	for _, backfill := range []string{
		"ALTER TABLE skills ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE skills ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'draft'",
		"ALTER TABLE skills ADD COLUMN IF NOT EXISTS active_revision_id TEXT",
		"ALTER TABLE skills ADD COLUMN IF NOT EXISTS draft_revision_id TEXT",
		"ALTER TABLE skills ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()",
		"ALTER TABLE agent_skill_links ADD COLUMN IF NOT EXISTS revision_id TEXT",
	} {
		if !strings.Contains(sql, backfill) {
			t.Fatalf("tenant_schema.sql missing additive legacy Skill upgrade %q", backfill)
		}
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

func TestTenantSchemaUpgradePreservesSkillEvaluationDataAndHistoricalAgentTraces(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)

	for _, forbidden := range []string{
		"DELETE FROM evaluation_deployments WHERE resource_kind = 'skill'",
		"DELETE FROM evaluation_experiments WHERE resource_kind = 'skill'",
		"DELETE FROM optimization_jobs WHERE resource_kind = 'skill'",
		"DELETE FROM eval_runs WHERE resource_kind = 'skill'",
		"DELETE FROM evaluation_feedback WHERE resource_kind = 'skill'",
		"DELETE FROM evaluation_jobs WHERE payload->>'resource_kind' = 'skill'",
		"DELETE FROM eval_suite_revisions WHERE resource_kind = 'skill'",
	} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("tenant reprovisioning must preserve Skill evaluation data: %q", forbidden)
		}
	}

	if strings.Contains(sql, "DELETE FROM agent_tool_traces WHERE provider_type = 'skill'") ||
		strings.Contains(sql, "DELETE FROM agent_trace_events WHERE provider_type = 'skill'") {
		t.Fatal("historical Agent Skill traces must be retained as immutable audit records")
	}
}

func TestTenantSchemaContainsResourceRevisionAndDecisionDDL(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)

	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS resource_revisions",
		"UNIQUE (resource_kind, resource_id, id)",
		"CHECK (resource_kind IN ('skill', 'agent', 'mcp', 'knowledge'))",
		"CHECK (source IN ('manual', 'optimization', 'rollback'))",
		"CHECK (status IN ('draft', 'published'))",
		"content_hash",
		"payload_hash",
		"payload_ref",
		"safe_summary",
		"idempotency_key",
		"CREATE TABLE IF NOT EXISTS experiment_decisions",
		"experiment_id",
		"actor_type",
		"actor_id",
		"prior_status",
		"new_status",
		"metrics",
		"reason",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("tenant_schema.sql missing revision/decision DDL %q", want)
		}
	}
}

func TestTenantSchemaUpgradeBackfillsExperimentStateBeforeDependentDDL(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)

	backfills := []string{
		"ALTER TABLE evaluation_experiments ADD COLUMN IF NOT EXISTS state_version BIGINT NOT NULL DEFAULT 1",
		"ALTER TABLE evaluation_experiments ADD COLUMN IF NOT EXISTS recommendation TEXT NOT NULL DEFAULT 'hold'",
		"ALTER TABLE evaluation_experiments ADD COLUMN IF NOT EXISTS safety_stopped BOOL NOT NULL DEFAULT false",
	}
	lastBackfill := -1
	for _, statement := range backfills {
		at := strings.Index(sql, statement)
		if at == -1 {
			t.Fatalf("tenant_schema.sql missing experiment backfill %q", statement)
		}
		if at < lastBackfill {
			t.Fatalf("experiment backfills are out of order: %q", statement)
		}
		lastBackfill = at
	}

	for _, dependent := range []string{
		"CREATE INDEX IF NOT EXISTS idx_evaluation_experiments_resource",
		"CREATE TABLE IF NOT EXISTS experiment_decisions",
	} {
		at := strings.Index(sql, dependent)
		if at == -1 {
			t.Fatalf("tenant_schema.sql missing dependent DDL %q", dependent)
		}
		if at < lastBackfill {
			t.Fatalf("dependent DDL %q must follow experiment backfills", dependent)
		}
	}
}

func TestTenantSchemaUpgradeBackfillsOptimizationIdempotencyBeforeUniqueIndex(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	keyBackfill := strings.Index(sql,
		"ALTER TABLE optimization_jobs ADD COLUMN IF NOT EXISTS idempotency_key TEXT NOT NULL DEFAULT ''")
	fingerprintBackfill := strings.Index(sql,
		"ALTER TABLE optimization_jobs ADD COLUMN IF NOT EXISTS request_fingerprint TEXT NOT NULL DEFAULT ''")
	index := strings.Index(sql, "CREATE UNIQUE INDEX IF NOT EXISTS idx_optimization_jobs_idempotency")
	if keyBackfill == -1 || fingerprintBackfill == -1 || index == -1 {
		t.Fatal("tenant_schema.sql missing optimization idempotency upgrade DDL")
	}
	if index < keyBackfill || index < fingerprintBackfill {
		t.Fatal("optimization idempotency index must follow historical column backfills")
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
		"arguments_digest  TEXT        NOT NULL DEFAULT ''",
		"ALTER TABLE agent_tool_approvals ADD COLUMN IF NOT EXISTS decision_id",
		"ALTER TABLE agent_tool_approvals ADD COLUMN IF NOT EXISTS arguments_digest",
		"ALTER TABLE agent_tool_approvals ADD COLUMN IF NOT EXISTS skill_revisions_digest",
		"ALTER TABLE agent_tool_approvals ADD COLUMN IF NOT EXISTS policy_version",
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

func TestTenantSchemaUpgradesToolApprovalStatusForUnknownOutcome(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	drop := "ALTER TABLE agent_tool_approvals DROP CONSTRAINT IF EXISTS agent_tool_approvals_status_check"
	add := "CHECK (status IN ('pending', 'approved', 'rejected', 'expired', 'executing', 'executed', 'unknown_outcome'))"
	dropAt, addAt := strings.Index(sql, drop), strings.LastIndex(sql, add)
	if dropAt == -1 || addAt == -1 {
		t.Fatalf("tenant schema must rebuild approval status constraint for unknown outcomes")
	}
	if dropAt > addAt {
		t.Fatalf("approval status constraint must be dropped before it is rebuilt")
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

func TestTenantSchemaIndexesEvaluationCenterCandidateQueriesAfterTables(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	for table, index := range map[string]string{
		"CREATE TABLE IF NOT EXISTS optimization_jobs":       "CREATE INDEX IF NOT EXISTS idx_optimization_jobs_center_query",
		"CREATE TABLE IF NOT EXISTS optimization_candidates": "CREATE INDEX IF NOT EXISTS idx_optimization_candidates_job_created",
	} {
		tableAt, indexAt := strings.Index(sql, table), strings.Index(sql, index)
		if indexAt == -1 {
			t.Fatalf("tenant_schema.sql missing evaluation center index %q", index)
		}
		if tableAt == -1 || indexAt < tableAt {
			t.Fatalf("evaluation center index %q must follow %q", index, table)
		}
	}
}

func TestTenantSchemaDropsObsoleteAgentObservationTables(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	for _, table := range []string{"agent_executions", "agent_tool_traces", "agent_trace_events"} {
		if !strings.Contains(sql, "DROP TABLE IF EXISTS "+table+";") {
			t.Fatalf("tenant_schema.sql must drop obsolete table %s", table)
		}
		if strings.Contains(sql, "CREATE TABLE IF NOT EXISTS "+table) {
			t.Fatalf("tenant_schema.sql must not recreate obsolete table %s", table)
		}
		if strings.Contains(sql, "ALTER TABLE "+table) {
			t.Fatalf("tenant_schema.sql must not alter obsolete table %s after dropping it", table)
		}
	}
}

func TestTenantSchemaContainsWorkflowStage1ATablesAndBackfills(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	required := []string{
		"CREATE TABLE IF NOT EXISTS workflow_definitions",
		"CREATE TABLE IF NOT EXISTS workflow_versions",
		"CREATE TABLE IF NOT EXISTS workflow_runs",
		"CREATE TABLE IF NOT EXISTS workflow_node_attempts",
		"ALTER TABLE workflow_definitions ADD COLUMN IF NOT EXISTS draft_revision",
		"ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS request_hash",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_workflow_runs_idempotency",
		"CHECK (status IN ('queued', 'running', 'pause_requested', 'paused', 'cancel_requested', 'canceled', 'manual_intervention', 'completed', 'failed'))",
		"ALTER TABLE workflow_runs ADD CONSTRAINT workflow_runs_status_check",
		"ALTER TABLE workflow_node_attempts ADD CONSTRAINT workflow_node_attempts_status_check",
	}
	for _, want := range required {
		if !strings.Contains(sql, want) {
			t.Fatalf("tenant_schema.sql missing workflow Stage 1A DDL %q", want)
		}
	}
}

func TestTenantSchemaBackfillsWorkflowProductColumnsBeforeIndexes(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	definitionsAt := strings.Index(sql, "CREATE TABLE IF NOT EXISTS workflow_definitions")
	versionsAt := strings.Index(sql, "CREATE TABLE IF NOT EXISTS workflow_versions")
	runsAt := strings.Index(sql, "CREATE TABLE IF NOT EXISTS workflow_runs")
	for _, check := range []struct {
		fragment string
		createAt int
	}{
		{fragment: "\n    draft_input_schema_json JSONB NOT NULL DEFAULT '{\"task_label\":\"任务\",\"fields\":[]}'", createAt: definitionsAt},
		{fragment: "\n    input_schema_json JSONB     NOT NULL DEFAULT '{\"task_label\":\"任务\",\"fields\":[]}'", createAt: versionsAt},
		{fragment: "\n    created_by       TEXT        NOT NULL DEFAULT ''", createAt: runsAt},
		{fragment: `ALTER TABLE workflow_definitions ADD COLUMN IF NOT EXISTS draft_input_schema_json JSONB NOT NULL DEFAULT '{"task_label":"任务","fields":[]}'`, createAt: definitionsAt},
		{fragment: `ALTER TABLE workflow_versions ADD COLUMN IF NOT EXISTS input_schema_json JSONB NOT NULL DEFAULT '{"task_label":"任务","fields":[]}'`, createAt: versionsAt},
		{fragment: "ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS created_by TEXT NOT NULL DEFAULT ''", createAt: runsAt},
	} {
		at := strings.Index(sql, check.fragment)
		if at == -1 {
			t.Fatalf("tenant schema missing workflow product DDL %q", check.fragment)
		}
		if at < check.createAt {
			t.Fatalf("workflow product backfill must follow table creation: %q", check.fragment)
		}
	}
	backfillAt := strings.Index(sql, "ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS created_by")
	indexAt := strings.Index(sql, "CREATE INDEX IF NOT EXISTS idx_workflow_runs_created_by_created")
	if indexAt == -1 || indexAt < backfillAt {
		t.Fatalf("workflow ownership index must follow created_by backfill: backfill=%d index=%d", backfillAt, indexAt)
	}
}

func TestTenantSchemaContainsWorkflowDurableRuntime(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	for _, fragment := range []string{
		"generation", "scheduler_owner", "lease_expires_at", "pause_reason",
		"run_generation", "lease_owner", "fence_token", "retry_at", "effect_class",
		"CREATE TABLE IF NOT EXISTS workflow_events", "UNIQUE (run_id, sequence_no)",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("tenant schema missing durable workflow fragment %q", fragment)
		}
	}
}

func TestTenantSchemaContainsWorkflowStage1BEventAndAttemptBackfills(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	for _, fragment := range []string{
		"ALTER TABLE workflow_node_attempts ADD COLUMN IF NOT EXISTS error_code",
		"ALTER TABLE workflow_events ADD COLUMN IF NOT EXISTS sequence_no",
		"ALTER TABLE workflow_events ADD COLUMN IF NOT EXISTS payload_json",
		"CREATE INDEX IF NOT EXISTS idx_workflow_events_cursor",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("tenant schema missing workflow Stage 1B backfill %q", fragment)
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

func TestTenantSchemaBackfillsChatMessageArtifactsAfterTableCreate(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	createAt := strings.Index(sql, "CREATE TABLE IF NOT EXISTS chat_messages")
	backfillAt := strings.Index(sql, "ALTER TABLE chat_messages\n    ADD COLUMN IF NOT EXISTS artifacts_json JSONB NOT NULL DEFAULT '[]'")
	if createAt == -1 || backfillAt == -1 {
		t.Fatal("tenant schema must create chat_messages and backfill artifacts_json")
	}
	if backfillAt < createAt {
		t.Fatal("artifacts_json backfill must follow chat_messages creation for historical tenants")
	}
}
