package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/infrastructure/seeds"
)

// stubRow 实现 pgx.Row.Scan;通过 hash / scanErr 注入 needsUpdate 的三种结果。
type stubRow struct {
	hash    string
	scanErr error
}

func (r stubRow) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	if len(dest) > 0 {
		if p, ok := dest[0].(*string); ok {
			*p = r.hash
		}
	}
	return nil
}

// execCall 记录一次 Exec 调用,供断言 SQL 与参数。
type execCall struct {
	sql  string
	args []any
}

// stubDB 注入 tenant 枚举、行查询与执行的确定性行为。
type stubDB struct {
	schemas    []string
	schemasErr error
	queryRowFn func(sql string, args ...any) pgx.Row
	execFn     func(sql string, args ...any) (pgconn.CommandTag, error)
}

func (s stubDB) TenantSchemas(context.Context) ([]string, error) {
	return s.schemas, s.schemasErr
}

func (s stubDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	return s.queryRowFn(sql, args...)
}

func (s stubDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return s.execFn(sql, args...)
}

var _ db = stubDB{}

func cmdTag(n int) pgconn.CommandTag {
	return pgconn.NewCommandTag("UPDATE " + itoa(n))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestNeedsUpdate(t *testing.T) {
	sk := seeds.BuiltinSkills()[0]
	cases := []struct {
		name    string
		row     stubRow
		want    bool
		wantErr bool
	}{
		{name: "same content hash skips", row: stubRow{hash: sk.Revision.ContentHash}, want: false},
		{name: "different content hash needs update", row: stubRow{hash: "old-hash"}, want: true},
		{name: "missing row needs update", row: stubRow{scanErr: pgx.ErrNoRows}, want: true},
		{name: "query error propagates", row: stubRow{scanErr: errors.New("db down")}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := stubDB{queryRowFn: func(string, ...any) pgx.Row { return tc.row }}
			got, err := needsUpdate(context.Background(), d, "tenant_abc", sk)
			if (err != nil) != tc.wantErr {
				t.Fatalf("needsUpdate error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if got != tc.want {
				t.Fatalf("needsUpdate = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNeedsUpdateQueriesActiveRevision(t *testing.T) {
	sk := seeds.BuiltinSkills()[0]
	var gotSQL string
	var gotArgs []any
	d := stubDB{queryRowFn: func(sql string, args ...any) pgx.Row {
		gotSQL, gotArgs = sql, args
		return stubRow{hash: sk.Revision.ContentHash}
	}}
	if _, err := needsUpdate(context.Background(), d, "tenant_abc", sk); err != nil {
		t.Fatalf("needsUpdate error = %v", err)
	}
	if !strings.Contains(gotSQL, `"tenant_abc"."skill_revisions"`) {
		t.Fatalf("needsUpdate SQL missing quoted schema: %q", gotSQL)
	}
	if !strings.Contains(gotSQL, `"tenant_abc"."skills"`) {
		t.Fatalf("needsUpdate SQL missing skills table: %q", gotSQL)
	}
	if len(gotArgs) != 1 || gotArgs[0] != sk.ID {
		t.Fatalf("needsUpdate args = %v, want [%s]", gotArgs, sk.ID)
	}
}

func TestApplyUpdates(t *testing.T) {
	sk := seeds.BuiltinSkill{
		ID:          "builtin:test",
		Name:        "stratum-test",
		Description: "测试",
		Revision: domain.SkillRevision{
			ID:           "rev-builtin-test-v1",
			Name:         "stratum-test",
			Description:  "测试",
			Instructions: "指令",
			ContentHash:  "hash-abc",
			PublishChecks: map[string]any{
				"required": true,
			},
		},
	}
	var calls []execCall
	d := stubDB{execFn: func(sql string, args ...any) (pgconn.CommandTag, error) {
		calls = append(calls, execCall{sql: sql, args: args})
		return cmdTag(1), nil
	}}
	if err := applyUpdates(context.Background(), d, "tenant_abc", sk); err != nil {
		t.Fatalf("applyUpdates error = %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("applyUpdates made %d execs, want 2", len(calls))
	}

	skills, revs := calls[0], calls[1]
	if !strings.Contains(skills.sql, `UPDATE "tenant_abc"."skills" SET name=$1`) {
		t.Fatalf("skills UPDATE SQL = %q", skills.sql)
	}
	wantSkillArgs := []any{"stratum-test", "测试", "rev-builtin-test-v1", "builtin:test"}
	if !equalArgs(skills.args, wantSkillArgs) {
		t.Fatalf("skills args = %v, want %v", skills.args, wantSkillArgs)
	}
	if !strings.Contains(revs.sql, `UPDATE "tenant_abc"."skill_revisions" SET name=$1`) {
		t.Fatalf("revisions UPDATE SQL = %q", revs.sql)
	}
	if !strings.Contains(revs.sql, `publish_checks=$5::jsonb`) {
		t.Fatalf("revisions UPDATE SQL missing jsonb cast: %q", revs.sql)
	}
	wantRevArgs := []any{"stratum-test", "测试", "指令", "hash-abc", `{"required":true}`, "rev-builtin-test-v1", "builtin:test"}
	if !equalArgs(revs.args, wantRevArgs) {
		t.Fatalf("revisions args = %v, want %v", revs.args, wantRevArgs)
	}
}

func TestApplyUpdatesZeroAffected(t *testing.T) {
	d := stubDB{execFn: func(string, ...any) (pgconn.CommandTag, error) { return cmdTag(0), nil }}
	err := applyUpdates(context.Background(), d, "tenant_abc", seeds.BuiltinSkills()[0])
	if err == nil {
		t.Fatal("applyUpdates succeeded but UPDATE affected 0 rows")
	}
	if !strings.Contains(err.Error(), "row not found") {
		t.Fatalf("applyUpdates error = %v, want 'row not found'", err)
	}
}

func TestApplyUpdatesExecErrorPropagates(t *testing.T) {
	d := stubDB{execFn: func(string, ...any) (pgconn.CommandTag, error) {
		return cmdTag(0), errors.New("db down")
	}}
	err := applyUpdates(context.Background(), d, "tenant_abc", seeds.BuiltinSkills()[0])
	if err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("applyUpdates error = %v, want db down propagated", err)
	}
}

func TestBackfillTenant(t *testing.T) {
	skills := seeds.BuiltinSkills()
	cases := []struct {
		name     string
		row      stubRow
		execute  bool
		want     int
		wantExec int
	}{
		{name: "dry run counts without writing", row: stubRow{hash: "old"}, execute: false, want: 4, wantExec: 0},
		{name: "execute writes two updates per skill", row: stubRow{hash: "old"}, execute: true, want: 4, wantExec: 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			execs := 0
			d := stubDB{
				queryRowFn: func(string, ...any) pgx.Row { return tc.row },
				execFn: func(string, ...any) (pgconn.CommandTag, error) {
					execs++
					return cmdTag(1), nil
				},
			}
			got, err := backfillTenant(context.Background(), d, "tenant_abc", skills, tc.execute)
			if err != nil {
				t.Fatalf("backfillTenant error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("backfillTenant changed = %d, want %d", got, tc.want)
			}
			if execs != tc.wantExec {
				t.Fatalf("backfillTenant made %d execs, want %d", execs, tc.wantExec)
			}
		})
	}
}

// TestBackfillTenantUpToDate 每个 skill 的 active content_hash 与种子一致时全部跳过。
// 种子的 4 个 skill 各有独立 hash,queryRowFn 须按 skill ID 返回对应 hash,共享 stubRow 做不到。
func TestBackfillTenantUpToDate(t *testing.T) {
	skills := seeds.BuiltinSkills()
	hashByID := make(map[string]string, len(skills))
	for _, sk := range skills {
		hashByID[sk.ID] = sk.Revision.ContentHash
	}
	execs := 0
	d := stubDB{
		queryRowFn: func(_ string, args ...any) pgx.Row {
			return stubRow{hash: hashByID[args[0].(string)]}
		},
		execFn: func(string, ...any) (pgconn.CommandTag, error) {
			execs++
			return cmdTag(1), nil
		},
	}
	got, err := backfillTenant(context.Background(), d, "tenant_abc", skills, true)
	if err != nil {
		t.Fatalf("backfillTenant error = %v", err)
	}
	if got != 0 {
		t.Fatalf("backfillTenant changed = %d, want 0", got)
	}
	if execs != 0 {
		t.Fatalf("backfillTenant made %d execs, want 0", execs)
	}
}

func TestBackfillAll(t *testing.T) {
	skills := seeds.BuiltinSkills()
	row := stubRow{hash: "old"}
	cases := []struct {
		name       string
		schemas    []string
		schemasErr error
		execute    bool
		filter     string
		wantTotal  int
		wantErr    bool
	}{
		{name: "dry run across tenants", schemas: []string{"tenant_a", "tenant_b"}, execute: false, wantTotal: 8},
		{name: "execute across tenants", schemas: []string{"tenant_a", "tenant_b"}, execute: true, wantTotal: 8},
		{name: "tenant filter restricts to one tenant", schemas: []string{"tenant_a", "tenant_b"}, execute: true, filter: "b", wantTotal: 4},
		{name: "tenant filter with no match is a no-op", schemas: []string{"tenant_a"}, execute: true, filter: "zzz", wantTotal: 0},
		{name: "tenant enumeration error propagates", schemasErr: errors.New("db down"), wantErr: true},
		{name: "no tenants is a no-op", schemas: nil, wantTotal: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := stubDB{
				schemas:    tc.schemas,
				schemasErr: tc.schemasErr,
				queryRowFn: func(string, ...any) pgx.Row { return row },
				execFn: func(string, ...any) (pgconn.CommandTag, error) {
					return cmdTag(1), nil
				},
			}
			total, err := backfillAll(context.Background(), d, skills, tc.execute, tc.filter, zap.NewNop())
			if (err != nil) != tc.wantErr {
				t.Fatalf("backfillAll error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if total != tc.wantTotal {
				t.Fatalf("backfillAll total = %d, want %d", total, tc.wantTotal)
			}
		})
	}
}

func TestBackfillAllPropagatesTenantFailure(t *testing.T) {
	skills := seeds.BuiltinSkills()
	d := stubDB{
		schemas: []string{"tenant_a"},
		queryRowFn: func(sql string, _ ...any) pgx.Row {
			if strings.Contains(sql, "skill_revisions") {
				return stubRow{scanErr: errors.New("schema missing")}
			}
			return stubRow{hash: "old"}
		},
	}
	_, err := backfillAll(context.Background(), d, skills, true, "", zap.NewNop())
	if err == nil || !strings.Contains(err.Error(), "tenant_a") {
		t.Fatalf("backfillAll error = %v, want tenant_a context", err)
	}
}

func equalArgs(got, want []any) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
