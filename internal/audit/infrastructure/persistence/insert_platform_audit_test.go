package persistence

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

// TestInsertPlatformAuditTx_WritesRow 成功路径：事件归一化后 11 个占位符逐位
// 落位，actor_tenant_id 传空 → NULL。
func TestInsertPlatformAuditTx_WritesRow(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	mock.ExpectBegin()
	tx, err := mock.Begin(context.Background())
	require.NoError(t, err)

	ev := &domain.ResourceChangeAuditEvent{
		ResourceKind: domain.ResourceKindTenant,
		ResourceID:   "tid-1",
		Operation:    domain.ChangeOpCreate,
		ActorID:      "actor-1",
		Before:       []byte(`{}`),
		After:        []byte(`{"id":"tid-1"}`),
	}
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO public.platform_resource_change_audits`)).
		WithArgs(pgxmock.AnyArg(), "tenant", "tid-1", "create", "actor-1", nil,
			"user", "api", "", json.RawMessage(`{}`), json.RawMessage(`{"id":"tid-1"}`)).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = InsertPlatformAuditTx(context.Background(), tx, "", ev)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestInsertPlatformAuditTx_WithActorTenant 平台内操作（如租户成员管理员）actor_tenant_id
// 落实际租户 ID，非 NULL。
func TestInsertPlatformAuditTx_WithActorTenant(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	mock.ExpectBegin()
	tx, err := mock.Begin(context.Background())
	require.NoError(t, err)

	ev := &domain.ResourceChangeAuditEvent{
		ResourceKind: domain.ResourceKindAdmin,
		ResourceID:   "u-9",
		Operation:    domain.ChangeOpCreate,
		ActorID:      "actor-1",
	}
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO public.platform_resource_change_audits`)).
		WithArgs(pgxmock.AnyArg(), "admin", "u-9", "create", "actor-1", "tenant-abc",
			"user", "api", "", json.RawMessage(`{}`), json.RawMessage(`{}`)).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = InsertPlatformAuditTx(context.Background(), tx, "tenant-abc", ev)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestInsertPlatformAuditTx_NilEventNoop nil 事件不触 SQL（调用方"无需审计"场景）。
func TestInsertPlatformAuditTx_NilEventNoop(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	mock.ExpectBegin()
	tx, err := mock.Begin(context.Background())
	require.NoError(t, err)

	require.NoError(t, InsertPlatformAuditTx(context.Background(), tx, "", nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestInsertPlatformAuditTx_ErrorWrapped 执行失败必须 wrap 传播，禁止吞错。
func TestInsertPlatformAuditTx_ErrorWrapped(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	mock.ExpectBegin()
	tx, err := mock.Begin(context.Background())
	require.NoError(t, err)

	ev := &domain.ResourceChangeAuditEvent{ResourceKind: "model", ResourceID: "m-1", Operation: "update", ActorID: "a"}
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO public.platform_resource_change_audits`)).
		WillReturnError(pgx.ErrTxClosed)

	err = InsertPlatformAuditTx(context.Background(), tx, "", ev)
	require.Error(t, err)
	require.ErrorContains(t, err, "insert platform audit")
}
