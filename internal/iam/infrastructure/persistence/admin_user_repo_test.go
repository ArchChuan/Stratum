package persistence

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
)

func newAdminUserRepo(t *testing.T) (*AdminUserRepo, pgxmock.PgxPoolIface) {
	mock := newMockPool(t)
	return NewAdminUserRepo(mock), mock
}

// 不带 global_role 的 4 列行（SearchUsers）。
func adminUserRow() *pgxmock.Rows {
	return pgxmock.NewRows([]string{"id", "username", "github_login", "avatar_url"})
}

// 带 global_role 的 5 列行（ListAdmins）。
func adminUserWithRoleRow() *pgxmock.Rows {
	return pgxmock.NewRows([]string{"id", "username", "github_login", "avatar_url", "global_role"})
}

func TestAdminUserRepo_SearchUsers(t *testing.T) {
	repo, mock := newAdminUserRepo(t)
	mock.ExpectQuery(`ILIKE \$1`).
		WithArgs("%alice%", 20).
		WillReturnRows(adminUserRow().
			AddRow("u1", "alice", "alice", "http://a.png").
			AddRow("u2", "bob", "bob", ""))

	users, err := repo.SearchUsers(context.Background(), "alice", 20)
	require.NoError(t, err)
	require.Len(t, users, 2)
	require.Equal(t, "u1", users[0].UserID)
	require.Equal(t, "alice", users[0].GitHubLogin)
	require.Equal(t, "http://a.png", users[0].AvatarURL)
}

func TestAdminUserRepo_SearchUsers_Empty(t *testing.T) {
	repo, mock := newAdminUserRepo(t)
	mock.ExpectQuery(`ILIKE \$1`).WithArgs("%zzz%", 20).WillReturnRows(adminUserRow())

	users, err := repo.SearchUsers(context.Background(), "zzz", 20)
	require.NoError(t, err)
	require.NotNil(t, users)
	require.Empty(t, users)
}

func TestAdminUserRepo_SearchUsers_Fails(t *testing.T) {
	repo, mock := newAdminUserRepo(t)
	mock.ExpectQuery(`ILIKE \$1`).WithArgs("%a%", 20).WillReturnError(errAny)

	_, err := repo.SearchUsers(context.Background(), "a", 20)
	require.ErrorIs(t, err, errAny)
}

func TestAdminUserRepo_ListAdmins(t *testing.T) {
	repo, mock := newAdminUserRepo(t)
	mock.ExpectQuery(`global_role IN \('system_admin', 'global_admin'\)`).
		WillReturnRows(adminUserWithRoleRow().
			AddRow("u1", "boss", "boss", "", domain.GlobalRoleGlobalAdmin).
			AddRow("u2", "op", "op", "", domain.GlobalRoleSystemAdmin))

	admins, err := repo.ListAdmins(context.Background())
	require.NoError(t, err)
	require.Len(t, admins, 2)
	require.Equal(t, domain.GlobalRoleGlobalAdmin, admins[0].GlobalRole)
	require.Equal(t, domain.GlobalRoleSystemAdmin, admins[1].GlobalRole)
}

func TestAdminUserRepo_ListAdmins_Empty(t *testing.T) {
	repo, mock := newAdminUserRepo(t)
	mock.ExpectQuery(`global_role IN \('system_admin', 'global_admin'\)`).
		WillReturnRows(adminUserWithRoleRow())

	admins, err := repo.ListAdmins(context.Background())
	require.NoError(t, err)
	require.NotNil(t, admins)
	require.Empty(t, admins)
}

func TestAdminUserRepo_ListAdmins_Fails(t *testing.T) {
	repo, mock := newAdminUserRepo(t)
	mock.ExpectQuery(`global_role IN`).WillReturnError(errAny)

	_, err := repo.ListAdmins(context.Background())
	require.ErrorIs(t, err, errAny)
}

func TestAdminUserRepo_SetAdminRole(t *testing.T) {
	repo, mock := newAdminUserRepo(t)
	mock.ExpectExec(`SET global_role = 'system_admin'`).
		WithArgs("u1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	require.NoError(t, repo.SetAdminRole(context.Background(), "u1"))
}

func TestAdminUserRepo_SetAdminRole_NotFound(t *testing.T) {
	repo, mock := newAdminUserRepo(t)
	mock.ExpectExec(`SET global_role = 'system_admin'`).
		WithArgs("u1").WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	require.ErrorIs(t, repo.SetAdminRole(context.Background(), "u1"), domain.ErrUserNotFound)
}

func TestAdminUserRepo_SetAdminRole_Fails(t *testing.T) {
	repo, mock := newAdminUserRepo(t)
	mock.ExpectExec(`SET global_role = 'system_admin'`).
		WithArgs("u1").WillReturnError(errAny)

	require.ErrorIs(t, repo.SetAdminRole(context.Background(), "u1"), errAny)
}

func TestAdminUserRepo_RemoveAdminRole(t *testing.T) {
	repo, mock := newAdminUserRepo(t)
	mock.ExpectExec(`SET global_role = 'user'`).
		WithArgs("u1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	require.NoError(t, repo.RemoveAdminRole(context.Background(), "u1"))
}

func TestAdminUserRepo_RemoveAdminRole_NotFound(t *testing.T) {
	repo, mock := newAdminUserRepo(t)
	mock.ExpectExec(`SET global_role = 'user'`).
		WithArgs("u1").WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	require.ErrorIs(t, repo.RemoveAdminRole(context.Background(), "u1"), domain.ErrUserNotFound)
}

func TestAdminUserRepo_RemoveAdminRole_Fails(t *testing.T) {
	repo, mock := newAdminUserRepo(t)
	mock.ExpectExec(`SET global_role = 'user'`).
		WithArgs("u1").WillReturnError(errAny)

	require.ErrorIs(t, repo.RemoveAdminRole(context.Background(), "u1"), errAny)
}

func TestAdminUserRepo_GetGlobalRole(t *testing.T) {
	repo, mock := newAdminUserRepo(t)
	mock.ExpectQuery(`SELECT global_role FROM public\.users WHERE id`).
		WithArgs("u1").
		WillReturnRows(pgxmock.NewRows([]string{"global_role"}).AddRow(domain.GlobalRoleSystemAdmin))

	role, err := repo.GetGlobalRole(context.Background(), "u1")
	require.NoError(t, err)
	require.Equal(t, domain.GlobalRoleSystemAdmin, role)
}

func TestAdminUserRepo_GetGlobalRole_NotFound(t *testing.T) {
	repo, mock := newAdminUserRepo(t)
	mock.ExpectQuery(`SELECT global_role FROM public\.users WHERE id`).
		WithArgs("u1").WillReturnError(pgx.ErrNoRows)

	_, err := repo.GetGlobalRole(context.Background(), "u1")
	require.ErrorIs(t, err, domain.ErrUserNotFound)
}

func TestAdminUserRepo_GetGlobalRole_Fails(t *testing.T) {
	repo, mock := newAdminUserRepo(t)
	mock.ExpectQuery(`SELECT global_role`).
		WithArgs("u1").WillReturnError(errAny)

	_, err := repo.GetGlobalRole(context.Background(), "u1")
	require.ErrorIs(t, err, errAny)
}
