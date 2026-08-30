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

func TestTenantSchemaAuditProjectionsAreCredentialFreeColumns(t *testing.T) {
	ddl, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(ddl)
	start := strings.Index(text, "CREATE TABLE IF NOT EXISTS resource_change_audits")
	if start == -1 {
		t.Fatal("tenant schema missing resource_change_audits")
	}
	end := strings.Index(text[start:], ");")
	if end == -1 {
		t.Fatal("tenant schema has unterminated resource_change_audits DDL")
	}
	body := strings.ToLower(text[start : start+end])
	for _, col := range []string{"before_projection", "after_projection", "actor_type", "source", "proposal_id"} {
		if !strings.Contains(body, col+" ") && !strings.Contains(body, col+"\n") {
			t.Fatalf("resource_change_audits missing %s column", col)
		}
	}
	// Projections are marshalled from Go safe projections; the DDL must not
	// hint at storing raw credential-bearing config blobs.
	for _, forbidden := range []string{"auth_config", "headers", "\"env\""} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("resource_change_audits DDL references credential-bearing field %q", forbidden)
		}
	}
}

func TestTenantSchemaWorkflowEditorsAndCreatedBy(t *testing.T) {
	ddl, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(ddl)

	// workflow_definitions 必须带 created_by（所有权矩阵 creator 语义的存储基线）。
	if !strings.Contains(text, "workflow_definitions") {
		t.Fatal("tenant schema missing workflow_definitions")
	}
	if !strings.Contains(text, "created_by TEXT NOT NULL DEFAULT ''") {
		t.Fatal("workflow_definitions must carry created_by TEXT NOT NULL DEFAULT ''")
	}
	// 幂等 ALTER 用于升级历史租户。
	if !strings.Contains(text, "ALTER TABLE workflow_definitions ADD COLUMN IF NOT EXISTS created_by TEXT NOT NULL DEFAULT '';") {
		t.Fatal("workflow_definitions must idempotently add created_by for historical tenants")
	}
	// resource_editors 注释声明 workflow kind（可申请编辑权的新资源类型）。
	if !strings.Contains(text, "agent|skill|mcp|knowledge|workflow") {
		t.Fatal("resource_editors kind comment must include workflow")
	}
}
