package persistence

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/audit/domain/port"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

func newChangeAuditMock(t *testing.T) pgxmock.PgxPoolIface {
	t.Helper()
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	return mock
}

func withTenantExpects(mock pgxmock.PgxPoolIface) {
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SET LOCAL search_path = "tenant_abc-1", public`)).
		WillReturnResult(pgxmock.NewResult("SET", 0))
}

// TestPgResourceChangeAuditRepo_EmptyTenantFailsClosed 空 tenantID 在触碰连接池
// 之前 fail closed（nil pool 可安全传入，因为提前 return）。
func TestPgResourceChangeAuditRepo_EmptyTenantFailsClosed(t *testing.T) {
	repo := &PgResourceChangeAuditRepo{} // 注意：nil pool，仅验证提前 return
	_, _, err := repo.List(context.Background(), "", port.ResourceChangeAuditFilter{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tenant id required")

	got, err := repo.GetByID(context.Background(), "", "id")
	require.Error(t, err)
	require.Nil(t, got)
}

// TestBuildChangeAuditWhere 锁定 WHERE 构造：tenant_id 谓词恒存在、actor_name
// 走 public.users 子串匹配、时间范围带占位符。
func TestBuildChangeAuditWhere(t *testing.T) {
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	where, args := buildChangeAuditWhere("tenant-1", port.ResourceChangeAuditFilter{
		ResourceKind: "workflow",
		ActorName:    "li",
		From:         &from,
		To:           &to,
	})
	require.Contains(t, where, `tenant_id = $1`)
	require.Contains(t, where, `resource_kind = $2`)
	require.Contains(t, where, `public.users`)
	require.Contains(t, where, `created_at >= $`)
	require.Contains(t, where, `created_at <= $`)
	require.Len(t, args, 5)

	whereOnly, argsOnly := buildChangeAuditWhere("tenant-1", port.ResourceChangeAuditFilter{})
	require.Equal(t, `WHERE tenant_id = $1`, whereOnly)
	require.Equal(t, []any{"tenant-1"}, argsOnly)
}

// TestPgResourceChangeAuditRepo_List_NoFilter 全链路（count+list+users 映射）
// 走 execTenant 事务。
func TestPgResourceChangeAuditRepo_List_NoFilter(t *testing.T) {
	mock := newChangeAuditMock(t)
	withTenantExpects(mock)

	created := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM resource_change_audits`).
		WithArgs("abc-1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery(`SELECT id, resource_kind, resource_id`).
		WithArgs("abc-1", 20, 0).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "operation", "actor_id", "created_at",
			"before_projection", "after_projection",
		}).
			AddRow("a1", "workflow", "wf-1", "publish", "u-1", created, []byte(`{}`), []byte(`{"id":"wf-1"}`)).
			AddRow("a2", "agent", "ag-1", "create", "worker", created, []byte(`{}`), []byte(`{}`)))
	// actor_id 支持 system 占位符（worker 非 uuid），必须 id::text = ANY($1) 而非
	// id = ANY($1)——后者被 PG 推断为 uuid[] 后逐元素 cast，非 uuid 值直接
	// invalid input syntax (22P02)。正则锁定 WHERE 条件防回归。
	mock.ExpectQuery(`SELECT id, COALESCE\(display_name,''\), COALESCE\(github_login,''\)\s+FROM public\.users WHERE id::text = ANY\(\$1\)`).
		WithArgs([]string{"u-1", "worker"}).
		WillReturnRows(pgxmock.NewRows([]string{"id", "display_name", "github_login"}).
			AddRow("u-1", "李雷", "lilei"))
	mock.ExpectCommit()

	repo := &PgResourceChangeAuditRepo{pool: mock}
	rows, total, err := repo.List(context.Background(), "abc-1", port.ResourceChangeAuditFilter{Limit: 20})
	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Len(t, rows, 2)
	require.Equal(t, "李雷", rows[0].ActorName)     // display_name 命中
	require.Equal(t, "worker", rows[1].ActorName) // 无 users 行 → actor_id 兜底
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPgResourceChangeAuditRepo_List_Empty skips SELECT 当 total==0。
func TestPgResourceChangeAuditRepo_List_Empty(t *testing.T) {
	mock := newChangeAuditMock(t)
	withTenantExpects(mock)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM resource_change_audits`).
		WithArgs("abc-1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectCommit()

	repo := &PgResourceChangeAuditRepo{pool: mock}
	rows, total, err := repo.List(context.Background(), "abc-1", port.ResourceChangeAuditFilter{})
	require.NoError(t, err)
	require.Equal(t, 0, total)
	require.Empty(t, rows)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPgResourceChangeAuditRepo_GetByID_NotFound pgx.ErrNoRows 映射为 (nil, nil)。
func TestPgResourceChangeAuditRepo_GetByID_NotFound(t *testing.T) {
	mock := newChangeAuditMock(t)
	withTenantExpects(mock)
	mock.ExpectQuery(`SELECT id, resource_kind, resource_id`).
		WithArgs("abc-1", "missing-id").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	repo := &PgResourceChangeAuditRepo{pool: mock}
	got, err := repo.GetByID(context.Background(), "abc-1", "missing-id")
	require.NoError(t, err)
	require.Nil(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPgResourceChangeAuditRepo_GetByID_Found 成功路径含 actor name 映射。
func TestPgResourceChangeAuditRepo_GetByID_Found(t *testing.T) {
	mock := newChangeAuditMock(t)
	withTenantExpects(mock)
	created := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT id, resource_kind, resource_id`).
		WithArgs("abc-1", "a1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "operation", "actor_id", "created_at",
			"before_projection", "after_projection",
		}).
			AddRow("a1", "knowledge", "kb-1", "update", "u-1", created, []byte(`{"a":1}`), []byte(`{"a":2}`)))
	mock.ExpectQuery(`SELECT id, COALESCE\(display_name,''\), COALESCE\(github_login,''\)\s+FROM public\.users WHERE id::text = ANY\(\$1\)`).
		WithArgs([]string{"u-1"}).
		WillReturnRows(pgxmock.NewRows([]string{"id", "display_name", "github_login"}).
			AddRow("u-1", "", "lilei"))
	mock.ExpectCommit()

	repo := &PgResourceChangeAuditRepo{pool: mock}
	got, err := repo.GetByID(context.Background(), "abc-1", "a1")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "lilei", got.ActorName) // display_name 空 → github_login
	require.NoError(t, mock.ExpectationsWereMet())
}
