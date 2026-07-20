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
