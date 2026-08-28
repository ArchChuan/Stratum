package persistence

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/iam/domain"
)

func newAdminRepo(t *testing.T) (*AdminTenantRepo, pgxmock.PgxPoolIface) {
	mock := newMockPool(t)
	return NewAdminTenantRepo(mock), mock
}

func adminTenantRow() *pgxmock.Rows {
	return pgxmock.NewRows([]string{"id", "name", "slug", "plan", "status", "created_at", "member_count", "is_default"})
}

func TestAdminTenantRepo_Count_WithStatus(t *testing.T) {
	repo, mock := newAdminRepo(t)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM public.tenants WHERE deleted_at IS NULL AND status=\$1`).
		WithArgs("active").WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))

	total, err := repo.Count(context.Background(), domain.TenantFilter{Status: "active"})
	require.NoError(t, err)
	require.Equal(t, 2, total)
}

func TestAdminTenantRepo_Count_All(t *testing.T) {
	repo, mock := newAdminRepo(t)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM public.tenants WHERE deleted_at IS NULL$`).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(5))

	total, err := repo.Count(context.Background(), domain.TenantFilter{})
	require.NoError(t, err)
	require.Equal(t, 5, total)
}

func TestAdminTenantRepo_Count_Fails(t *testing.T) {
	repo, mock := newAdminRepo(t)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM public.tenants`).WillReturnError(errAny)

	_, err := repo.Count(context.Background(), domain.TenantFilter{})
	require.ErrorIs(t, err, errAny)
}

func TestAdminTenantRepo_List_WithStatus(t *testing.T) {
	repo, mock := newAdminRepo(t)
	now := time.Now()
	mock.ExpectQuery(`SELECT t.id, t.name, t.slug, t.plan, t.status, t.created_at`).
		WithArgs("active", 20, 40).
		WillReturnRows(adminTenantRow().
			AddRow("t1", "Acme", "acme", "pro", "active", now, 3, false).
			AddRow("t2", "Globex", "globex", "free", "active", now, 1, true))

	tenants, err := repo.List(context.Background(), domain.TenantFilter{Status: "active", Page: 3, PageSize: 20})
	require.NoError(t, err)
	require.Len(t, tenants, 2)
	require.Equal(t, "t1", tenants[0].ID)
	require.Equal(t, 3, tenants[0].MemberCount)
	require.True(t, tenants[1].IsDefault)
}

func TestAdminTenantRepo_List_All(t *testing.T) {
	repo, mock := newAdminRepo(t)
	mock.ExpectQuery(`SELECT t.id, t.name, t.slug, t.plan, t.status, t.created_at`).
		WithArgs(20, 40).
		WillReturnRows(adminTenantRow())

	tenants, err := repo.List(context.Background(), domain.TenantFilter{Page: 3, PageSize: 20})
	require.NoError(t, err)
	require.NotNil(t, tenants)
	require.Empty(t, tenants)
}

func TestAdminTenantRepo_List_QueryFails(t *testing.T) {
	repo, mock := newAdminRepo(t)
	mock.ExpectQuery(`SELECT t.id, t.name, t.slug`).WithArgs(0, 0).WillReturnError(errAny)

	_, err := repo.List(context.Background(), domain.TenantFilter{})
	require.ErrorIs(t, err, errAny)
}

func TestAdminTenantRepo_Get(t *testing.T) {
	repo, mock := newAdminRepo(t)
	now := time.Now()
	mock.ExpectQuery(`SELECT id, name, slug, plan, status, created_at, deleted_at FROM public.tenants WHERE id=\$1`).
		WithArgs("t1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "slug", "plan", "status", "created_at", "deleted_at"}).
			AddRow("t1", "Acme", "acme", "pro", "active", now, nil))

	tenant, err := repo.Get(context.Background(), "t1")
	require.NoError(t, err)
	require.Equal(t, "Acme", tenant.Name)
	require.Nil(t, tenant.DeletedAt)
}

func TestAdminTenantRepo_Get_NotFound(t *testing.T) {
	repo, mock := newAdminRepo(t)
	mock.ExpectQuery(`SELECT id, name, slug`).WithArgs("t1").WillReturnError(pgx.ErrNoRows)

	_, err := repo.Get(context.Background(), "t1")
	require.ErrorIs(t, err, domain.ErrTenantNotFound)
}

func TestAdminTenantRepo_Get_QueryFails(t *testing.T) {
	repo, mock := newAdminRepo(t)
	mock.ExpectQuery(`SELECT id, name, slug`).WithArgs("t1").WillReturnError(errAny)

	_, err := repo.Get(context.Background(), "t1")
	require.ErrorIs(t, err, errAny)
}

func TestAdminTenantRepo_Create(t *testing.T) {
	repo, mock := newAdminRepo(t)
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO public.tenants\(id, name, slug, plan, status, created_at\)`).
		WithArgs("t1", "Acme", "acme", "pro", "active", now).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	err := repo.Create(context.Background(), domain.Tenant{ID: "t1", Name: "Acme", Slug: "acme", Plan: "pro", Status: "active", CreatedAt: now}, "", nil)
	require.NoError(t, err)
}

// TestAdminTenantRepo_Create_WritesAudit 传真实审计事件时，事务内必须落平台审计
// INSERT（11 个占位符，actor_tenant_id 传空 → NULL）。
func TestAdminTenantRepo_Create_WritesAudit(t *testing.T) {
	repo, mock := newAdminRepo(t)
	now := time.Now()
	ev := &auditdomain.ResourceChangeAuditEvent{
		ResourceKind: auditdomain.ResourceKindTenant,
		ResourceID:   "t1",
		Operation:    auditdomain.ChangeOpCreate,
		ActorID:      "actor-1",
		After:        json.RawMessage(`{"id":"t1","name":"Acme"}`),
	}
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO public.tenants\(id, name, slug, plan, status, created_at\)`).
		WithArgs("t1", "Acme", "acme", "pro", "active", now).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec(`INSERT INTO public.platform_resource_change_audits`).
		WithArgs(pgxmock.AnyArg(), "tenant", "t1", "create", "actor-1", nil,
			"user", "api", "", json.RawMessage(`{}`), json.RawMessage(`{"id":"t1","name":"Acme"}`)).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	err := repo.Create(context.Background(), domain.Tenant{ID: "t1", Name: "Acme", Slug: "acme", Plan: "pro", Status: "active", CreatedAt: now}, "", ev)
	require.NoError(t, err)
}

func TestAdminTenantRepo_Create_Fails(t *testing.T) {
	repo, mock := newAdminRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO public.tenants`).WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(errAny)
	mock.ExpectRollback()

	require.ErrorIs(t, repo.Create(context.Background(), domain.Tenant{}, "", nil), errAny)
}

func TestAdminTenantRepo_UpdatePatch(t *testing.T) {
	repo, mock := newAdminRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE public.tenants SET plan=COALESCE\(NULLIF\(\$1,''\), plan\), status=COALESCE\(NULLIF\(\$2,''\), status\) WHERE id=\$3 AND deleted_at IS NULL`).
		WithArgs("pro", "active", "t1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.UpdatePatch(context.Background(), "t1", domain.TenantPatch{Plan: "pro", Status: "active"}, "", nil))
}

func TestAdminTenantRepo_UpdatePatch_NotFound(t *testing.T) {
	repo, mock := newAdminRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE public.tenants SET plan=COALESCE`).
		WithArgs("pro", "active", "t1").WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectRollback()

	require.ErrorIs(t, repo.UpdatePatch(context.Background(), "t1", domain.TenantPatch{Plan: "pro", Status: "active"}, "", nil), domain.ErrTenantNotFound)
}

func TestAdminTenantRepo_UpdatePatch_ExecFails(t *testing.T) {
	repo, mock := newAdminRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE public.tenants SET plan=COALESCE`).
		WithArgs("pro", "active", "t1").WillReturnError(errAny)
	mock.ExpectRollback()

	require.ErrorIs(t, repo.UpdatePatch(context.Background(), "t1", domain.TenantPatch{Plan: "pro", Status: "active"}, "", nil), errAny)
}

func TestAdminTenantRepo_HardDelete(t *testing.T) {
	repo, mock := newAdminRepo(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT is_default FROM public.tenants WHERE id=\$1`).
		WithArgs("t1").WillReturnRows(pgxmock.NewRows([]string{"is_default"}).AddRow(false))
	mock.ExpectExec(`DELETE FROM public.tenants WHERE id=\$1`).
		WithArgs("t1").WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.HardDelete(context.Background(), "t1", "", nil))
}

func TestAdminTenantRepo_HardDelete_DefaultTenant(t *testing.T) {
	repo, mock := newAdminRepo(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT is_default FROM public.tenants WHERE id=\$1`).
		WithArgs("t1").WillReturnRows(pgxmock.NewRows([]string{"is_default"}).AddRow(true))
	mock.ExpectRollback()

	require.ErrorIs(t, repo.HardDelete(context.Background(), "t1", "", nil), domain.ErrDefaultTenantDelete)
}

func TestAdminTenantRepo_HardDelete_NotFound(t *testing.T) {
	repo, mock := newAdminRepo(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT is_default FROM public.tenants WHERE id=\$1`).
		WithArgs("t1").WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	require.ErrorIs(t, repo.HardDelete(context.Background(), "t1", "", nil), domain.ErrTenantNotFound)
}

func TestAdminTenantRepo_HardDelete_DeleteNoRows(t *testing.T) {
	repo, mock := newAdminRepo(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT is_default FROM public.tenants WHERE id=\$1`).
		WithArgs("t1").WillReturnRows(pgxmock.NewRows([]string{"is_default"}).AddRow(false))
	mock.ExpectExec(`DELETE FROM public.tenants WHERE id=\$1`).
		WithArgs("t1").WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectRollback()

	require.ErrorIs(t, repo.HardDelete(context.Background(), "t1", "", nil), domain.ErrTenantNotFound)
}

func TestAdminTenantRepo_ProvisionSchema_NilPoolNoop(t *testing.T) {
	repo := NewAdminTenantRepo(nil)
	require.NoError(t, repo.ProvisionSchema(context.Background(), "t1"))
}

func TestAdminTenantRepo_ProvisionSchema_MockPoolRejected(t *testing.T) {
	repo, _ := newAdminRepo(t) // pgxmock 不是 *pgxpool.Pool
	err := repo.ProvisionSchema(context.Background(), "t1")
	require.ErrorContains(t, err, "requires a real pgx pool")
}

func TestAdminTenantRepo_ActivateTenant(t *testing.T) {
	repo, mock := newAdminRepo(t)
	mock.ExpectExec(`UPDATE public.tenants SET status='active', updated_at=NOW\(\) WHERE id=\$1`).
		WithArgs("t1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	require.NoError(t, repo.ActivateTenant(context.Background(), "t1"))
}

func TestAdminTenantRepo_ActivateTenant_Fails(t *testing.T) {
	repo, mock := newAdminRepo(t)
	mock.ExpectExec(`UPDATE public.tenants SET status='active'`).
		WithArgs("t1").WillReturnError(errAny)

	require.ErrorIs(t, repo.ActivateTenant(context.Background(), "t1"), errAny)
}

func TestAdminTenantRepo_MarkProvisioningFailed(t *testing.T) {
	repo, mock := newAdminRepo(t)
	mock.ExpectExec(`UPDATE public.tenants SET status='provision_failed', updated_at=NOW\(\) WHERE id=\$1`).
		WithArgs("t1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	require.NoError(t, repo.MarkProvisioningFailed(context.Background(), "t1"))
}
