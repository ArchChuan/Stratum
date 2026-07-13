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
