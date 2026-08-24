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
	if got := strings.Count(ChangeAuditInsertSQL, "$"); got != len(cols) {
		t.Fatalf("placeholder count %d != column count %d", got, len(cols))
	}

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
// Placeholder/column-count 一致性由各测试按自己的 VALUES 形状断言（租户 SQL
// 全占位符、platform SQL 有 'platform' 字面量）。
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
	return cols
}

// extractTableColumns parses the column list of a CREATE TABLE block.
// 容忍可选的 schema 限定前缀（public.platform_resource_change_audits）。
func extractTableColumns(t *testing.T, schema, table string) []string {
	t.Helper()
	re := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS (?:\w+\.)?` + table + `\s*\((.*?)\n\);`)
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

// TestPlatformChangeAuditInsertSQLMatchesSchema asserts the shared platform
// audit INSERT column contract stays in lockstep with the 039 migration DDL
// (public.platform_resource_change_audits). llmgateway 与 parameters 两个写入
// 方共用该常量，列漂移会静默破坏写路径。
func TestPlatformChangeAuditInsertSQLMatchesSchema(t *testing.T) {
	cols := extractInsertColumns(t, PlatformChangeAuditInsertSQL)
	// scope 是字面量 'platform'（public 目录恒平台 scope），12 列 = 11 占位符 + 1 字面量。
	if got := strings.Count(PlatformChangeAuditInsertSQL, "$"); got != len(cols)-1 {
		t.Fatalf("placeholder count %d != column count-1 %d (scope literal)", got, len(cols)-1)
	}

	schemaPath := filepath.Join("..", "..", "..", "pkg", "migration", "sql", "039_model_policy_governance.up.sql")
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read 039 up.sql: %v", err)
	}
	ddlCols := extractTableColumns(t, string(raw), "platform_resource_change_audits")

	ddlSet := make(map[string]bool, len(ddlCols))
	for _, c := range ddlCols {
		ddlSet[c] = true
	}
	for _, c := range cols {
		if !ddlSet[c] {
			t.Errorf("platform INSERT column %q not present in CREATE TABLE platform_resource_change_audits", c)
		}
	}
}
