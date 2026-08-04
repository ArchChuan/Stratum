package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// newTokenStore 组装 pgxmock 池 + miniredis 客户端。
func newTokenStore(t *testing.T) (*TokenStore, pgxmock.PgxPoolIface, *miniredis.Miniredis) {
	mock := newMockPool(t)
	mr := miniredis.RunT(t)
	// 构造签名保持 *pgxpool.Pool 以兼容 wiring；测试注入接口实例。
	return &TokenStore{db: mock, rdb: redis.NewClient(&redis.Options{Addr: mr.Addr()})}, mock, mr
}

func TestTokenStore_Create_WithTenant(t *testing.T) {
	store, mock, _ := newTokenStore(t)
	mock.ExpectExec(`INSERT INTO refresh_tokens`).
		WithArgs(pgxmock.AnyArg(), "u1", "t1", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	require.NoError(t, store.Create(context.Background(), "u1", "t1", "raw-token-1", time.Hour))
}

func TestTokenStore_Create_WithoutTenant(t *testing.T) {
	store, mock, _ := newTokenStore(t)
	mock.ExpectExec(`INSERT INTO refresh_tokens`).
		WithArgs(pgxmock.AnyArg(), "u1", nil, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	require.NoError(t, store.Create(context.Background(), "u1", "", "raw-token-1", time.Hour))
}

func TestTokenStore_Create_ExecFails(t *testing.T) {
	store, mock, _ := newTokenStore(t)
	mock.ExpectExec(`INSERT INTO refresh_tokens`).
		WithArgs(pgxmock.AnyArg(), "u1", nil, pgxmock.AnyArg()).
		WillReturnError(errAny)

	err := store.Create(context.Background(), "u1", "", "raw-token-1", time.Hour)
	require.ErrorContains(t, err, "token_store: create")
	require.ErrorIs(t, err, errAny)
}

func TestTokenStore_Rotate_CommitsAndBlacklists(t *testing.T) {
	store, mock, mr := newTokenStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`UPDATE refresh_tokens SET revoked_at = NOW\(\)`).
		WithArgs(pgxmock.AnyArg()).
		// pgxmock 不支持 scan 到 *string；用 nil 覆盖 NULL 分支。
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "tenant_id", "expires_at"}).
			AddRow("u1", nil, time.Now().Add(time.Hour)))
	mock.ExpectExec(`INSERT INTO refresh_tokens`).
		WithArgs(pgxmock.AnyArg(), "u1", nil, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	require.NoError(t, store.Rotate(context.Background(), "old-token", "new-token", time.Hour))
	require.NotEmpty(t, mr.Keys())
}

func TestTokenStore_Rotate_BeginFails(t *testing.T) {
	store, mock, _ := newTokenStore(t)
	mock.ExpectBegin().WillReturnError(errAny)

	err := store.Rotate(context.Background(), "old", "new", time.Hour)
	require.ErrorContains(t, err, "rotate begin")
}

func TestTokenStore_Rotate_NoMatchingOldToken(t *testing.T) {
	store, mock, _ := newTokenStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`UPDATE refresh_tokens SET revoked_at = NOW\(\)`).
		WithArgs(pgxmock.AnyArg()).WillReturnError(errAny)
	mock.ExpectRollback()

	err := store.Rotate(context.Background(), "old", "new", time.Hour)
	require.ErrorContains(t, err, "rotate revoke old")
}

func TestTokenStore_Rotate_ReplacementInsertFails(t *testing.T) {
	store, mock, _ := newTokenStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`UPDATE refresh_tokens SET revoked_at = NOW\(\)`).
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "tenant_id", "expires_at"}).
			AddRow("u1", nil, time.Now().Add(time.Hour)))
	mock.ExpectExec(`INSERT INTO refresh_tokens`).
		WithArgs(pgxmock.AnyArg(), "u1", nil, pgxmock.AnyArg()).WillReturnError(errAny)
	mock.ExpectRollback()

	err := store.Rotate(context.Background(), "old", "new", time.Hour)
	require.ErrorContains(t, err, "rotate create replacement")
}

func TestTokenStore_Rotate_CommitFails(t *testing.T) {
	store, mock, _ := newTokenStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`UPDATE refresh_tokens SET revoked_at = NOW\(\)`).
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "tenant_id", "expires_at"}).
			AddRow("u1", nil, time.Now().Add(time.Hour)))
	mock.ExpectExec(`INSERT INTO refresh_tokens`).
		WithArgs(pgxmock.AnyArg(), "u1", nil, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit().WillReturnError(errAny)

	err := store.Rotate(context.Background(), "old", "new", time.Hour)
	require.ErrorContains(t, err, "rotate commit")
}

func TestTokenStore_Rotate_ExpiredTokenSkipsBlacklist(t *testing.T) {
	store, mock, mr := newTokenStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`UPDATE refresh_tokens SET revoked_at = NOW\(\)`).
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "tenant_id", "expires_at"}).
			AddRow("u1", nil, time.Now().Add(-time.Hour)))
	mock.ExpectExec(`INSERT INTO refresh_tokens`).
		WithArgs(pgxmock.AnyArg(), "u1", nil, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	require.NoError(t, store.Rotate(context.Background(), "old", "new", time.Hour))
	require.Empty(t, mr.Keys())
}

func TestTokenStore_Revoke_Blacklists(t *testing.T) {
	store, mock, mr := newTokenStore(t)
	mock.ExpectQuery(`UPDATE refresh_tokens SET revoked_at = NOW\(\)`).
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"expires_at"}).AddRow(time.Now().Add(time.Hour)))

	require.NoError(t, store.Revoke(context.Background(), "revoke-me"))
	require.Len(t, mr.Keys(), 1)
}

func TestTokenStore_Revoke_ExpiredNoBlacklist(t *testing.T) {
	store, mock, mr := newTokenStore(t)
	mock.ExpectQuery(`UPDATE refresh_tokens SET revoked_at = NOW\(\)`).
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"expires_at"}).AddRow(time.Now().Add(-time.Hour)))

	require.NoError(t, store.Revoke(context.Background(), "revoke-me"))
	require.Empty(t, mr.Keys())
}

func TestTokenStore_Revoke_QueryFails(t *testing.T) {
	store, mock, _ := newTokenStore(t)
	mock.ExpectQuery(`UPDATE refresh_tokens SET revoked_at = NOW\(\)`).
		WithArgs(pgxmock.AnyArg()).WillReturnError(errAny)

	err := store.Revoke(context.Background(), "revoke-me")
	require.ErrorContains(t, err, "token_store: revoke")
}

func TestTokenStore_GetActiveClaims(t *testing.T) {
	store, mock, _ := newTokenStore(t)
	mock.ExpectQuery(`SELECT rt.user_id, rt.tenant_id, COALESCE\(u.avatar_url, ''\), u.github_login`).
		WithArgs(pgxmock.AnyArg()).
		// pgxmock 不支持 scan 到 *string；用 nil 覆盖 NULL 分支。
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "tenant_id", "avatar_url", "github_login"}).
			AddRow("u1", nil, "https://a", "alice"))

	session, err := store.GetActiveClaims(context.Background(), "raw-token")
	require.NoError(t, err)
	require.Equal(t, "u1", session.UserID)
	require.Equal(t, "", session.TenantID)
	require.Equal(t, "alice", session.GitHubLogin)
}

func TestTokenStore_GetActiveClaims_NoTenant(t *testing.T) {
	store, mock, _ := newTokenStore(t)
	mock.ExpectQuery(`SELECT rt.user_id, rt.tenant_id, COALESCE\(u.avatar_url, ''\), u.github_login`).
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "tenant_id", "avatar_url", "github_login"}).
			AddRow("u1", nil, "", "alice"))

	session, err := store.GetActiveClaims(context.Background(), "raw-token")
	require.NoError(t, err)
	require.Equal(t, "", session.TenantID)
}

func TestTokenStore_GetActiveClaims_QueryFails(t *testing.T) {
	store, mock, _ := newTokenStore(t)
	mock.ExpectQuery(`SELECT rt.user_id, rt.tenant_id, COALESCE`).
		WithArgs(pgxmock.AnyArg()).WillReturnError(errAny)

	_, err := store.GetActiveClaims(context.Background(), "raw-token")
	require.ErrorContains(t, err, "get active claims")
}

func TestTokenStore_IsBlacklisted(t *testing.T) {
	store, _, mr := newTokenStore(t)
	require.NoError(t, mr.Set("rt:blacklist:"+hashToken("bad"), "1"))

	blacklisted, err := store.IsBlacklisted(context.Background(), "bad")
	require.NoError(t, err)
	require.True(t, blacklisted)

	missing, err := store.IsBlacklisted(context.Background(), "missing")
	require.NoError(t, err)
	require.False(t, missing)
}

func TestTokenStore_IsBlacklisted_RedisError(t *testing.T) {
	store, _, _ := newTokenStore(t)
	store.rdb = redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond})

	_, err := store.IsBlacklisted(context.Background(), "any")
	require.ErrorContains(t, err, "token_store: redis get")
}

func TestTokenStore_HashTokenStable(t *testing.T) {
	require.Equal(t, hashToken("abc"), hashToken("abc"))
	require.NotEqual(t, hashToken("abc"), hashToken("abd"))
	require.Len(t, hashToken("abc"), 64)
}
