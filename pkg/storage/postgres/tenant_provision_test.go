package postgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestProvisionTenantSchemaIDsContinuesAndAggregatesFailures(t *testing.T) {
	errA := errors.New("tenant a failed")
	errC := errors.New("tenant c failed")
	var attempted []string

	err := provisionTenantSchemaIDs(context.Background(), []string{"a", "b", "c"}, func(_ context.Context, id string) error {
		attempted = append(attempted, id)
		switch id {
		case "a":
			return errA
		case "c":
			return errC
		default:
			return nil
		}
	})

	if !reflect.DeepEqual(attempted, []string{"a", "b", "c"}) {
		t.Fatalf("attempted = %v", attempted)
	}
	if !errors.Is(err, errA) || !errors.Is(err, errC) {
		t.Fatalf("aggregate error = %v, want both tenant failures", err)
	}
	if got := err.Error(); got == "" || !containsAll(got, "tenant a", "tenant c") {
		t.Fatalf("aggregate error lacks tenant context: %v", err)
	}
}

func containsAll(s string, values ...string) bool {
	for _, value := range values {
		if !strings.Contains(s, value) {
			return false
		}
	}
	return true
}

// TestSplitStatementsKeepsSemicolonsInsideStringLiterals 验证语句切分尊重
// 单引号字符串字面量：提示词正文中的分号不得把 UPDATE 拆成两条残缺语句。
func TestSplitStatementsKeepsSemicolonsInsideStringLiterals(t *testing.T) {
	sql := "UPDATE agents SET system_prompt = 'a; b; c' WHERE id = 'x';\n" +
		"CREATE TABLE t (id text);"
	stmts := splitStatements(sql)
	if len(stmts) != 2 {
		t.Fatalf("statements = %d, want 2: %#v", len(stmts), stmts)
	}
	if stmts[0] != "UPDATE agents SET system_prompt = 'a; b; c' WHERE id = 'x'" {
		t.Fatalf("first statement torn by inner semicolon: %q", stmts[0])
	}
	if stmts[1] != "CREATE TABLE t (id text)" {
		t.Fatalf("second statement = %q", stmts[1])
	}
}

// TestSplitStatementsKeepsDollarQuoteBodiesIntact 验证 PL/pgSQL DO 块整体保留。
func TestSplitStatementsKeepsDollarQuoteBodiesIntact(t *testing.T) {
	sql := "DO $$ BEGIN EXECUTE 'select 1; select 2'; END $$;\n" +
		"SELECT 1;"
	stmts := splitStatements(sql)
	if len(stmts) != 2 {
		t.Fatalf("statements = %d, want 2: %#v", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "DO $$") {
		t.Fatalf("DO block torn apart: %q", stmts[0])
	}
	if stmts[1] != "SELECT 1" {
		t.Fatalf("second statement = %q", stmts[1])
	}
}
