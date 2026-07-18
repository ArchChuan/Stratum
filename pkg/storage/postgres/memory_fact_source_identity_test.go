package postgres_test

import (
	"os"
	"strings"
	"testing"
)

func TestTenantSchemaContainsFactSourceIdentityGuardrails(t *testing.T) {
	data, err := os.ReadFile("tenant_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(data)
	for _, want := range []string{
		"source_message_id TEXT",
		"source_task_id BIGINT",
		"source_ordinal INT",
		"source_payload_hash TEXT",
		"memory_facts_source_identity_complete",
		"uq_memory_facts_source_user",
		"uq_memory_facts_source_agent",
		"WHERE source_message_id IS NOT NULL AND scope = 'user'",
		"WHERE source_message_id IS NOT NULL AND scope = 'agent'",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("tenant_schema.sql missing fact source identity DDL %q", want)
		}
	}
}

func TestFactSourceIdentityMigrationMarkerIsForwardOnly(t *testing.T) {
	for _, path := range []string{
		"../../migration/sql/024_memory_fact_source_identity.up.sql",
		"../../migration/sql/024_memory_fact_source_identity.down.sql",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.Contains(text, "tenant_schema.sql") {
			t.Fatalf("migration marker %s must identify tenant_schema.sql as canonical DDL", path)
		}
		if strings.Contains(strings.ToUpper(text), "DROP ") {
			t.Fatalf("migration marker %s must retain source identity data on application rollback", path)
		}
	}
}
