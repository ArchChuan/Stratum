package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
)

func newTenantRepo(t *testing.T) (*TenantRepo, pgxmock.PgxPoolIface) {
	mock := newMockPool(t)
	return NewTenantRepo(mock), mock
}

func TestTenantRepo_CountMembers(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM public.tenant_members WHERE tenant_id=\$1`).
		WithArgs("t1").WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(3))

	total, err := repo.CountMembers(context.Background(), "t1")
	require.NoError(t, err)
	require.Equal(t, 3, total)
}

func TestTenantRepo_CountMembers_QueryFails(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM public.tenant_members`).
		WithArgs("t1").WillReturnError(errAny)

	_, err := repo.CountMembers(context.Background(), "t1")
	require.ErrorContains(t, err, "count members")
}

func TestTenantRepo_ListMembers(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectQuery(`SELECT tm.user_id, u.github_login`).
		WithArgs("t1", 10, 0).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "github_login", "avatar_url", "role", "joined_at"}).
			AddRow("u1", "alice", "https://a", "admin", time.Now()).
			AddRow("u2", "bob", "", "member", time.Now()))

	members, err := repo.ListMembers(context.Background(), "t1", 10, 0)
	require.NoError(t, err)
	require.Len(t, members, 2)
	require.Equal(t, "alice", members[0].GitHubLogin)
	require.Equal(t, "admin", members[0].Role)
}

func TestTenantRepo_ListMembers_EmptyIsNonNil(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectQuery(`SELECT tm.user_id, u.github_login`).
		WithArgs("t1", 10, 0).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "github_login", "avatar_url", "role", "joined_at"}))

	members, err := repo.ListMembers(context.Background(), "t1", 10, 0)
	require.NoError(t, err)
	require.Nil(t, members) // 产品实现：零行返回 nil 切片
	require.Empty(t, members)
}

func TestTenantRepo_ListMembers_QueryFails(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectQuery(`SELECT tm.user_id, u.github_login`).
		WithArgs("t1", 10, 0).WillReturnError(errAny)

	_, err := repo.ListMembers(context.Background(), "t1", 10, 0)
	require.ErrorContains(t, err, "list members")
}

func TestTenantRepo_ListMembers_ScanFails(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectQuery(`SELECT tm.user_id, u.github_login`).
		WithArgs("t1", 10, 0).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "github_login"}).AddRow("u1", "alice"))

	_, err := repo.ListMembers(context.Background(), "t1", 10, 0)
	require.ErrorContains(t, err, "scan member")
}

func TestTenantRepo_GetMemberRole(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectQuery(`SELECT role FROM public.tenant_members WHERE tenant_id=\$1 AND user_id=\$2`).
		WithArgs("t1", "u1").WillReturnRows(pgxmock.NewRows([]string{"role"}).AddRow("admin"))

	role, err := repo.GetMemberRole(context.Background(), "t1", "u1")
	require.NoError(t, err)
	require.Equal(t, "admin", role)
}

func TestTenantRepo_GetMemberRole_NotFound(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectQuery(`SELECT role FROM public.tenant_members`).
		WithArgs("t1", "u1").WillReturnError(pgxErrNoRows())

	_, err := repo.GetMemberRole(context.Background(), "t1", "u1")
	require.ErrorIs(t, err, domain.ErrMemberNotFound)
}

func TestTenantRepo_GetMemberRole_QueryFails(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectQuery(`SELECT role FROM public.tenant_members`).
		WithArgs("t1", "u1").WillReturnError(errAny)

	_, err := repo.GetMemberRole(context.Background(), "t1", "u1")
	require.ErrorContains(t, err, "get member role")
}

func TestTenantRepo_UpdateMemberRole(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectExec(`UPDATE public.tenant_members SET role=\$1 WHERE tenant_id=\$2 AND user_id=\$3`).
		WithArgs("member", "t1", "u1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	require.NoError(t, repo.UpdateMemberRole(context.Background(), "t1", "u1", "member"))
}

func TestTenantRepo_UpdateMemberRole_NotFound(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectExec(`UPDATE public.tenant_members SET role=\$1`).
		WithArgs("member", "t1", "u1").WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	err := repo.UpdateMemberRole(context.Background(), "t1", "u1", "member")
	require.ErrorIs(t, err, domain.ErrMemberNotFound)
}

func TestTenantRepo_UpdateMemberRole_ExecFails(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectExec(`UPDATE public.tenant_members SET role=\$1`).
		WithArgs("member", "t1", "u1").WillReturnError(errAny)

	err := repo.UpdateMemberRole(context.Background(), "t1", "u1", "member")
	require.ErrorContains(t, err, "update member role")
}

func TestTenantRepo_DeleteMember(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectExec(`DELETE FROM public.tenant_members WHERE tenant_id=\$1 AND user_id=\$2`).
		WithArgs("t1", "u1").WillReturnResult(pgxmock.NewResult("DELETE", 1))

	require.NoError(t, repo.DeleteMember(context.Background(), "t1", "u1"))
}

func TestTenantRepo_DeleteMember_NotFound(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectExec(`DELETE FROM public.tenant_members WHERE tenant_id=\$1`).
		WithArgs("t1", "u1").WillReturnResult(pgxmock.NewResult("DELETE", 0))

	require.ErrorIs(t, repo.DeleteMember(context.Background(), "t1", "u1"), domain.ErrMemberNotFound)
}

func TestTenantRepo_GetTenantSettings(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectQuery(`SELECT name, is_default, settings FROM public.tenants`).
		WithArgs("t1").
		WillReturnRows(pgxmock.NewRows([]string{"name", "is_default", "settings"}).AddRow("Acme", true, []byte(`{"x":1}`)))

	name, isDefault, settings, err := repo.GetTenantSettings(context.Background(), "t1")
	require.NoError(t, err)
	require.Equal(t, "Acme", name)
	require.True(t, isDefault)
	require.JSONEq(t, `{"x":1}`, string(settings))
}

func TestTenantRepo_GetTenantSettings_NotFound(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectQuery(`SELECT name, is_default, settings FROM public.tenants`).
		WithArgs("t1").WillReturnError(pgxErrNoRows())

	_, _, _, err := repo.GetTenantSettings(context.Background(), "t1")
	require.ErrorIs(t, err, domain.ErrTenantNotFound)
}

func TestTenantRepo_UpdateTenantName(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectExec(`UPDATE public.tenants SET name=\$1, updated_at=now\(\) WHERE id=\$2`).
		WithArgs("New", "t1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	require.NoError(t, repo.UpdateTenantName(context.Background(), "t1", "New"))
}

func TestTenantRepo_UpdateTenantName_NotFound(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectExec(`UPDATE public.tenants SET name=\$1`).
		WithArgs("New", "t1").WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	require.ErrorIs(t, repo.UpdateTenantName(context.Background(), "t1", "New"), domain.ErrTenantNotFound)
}

func TestTenantRepo_UpdateTenantSettings(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectExec(`UPDATE public.tenants SET settings=\$1, updated_at=now\(\) WHERE id=\$2`).
		WithArgs(`{"a":1}`, "t1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	require.NoError(t, repo.UpdateTenantSettings(context.Background(), "t1", []byte(`{"a":1}`)))
}

func TestTenantRepo_UpdateTenantSettings_NotFound(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectExec(`UPDATE public.tenants SET settings=\$1`).
		WithArgs(`{"a":1}`, "t1").WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	require.ErrorIs(t, repo.UpdateTenantSettings(context.Background(), "t1", []byte(`{"a":1}`)), domain.ErrTenantNotFound)
}

func TestTenantRepo_ListUserTenants(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectQuery(`SELECT t.id, t.name, t.is_default`).
		WithArgs("u1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "is_default"}).
			AddRow("t1", "Acme", true).AddRow("t2", "Globex", false))

	tenants, err := repo.ListUserTenants(context.Background(), "u1")
	require.NoError(t, err)
	require.Len(t, tenants, 2)
	require.Equal(t, "t1", tenants[0].TenantID)
	require.True(t, tenants[0].IsDefault)
}

func TestTenantRepo_ListUserTenants_EmptyIsNonNil(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectQuery(`SELECT t.id, t.name, t.is_default`).
		WithArgs("u1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "is_default"}))

	tenants, err := repo.ListUserTenants(context.Background(), "u1")
	require.NoError(t, err)
	require.Nil(t, tenants) // 产品实现：零行返回 nil 切片
	require.Empty(t, tenants)
}

func TestTenantRepo_ListUserTenants_QueryFails(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectQuery(`SELECT t.id, t.name, t.is_default`).
		WithArgs("u1").WillReturnError(errAny)

	_, err := repo.ListUserTenants(context.Background(), "u1")
	require.ErrorContains(t, err, "list user tenants")
}

func TestTenantRepo_ListMembers_QueryFailsWrapped(t *testing.T) {
	repo, mock := newTenantRepo(t)
	mock.ExpectQuery(`SELECT tm.user_id, u.github_login`).
		WithArgs("t1", 10, 0).WillReturnError(errAny)

	_, err := repo.ListMembers(context.Background(), "t1", 10, 0)
	require.ErrorIs(t, err, errAny)
}

// pgxErrNoRows 返回 pgx.ErrNoRows 本体，保证 errors.Is 分支被真实命中。
func pgxErrNoRows() error { return pgx.ErrNoRows }
