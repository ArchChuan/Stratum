package domain

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestChangeAuditInsertSQLMatchesSchema asserts the shared INSERT column
// contract stays in lockstep with the tenant_schema.sql table definition.
// Every repository uses ChangeAuditInsertSQL inside its business transaction,
// so a column drift here would silently break writes.
func TestChangeAuditInsertSQLMatchesSchema(t *testing.T) {
	cols := extractInsertColumns(t, ChangeAuditInsertSQL)

	schemaPath := filepath.Join("..", "..", "..", "pkg", "storage", "postgres", "tenant_schema.sql")
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read tenant_schema.sql: %v", err)
	}
	ddlCols := extractTableColumns(t, string(raw), "resource_change_audits")

	ddlSet := make(map[string]bool, len(ddlCols))
	for _, c := range ddlCols {
		ddlSet[c] = true
	}
	for _, c := range cols {
		if !ddlSet[c] {
			t.Errorf("INSERT column %q not present in CREATE TABLE resource_change_audits", c)
		}
	}
}

// extractInsertColumns parses "(col1, col2, ...)" from an INSERT statement.
func extractInsertColumns(t *testing.T, sql string) []string {
	t.Helper()
	m := regexp.MustCompile(`\(([^)]+)\)\s+VALUES`).FindStringSubmatch(sql)
	if len(m) != 2 {
		t.Fatalf("no column list found in INSERT: %s", sql)
	}
	var cols []string
	for _, c := range strings.Split(m[1], ",") {
		cols = append(cols, strings.TrimSpace(c))
	}
	placeholders := strings.Count(sql, "$")
	if placeholders != len(cols) {
		t.Fatalf("placeholder count %d != column count %d", placeholders, len(cols))
	}
	return cols
}

// extractTableColumns parses the column list of a CREATE TABLE block.
func extractTableColumns(t *testing.T, schema, table string) []string {
	t.Helper()
	re := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS ` + table + `\s*\((.*?)\n\);`)
	m := re.FindStringSubmatch(schema)
	if len(m) != 2 {
		t.Fatalf("CREATE TABLE %s not found in tenant_schema.sql", table)
	}
	var cols []string
	for _, line := range strings.Split(m[1], "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "CONSTRAINT") {
			continue
		}
		cols = append(cols, fields[0])
	}
	return cols
}
