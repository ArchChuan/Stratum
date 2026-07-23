package postgres

import (
	"os"
	"strings"
	"testing"
)

func TestTenantSchemaQuarantinesUnmappedKnowledgeChunksWithoutDeletingThem(t *testing.T) {
	ddl, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(ddl)
	if strings.Contains(text, "DELETE FROM knowledge_chunks WHERE workspace_id IS NULL") {
		t.Fatal("tenant startup DDL still deletes unmapped knowledge chunks")
	}
	if !strings.Contains(text, "knowledge_chunks_quarantine") {
		t.Fatal("tenant startup DDL does not preserve unmapped chunks in quarantine")
	}
}

func TestTenantSchemaRevisionAndDecisionSafetyAvoidsPlaintextPayloads(t *testing.T) {
	ddl, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(ddl)

	for _, table := range []string{"resource_revisions", "experiment_decisions"} {
		start := strings.Index(text, "CREATE TABLE IF NOT EXISTS "+table)
		if start == -1 {
			t.Fatalf("tenant schema missing %s", table)
		}
		end := strings.Index(text[start:], ");")
		if end == -1 {
			t.Fatalf("tenant schema has unterminated %s DDL", table)
		}
		body := strings.ToLower(text[start : start+end])
		if strings.Contains(body, "payload jsonb") || strings.Contains(body, "payload_json jsonb") {
			t.Fatalf("%s must not store plaintext payload JSONB", table)
		}
	}

	for _, table := range []string{
		"skills",
		"skill_versions",
		"skill_test_cases",
		"skill_eval_runs",
		"agent_skill_links",
		"eval_suites",
		"eval_suite_revisions",
		"eval_runs",
		"evaluation_experiments",
		"evaluation_deployments",
		"evaluation_feedback",
	} {
		if strings.Contains(text, "DROP TABLE IF EXISTS "+table) {
			t.Fatalf("tenant upgrade must not drop existing Skill evaluation table %s", table)
		}
	}
}
