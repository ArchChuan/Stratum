package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

// pgxmock.PgxPoolIface satisfies the package-level pgxPool interface
// (BeginTx/Exec/Query/QueryRow), so tests inject the mock directly.

func newIAMMock(t *testing.T) pgxmock.PgxPoolIface {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	return mock
}

func pgErr(code string) error { return &pgconn.PgError{Code: code} }

// --- pure helpers ---

func TestTenantSlug(t *testing.T) {
	require.Equal(t, "preferred", tenantSlug("any-id", "preferred"))
	require.Equal(t, "tid-1", tenantSlug("tid-1", ""))
}

func TestIsRelationNotFound(t *testing.T) {
	require.True(t, isRelationNotFound(pgErr("42P01")))
	require.False(t, isRelationNotFound(pgErr("23505")))
	require.False(t, isRelationNotFound(errors.New("plain")))
	require.False(t, isRelationNotFound(nil))
}

func TestIsColumnNotFound(t *testing.T) {
	require.True(t, isColumnNotFound(pgErr("42703")))
	require.False(t, isColumnNotFound(pgErr("42P01")))
	require.False(t, isColumnNotFound(errors.New("plain")))
	require.False(t, isColumnNotFound(nil))
}

// --- CreateTenant ---

func TestCreateTenant_success(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO users").
		WithArgs("42", "alice", "http://avatar", "a@b.c").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("u1"))
	mock.ExpectExec("INSERT INTO tenants").
		WithArgs(pgxmock.AnyArg(), "Acme", pgxmock.AnyArg(), "acme-org").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO tenant_members").
		WithArgs(pgxmock.AnyArg(), "u1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("CREATE SCHEMA IF NOT EXISTS").
		WillReturnResult(pgxmock.NewResult("CREATE SCHEMA", 1))
	mock.ExpectCommit()

	res, err := repo.CreateTenant(context.Background(), domain.CreateTenantInput{
		GitHubID: 42, GitHubLogin: "alice", AvatarURL: "http://avatar", Email: "a@b.c", Name: "Acme", GitHubOrg: "acme-org",
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.TenantID)
	require.Equal(t, "tenant_"+res.TenantID, res.SchemaName)
	require.Equal(t, "u1", res.UserUUID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateTenant_beginFails(t *testing.T) {
	mock := newIAMMock(t)
	mock.ExpectBegin().WillReturnError(pgx.ErrTxClosed)
	repo := NewOnboardRepo(mock)

	_, err := repo.CreateTenant(context.Background(), domain.CreateTenantInput{GitHubID: 1})
	require.ErrorContains(t, err, "begin tx")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateTenant_upsertFails(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO users").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.CreateTenant(context.Background(), domain.CreateTenantInput{GitHubID: 1})
	require.ErrorContains(t, err, "upsert user")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateTenant_memberFails(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO users").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("u1"))
	mock.ExpectExec("INSERT INTO tenants").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO tenant_members").
		WithArgs(pgxmock.AnyArg(), "u1").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.CreateTenant(context.Background(), domain.CreateTenantInput{GitHubID: 1})
	require.ErrorContains(t, err, "insert tenant_member")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateTenant_commitFails(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO users").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("u1"))
	mock.ExpectExec("INSERT INTO tenants").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO tenant_members").
		WithArgs(pgxmock.AnyArg(), "u1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("CREATE SCHEMA IF NOT EXISTS").
		WillReturnResult(pgxmock.NewResult("CREATE SCHEMA", 1))
	mock.ExpectCommit().WillReturnError(pgx.ErrTxClosed)

	_, err := repo.CreateTenant(context.Background(), domain.CreateTenantInput{GitHubID: 1})
	require.ErrorContains(t, err, "commit")
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- CreateTenantForUser ---

func TestCreateTenantForUser_success(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO tenants").
		WithArgs(pgxmock.AnyArg(), "Team", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO tenant_members").
		WithArgs(pgxmock.AnyArg(), "u9").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("CREATE SCHEMA IF NOT EXISTS").
		WillReturnResult(pgxmock.NewResult("CREATE SCHEMA", 1))
	mock.ExpectCommit()

	tid, err := repo.CreateTenantForUser(context.Background(), "u9", "Team")
	require.NoError(t, err)
	require.NotEmpty(t, tid)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateTenantForUser_schemaFails(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO tenants").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO tenant_members").
		WithArgs(pgxmock.AnyArg(), "u9").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("CREATE SCHEMA IF NOT EXISTS").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.CreateTenantForUser(context.Background(), "u9", "Team")
	require.ErrorContains(t, err, "create schema")
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- GetUserTenant / FindUsernameByUserID / GetUserTenantByUserID ---

func TestGetUserTenant_success(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("LEFT JOIN tenant_members").
		WithArgs("42").
		WillReturnRows(pgxmock.NewRows([]string{"id", "tid"}).AddRow("u1", "t1"))

	uid, tid, ok, err := repo.GetUserTenant(context.Background(), "42")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "u1", uid)
	require.Equal(t, "t1", tid)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUserTenant_noRows(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("LEFT JOIN tenant_members").
		WithArgs("999").
		WillReturnError(pgx.ErrNoRows)

	_, _, ok, err := repo.GetUserTenant(context.Background(), "999")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUserTenant_queryFails(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("LEFT JOIN tenant_members").
		WithArgs("42").
		WillReturnError(pgx.ErrTxClosed)

	_, _, _, err := repo.GetUserTenant(context.Background(), "42")
	require.ErrorContains(t, err, "get user tenant")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFindUsernameByUserID_success(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("COALESCE\\(username").
		WithArgs("u1").
		WillReturnRows(pgxmock.NewRows([]string{"username"}).AddRow("alice"))

	name, err := repo.FindUsernameByUserID(context.Background(), "u1")
	require.NoError(t, err)
	require.Equal(t, "alice", name)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFindUsernameByUserID_noRowsReturnsEmpty(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("COALESCE\\(username").
		WithArgs("u1").
		WillReturnError(pgx.ErrNoRows)

	name, err := repo.FindUsernameByUserID(context.Background(), "u1")
	require.NoError(t, err)
	require.Empty(t, name)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUserTenantByUserID_success(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	// 选择顺序必须符合登录规格：非默认(自己创建 owner 优先、其次加入) -> 默认；
	// 同优先级按租户创建时间正序，且排除已软删除租户。
	mock.ExpectQuery("ORDER BY t\\.is_default ASC, \\(tm\\.role = 'owner'\\) DESC, t\\.created_at ASC\\s+LIMIT 1").
		WithArgs("u1").
		WillReturnRows(pgxmock.NewRows([]string{"tid", "role"}).AddRow("t1", "member"))

	tid, role, err := repo.GetUserTenantByUserID(context.Background(), "u1")
	require.NoError(t, err)
	require.Equal(t, "t1", tid)
	require.Equal(t, "member", role)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUserTenantByUserID_filtersDeletedTenants(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("AND t\\.deleted_at IS NULL").
		WithArgs("u1").
		WillReturnRows(pgxmock.NewRows([]string{"tid", "role"}).AddRow("t1", "owner"))

	tid, role, err := repo.GetUserTenantByUserID(context.Background(), "u1")
	require.NoError(t, err)
	require.Equal(t, "t1", tid)
	require.Equal(t, "owner", role)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUserTenantByUserID_noMembership(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("ORDER BY t\\.is_default ASC").
		WithArgs("u1").
		WillReturnError(pgx.ErrNoRows)

	_, _, err := repo.GetUserTenantByUserID(context.Background(), "u1")
	require.ErrorIs(t, err, domain.ErrMemberNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUserTenantByUserID_queryFails(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("ORDER BY t\\.is_default ASC").
		WithArgs("u1").
		WillReturnError(pgx.ErrTxClosed)

	_, _, err := repo.GetUserTenantByUserID(context.Background(), "u1")
	require.ErrorContains(t, err, "get user tenant by id")
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- GetUserTenants ---

var tenantInfoColumns = []string{"tid", "name", "is_default", "role", "created_at"}

func TestGetUserTenants_success(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery("FROM users WHERE github_id").
		WithArgs("42").
		WillReturnRows(pgxmock.NewRows([]string{"id", "gr"}).AddRow("u1", "admin"))
	mock.ExpectQuery("ORDER BY t\\.is_default DESC").
		WithArgs("u1").
		WillReturnRows(pgxmock.NewRows(tenantInfoColumns).
			AddRow("t1", "Default", true, "owner", created).
			AddRow("t2", "Acme", false, "member", created))

	uid, gr, tenants, ok, err := repo.GetUserTenants(context.Background(), "42")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "u1", uid)
	require.Equal(t, "admin", gr)
	require.Len(t, tenants, 2)
	require.True(t, tenants[0].IsDefault)
	require.Equal(t, "member", tenants[1].Role)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUserTenants_userNotFound(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("FROM users WHERE github_id").
		WithArgs("999").
		WillReturnError(pgx.ErrNoRows)

	_, _, _, ok, err := repo.GetUserTenants(context.Background(), "999")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUserTenants_listFails(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("FROM users WHERE github_id").
		WithArgs("42").
		WillReturnRows(pgxmock.NewRows([]string{"id", "gr"}).AddRow("u1", ""))
	mock.ExpectQuery("ORDER BY t\\.is_default DESC").
		WithArgs("u1").
		WillReturnError(pgx.ErrTxClosed)

	_, _, _, _, err := repo.GetUserTenants(context.Background(), "42")
	require.ErrorContains(t, err, "list tenants")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUserTenants_scanFails(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	// is_default column receives an int, not bool -> Scan error inside the loop.
	mock.ExpectQuery("FROM users WHERE github_id").
		WithArgs("42").
		WillReturnRows(pgxmock.NewRows([]string{"id", "gr"}).AddRow("u1", ""))
	mock.ExpectQuery("ORDER BY t\\.is_default DESC").
		WithArgs("u1").
		WillReturnRows(pgxmock.NewRows(tenantInfoColumns).AddRow("t1", "Default", 1, "owner", time.Now()))

	_, _, _, _, err := repo.GetUserTenants(context.Background(), "42")
	require.ErrorContains(t, err, "scan tenant")
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- SetGlobalRole / GetGlobalRole ---

func TestSetGlobalRole(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectExec("UPDATE users SET global_role").
		WithArgs("admin", "u1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	require.NoError(t, repo.SetGlobalRole(context.Background(), "u1", "admin"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSetGlobalRole_execFails(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectExec("UPDATE users SET global_role").
		WithArgs("admin", "u1").
		WillReturnError(pgx.ErrTxClosed)
	err := repo.SetGlobalRole(context.Background(), "u1", "admin")
	require.ErrorContains(t, err, "set global role")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetGlobalRole(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("COALESCE\\(global_role").
		WithArgs("u1").
		WillReturnRows(pgxmock.NewRows([]string{"role"}).AddRow("admin"))
	role, err := repo.GetGlobalRole(context.Background(), "u1")
	require.NoError(t, err)
	require.Equal(t, "admin", role)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetGlobalRole_queryFails(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("COALESCE\\(global_role").
		WithArgs("u1").
		WillReturnError(pgx.ErrTxClosed)
	_, err := repo.GetGlobalRole(context.Background(), "u1")
	require.ErrorContains(t, err, "get global role")
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- AutoJoinDefaultTenant ---

func autoJoinExpectations(mock pgxmock.PgxPoolIface, login string, role string) {
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO users").
		WithArgs("7", login, "http://avatar", "b@c.d").
		WillReturnRows(pgxmock.NewRows([]string{"id", "gr"}).AddRow("u2", "admin"))
	mock.ExpectQuery("WHERE is_default = true").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("t1"))
	mock.ExpectExec("INSERT INTO tenant_members").
		WithArgs("t1", "u2", role).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()
}

func TestAutoJoinDefaultTenant_member(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)
	autoJoinExpectations(mock, "bob", "member")

	uid, tid, gr, err := repo.AutoJoinDefaultTenant(context.Background(), domain.AutoJoinInput{
		GitHubID: 7, GitHubLogin: "bob", AvatarURL: "http://avatar", Email: "b@c.d",
	})
	require.NoError(t, err)
	require.Equal(t, "u2", uid)
	require.Equal(t, "t1", tid)
	require.Equal(t, "admin", gr)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAutoJoinDefaultTenant_ownerBranch(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)
	// GlobalAdminLogin matches (case-insensitive) -> role promoted to owner.
	autoJoinExpectations(mock, "Bob", "owner")

	_, _, _, err := repo.AutoJoinDefaultTenant(context.Background(), domain.AutoJoinInput{
		GitHubID: 7, GitHubLogin: "Bob", AvatarURL: "http://avatar", Email: "b@c.d", GlobalAdminLogin: "BOB",
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAutoJoinDefaultTenant_noDefaultTenant(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO users").
		WithArgs("7", "", "", "").
		WillReturnRows(pgxmock.NewRows([]string{"id", "gr"}).AddRow("u2", ""))
	mock.ExpectQuery("WHERE is_default = true").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	_, _, _, err := repo.AutoJoinDefaultTenant(context.Background(), domain.AutoJoinInput{GitHubID: 7})
	require.ErrorContains(t, err, "default tenant not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- GetTenantRole / IsMember ---

func TestGetTenantRole_success(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("COALESCE\\(role, 'member'\\) FROM tenant_members").
		WithArgs("u1", "t1").
		WillReturnRows(pgxmock.NewRows([]string{"role"}).AddRow("owner"))
	role, err := repo.GetTenantRole(context.Background(), "u1", "t1")
	require.NoError(t, err)
	require.Equal(t, "owner", role)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTenantRole_notMember(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("COALESCE\\(role, 'member'\\) FROM tenant_members").
		WithArgs("u1", "t1").
		WillReturnError(pgx.ErrNoRows)
	_, err := repo.GetTenantRole(context.Background(), "u1", "t1")
	require.ErrorIs(t, err, domain.ErrMemberNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTenantRole_fallbackOnError(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("COALESCE\\(role, 'member'\\) FROM tenant_members").
		WithArgs("u1", "t1").
		WillReturnError(pgx.ErrTxClosed)
	// Documented behavior: falls back to "member" while still surfacing the error.
	role, err := repo.GetTenantRole(context.Background(), "u1", "t1")
	require.Equal(t, "member", role)
	require.ErrorContains(t, err, "get tenant role")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIsMember_true(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("COUNT\\(\\*\\) FROM tenant_members").
		WithArgs("u1", "t1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	ok, err := repo.IsMember(context.Background(), "u1", "t1")
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIsMember_false(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("COUNT\\(\\*\\) FROM tenant_members").
		WithArgs("u1", "t1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	ok, err := repo.IsMember(context.Background(), "u1", "t1")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIsMember_queryFails(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("COUNT\\(\\*\\) FROM tenant_members").
		WithArgs("u1", "t1").
		WillReturnError(pgx.ErrTxClosed)
	_, err := repo.IsMember(context.Background(), "u1", "t1")
	require.ErrorContains(t, err, "is member")
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- CreateGuestSandboxTenant (with retry) ---

func TestCreateGuestSandboxTenant_success(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	expires := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO users").
		WithArgs("guest:g1", "guest-login", "http://avatar", expires).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("g1"))
	mock.ExpectExec("INSERT INTO tenants").
		WithArgs(pgxmock.AnyArg(), "guest-login", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO tenant_members").
		WithArgs(pgxmock.AnyArg(), "g1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	uid, tid, err := repo.CreateGuestSandboxTenant(context.Background(), "guest:g1", "guest-login", "http://avatar", expires)
	require.NoError(t, err)
	require.Equal(t, "g1", uid)
	require.NotEmpty(t, tid)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateGuestSandboxTenant_retriesOnSerialization(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	expires := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	// First attempt dies with a retryable 40001 serialization failure.
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO users").
		WithArgs("guest:g1", "guest-login", "http://avatar", expires).
		WillReturnError(pgErr("40001"))
	mock.ExpectRollback()
	// Second attempt succeeds end-to-end.
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO users").
		WithArgs("guest:g1", "guest-login", "http://avatar", expires).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("g1"))
	mock.ExpectExec("INSERT INTO tenants").
		WithArgs(pgxmock.AnyArg(), "guest-login", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO tenant_members").
		WithArgs(pgxmock.AnyArg(), "g1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	uid, tid, err := repo.CreateGuestSandboxTenant(context.Background(), "guest:g1", "guest-login", "http://avatar", expires)
	require.NoError(t, err)
	require.Equal(t, "g1", uid)
	require.NotEmpty(t, tid)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateGuestSandboxTenant_nonRetryable(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	// A non-retryable error (unique violation) must not be retried.
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO users").
		WithArgs("guest:g1", "l", "a", pgxmock.AnyArg()).
		WillReturnError(pgErr("23505"))
	mock.ExpectRollback()

	_, _, err := repo.CreateGuestSandboxTenant(context.Background(), "guest:g1", "l", "a", time.Now())
	require.ErrorContains(t, err, "insert guest user")
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- ListExpiredGuests / ListOwnedNonDefaultTenants ---

func TestListExpiredGuests_success(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	now := time.Now()
	mock.ExpectQuery("expires_at <= \\$1").
		WithArgs(now).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("g1").AddRow("g2"))
	ids, err := repo.ListExpiredGuests(context.Background(), now)
	require.NoError(t, err)
	require.Equal(t, []string{"g1", "g2"}, ids)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListExpiredGuests_queryFails(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("expires_at <= \\$1").
		WithArgs(pgxmock.AnyArg()).
		WillReturnError(pgx.ErrTxClosed)
	_, err := repo.ListExpiredGuests(context.Background(), time.Now())
	require.ErrorContains(t, err, "list expired guests")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListExpiredGuests_scanFails(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	// int cannot scan into *string -> per-row Scan error.
	mock.ExpectQuery("expires_at <= \\$1").
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(42))
	_, err := repo.ListExpiredGuests(context.Background(), time.Now())
	require.ErrorContains(t, err, "scan guest id")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListOwnedNonDefaultTenants_success(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("t\\.is_default = false").
		WithArgs("u1").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("t2").AddRow("t3"))
	ids, err := repo.ListOwnedNonDefaultTenants(context.Background(), "u1")
	require.NoError(t, err)
	require.Equal(t, []string{"t2", "t3"}, ids)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListOwnedNonDefaultTenants_queryFails(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("t\\.is_default = false").
		WithArgs("u1").
		WillReturnError(pgx.ErrTxClosed)
	_, err := repo.ListOwnedNonDefaultTenants(context.Background(), "u1")
	require.ErrorContains(t, err, "list owned tenants")
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- DeleteUser ---

func TestDeleteUser_success(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectExec("DELETE FROM tenant_invitations").
		WithArgs("u1").
		WillReturnResult(pgxmock.NewResult("DELETE", 2))
	mock.ExpectExec("UPDATE tenant_members SET invited_by = NULL").
		WithArgs("u1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("DELETE FROM users WHERE id = \\$1").
		WithArgs("u1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	require.NoError(t, repo.DeleteUser(context.Background(), "u1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteUser_skipsMissingRelation(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	// tenant_invitations does not exist yet (42P01): cleanup skipped, deletion proceeds.
	mock.ExpectExec("DELETE FROM tenant_invitations").
		WithArgs("u1").
		WillReturnError(pgErr("42P01"))
	mock.ExpectExec("UPDATE tenant_members SET invited_by = NULL").
		WithArgs("u1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("DELETE FROM users WHERE id = \\$1").
		WithArgs("u1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	require.NoError(t, repo.DeleteUser(context.Background(), "u1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteUser_skipsMissingColumn(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectExec("DELETE FROM tenant_invitations").
		WithArgs("u1").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	// invited_by column not yet migrated (42703): cleanup skipped.
	mock.ExpectExec("UPDATE tenant_members SET invited_by = NULL").
		WithArgs("u1").
		WillReturnError(pgErr("42703"))
	mock.ExpectExec("DELETE FROM users WHERE id = \\$1").
		WithArgs("u1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	require.NoError(t, repo.DeleteUser(context.Background(), "u1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteUser_invitationCleanupFails(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectExec("DELETE FROM tenant_invitations").
		WithArgs("u1").
		WillReturnError(pgx.ErrTxClosed)
	err := repo.DeleteUser(context.Background(), "u1")
	require.ErrorContains(t, err, "cleanup invitations")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteUser_memberCleanupFails(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectExec("DELETE FROM tenant_invitations").
		WithArgs("u1").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectExec("UPDATE tenant_members SET invited_by = NULL").
		WithArgs("u1").
		WillReturnError(pgx.ErrTxClosed)
	err := repo.DeleteUser(context.Background(), "u1")
	require.ErrorContains(t, err, "cleanup member invited_by")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteUser_userDeleteFails(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectExec("DELETE FROM tenant_invitations").
		WithArgs("u1").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectExec("UPDATE tenant_members SET invited_by = NULL").
		WithArgs("u1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectExec("DELETE FROM users WHERE id = \\$1").
		WithArgs("u1").
		WillReturnError(pgx.ErrTxClosed)
	err := repo.DeleteUser(context.Background(), "u1")
	require.ErrorContains(t, err, "delete user")
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- RegisterByUsername ---

func TestRegisterByUsername_success(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO users").
		WithArgs("local:alice", "alice", "alice", "hash").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("u1"))
	mock.ExpectQuery("WHERE is_default = true").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("t1"))
	mock.ExpectExec("INSERT INTO tenant_members").
		WithArgs("t1", "u1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	uid, tid, err := repo.RegisterByUsername(context.Background(), "alice", "hash")
	require.NoError(t, err)
	require.Equal(t, "u1", uid)
	require.Equal(t, "t1", tid)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRegisterByUsername_taken(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO users").
		WithArgs("local:alice", "alice", "alice", "hash").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	_, _, err := repo.RegisterByUsername(context.Background(), "alice", "hash")
	require.ErrorIs(t, err, domain.ErrUsernameTaken)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- FindByUsername / FindByUsernameWithLogin / UpdateProfile ---

func TestFindByUsernameWithLogin_success(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("FROM users WHERE username = \\$1").
		WithArgs("alice").
		WillReturnRows(pgxmock.NewRows([]string{"id", "ph", "gl", "gr"}).AddRow("u1", "hash", "alice", "admin"))
	uid, ph, gl, gr, found, err := repo.FindByUsernameWithLogin(context.Background(), "alice")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "u1", uid)
	require.Equal(t, "hash", ph)
	require.Equal(t, "alice", gl)
	require.Equal(t, "admin", gr)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFindByUsernameWithLogin_notFound(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("FROM users WHERE username = \\$1").
		WithArgs("nobody").
		WillReturnError(pgx.ErrNoRows)
	_, _, _, _, found, err := repo.FindByUsernameWithLogin(context.Background(), "nobody")
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFindByUsernameWithLogin_queryFails(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("FROM users WHERE username = \\$1").
		WithArgs("alice").
		WillReturnError(pgx.ErrTxClosed)
	_, _, _, _, _, err := repo.FindByUsernameWithLogin(context.Background(), "alice")
	require.ErrorContains(t, err, "find by username")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFindByUsername_delegates(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectQuery("FROM users WHERE username = \\$1").
		WithArgs("alice").
		WillReturnRows(pgxmock.NewRows([]string{"id", "ph", "gl", "gr"}).AddRow("u1", "hash", "alice", ""))
	uid, ph, gr, found, err := repo.FindByUsername(context.Background(), "alice")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "u1", uid)
	require.Equal(t, "hash", ph)
	require.Equal(t, "", gr)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateProfile_userIDRequired(t *testing.T) {
	repo := NewOnboardRepo(newIAMMock(t))
	err := repo.UpdateProfile(context.Background(), "", "name", "")
	require.ErrorContains(t, err, "userID required")
}

func TestUpdateProfile_noFieldsIsNoOp(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)
	// No SQL expected: empty displayName and avatarURL short-circuit.
	require.NoError(t, repo.UpdateProfile(context.Background(), "u1", "", ""))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateProfile_displayNameOnly(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectExec("UPDATE users SET github_login = \\$1 WHERE id = \\$2").
		WithArgs("new-name", "u1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	require.NoError(t, repo.UpdateProfile(context.Background(), "u1", "new-name", ""))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateProfile_avatarOnly(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectExec("UPDATE users SET avatar_url = \\$1 WHERE id = \\$2").
		WithArgs("http://new", "u1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	require.NoError(t, repo.UpdateProfile(context.Background(), "u1", "", "http://new"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateProfile_bothFields(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectExec("UPDATE users SET github_login = \\$1, avatar_url = \\$2 WHERE id = \\$3").
		WithArgs("new-name", "http://new", "u1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	require.NoError(t, repo.UpdateProfile(context.Background(), "u1", "new-name", "http://new"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateProfile_execFails(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)

	mock.ExpectExec("UPDATE users SET github_login = \\$1 WHERE id = \\$2").
		WithArgs("new-name", "u1").
		WillReturnError(pgx.ErrTxClosed)
	err := repo.UpdateProfile(context.Background(), "u1", "new-name", "")
	require.ErrorContains(t, err, "update profile")
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- TenantIsActive ---

func TestOnboardRepo_TenantIsActive(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM public.tenants`).
		WithArgs("t1").WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	active, err := repo.TenantIsActive(context.Background(), "t1")
	require.NoError(t, err)
	require.True(t, active)
}

func TestOnboardRepo_TenantIsActive_NotActive(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM public.tenants`).
		WithArgs("t1").WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	active, err := repo.TenantIsActive(context.Background(), "t1")
	require.NoError(t, err)
	require.False(t, active)
}

func TestOnboardRepo_TenantIsActive_Error(t *testing.T) {
	mock := newIAMMock(t)
	repo := NewOnboardRepo(mock)
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM public.tenants`).
		WithArgs("t1").WillReturnError(pgx.ErrTxClosed)
	_, err := repo.TenantIsActive(context.Background(), "t1")
	require.Error(t, err)
}
