package main

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres/postgrestest"
)

var testFixKey = [32]byte{
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
}

func nopFix(ctx context.Context, pool pgxPool, key [32]byte, logger *zap.Logger, dryRun bool) error {
	return nil
}

func TestRun_requiresPostgresURL(t *testing.T) {
	logger := zap.NewNop()
	err := run([]string{}, func(string) string { return "" }, logger, nopFix)
	require.ErrorContains(t, err, "POSTGRES_URL is required")
}

func TestRun_resolvesDataKeyFailClosed(t *testing.T) {
	logger := zap.NewNop()
	err := run([]string{}, func(k string) string {
		if k == "POSTGRES_URL" {
			return "postgres://test"
		}
		return "" // DATA_ENCRYPTION_KEY 与 JWT_PRIVATE_KEY_PEM 皆空
	}, logger, nopFix)
	require.ErrorContains(t, err, "resolve data encryption key")
}

func TestRun_flagParseError(t *testing.T) {
	logger := zap.NewNop()
	err := run([]string{"-nope"}, func(string) string { return "postgres://test" }, logger, nopFix)
	require.ErrorContains(t, err, "parse flags")
}

// TestFixProviderKeys_tenantsTableMissing 验证 public.tenants 缺失
// （relation does not exist）时视为 0 租户正常退出，不阻塞全新环境部署。
func TestFixProviderKeys_tenantsTableMissing(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)

	mock.ExpectQuery("SELECT id FROM public.tenants").
		WillReturnError(&pgconn.PgError{Code: pgCodeUndefinedTable})

	err = fixProviderKeys(context.Background(), mock, testFixKey, zap.NewNop(), false)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestFixProviderKeys_tenantListFails 验证 public.tenants 查询的其他错误必须
// 向上传播（fail closed），禁止静默漏修。
func TestFixProviderKeys_tenantListFails(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)

	mock.ExpectQuery("SELECT id FROM public.tenants").
		WillReturnError(context.DeadlineExceeded)

	err = fixProviderKeys(context.Background(), mock, testFixKey, zap.NewNop(), false)
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestFixProviderKeys_tenantSchemaMissing 验证未 provision 的租户 schema
// （relation does not exist）跳过该租户，不中断其他租户。
func TestFixProviderKeys_tenantSchemaMissing(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)

	mock.ExpectQuery("SELECT id FROM public.tenants").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("t-unprovisioned"))
	mock.ExpectQuery(`FROM "tenant_t-unprovisioned".providers`).
		WillReturnError(&pgconn.PgError{Code: pgCodeUndefinedTable})

	err = fixProviderKeys(context.Background(), mock, testFixKey, zap.NewNop(), false)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestFixProviderKeys_updateFails 验证 UPDATE 失败必须中止并传播错误。
func TestFixProviderKeys_updateFails(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)

	mock.ExpectQuery("SELECT id FROM public.tenants").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("t1"))
	mock.ExpectQuery(`FROM "tenant_t1".providers`).
		WillReturnRows(pgxmock.NewRows([]string{"id", "api_key"}).AddRow("p1", "sk-plain"))
	mock.ExpectExec(`UPDATE "tenant_t1".providers SET api_key`).
		WithArgs(pgxmock.AnyArg(), "p1").WillReturnError(context.DeadlineExceeded)

	err = fixProviderKeys(context.Background(), mock, testFixKey, zap.NewNop(), false)
	require.Error(t, err)
	require.ErrorContains(t, err, "update provider p1 (tenant t1)")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestFixProviderKeys_realRoundTrip 验证真实库链路：明文 → enc:v1: 密文，
// 密文可用同一密钥解密回明文；已加密行与空 key 行不受影响。
func TestFixProviderKeys_realRoundTrip(t *testing.T) {
	pool := postgrestest.NewPool(t)
	ctx := context.Background()
	tenantID := postgrestest.CreateTestTenant(t, pool)

	insert := func(id, apiKey string) {
		t.Helper()
		_, err := pool.Exec(ctx,
			`INSERT INTO "tenant_`+tenantID+`".providers
			 (id, tenant_id, name, kind, base_url, api_key, default_model, enabled)
			 VALUES ($1,$2,$3,$4,$5,$6,'',true)`,
			id, tenantID, id, "openai", "https://"+id+".example.com", apiKey)
		require.NoError(t, err)
	}

	encExisting, err := crypto.EncryptSecret(testFixKey, "sk-already-encrypted")
	require.NoError(t, err)
	insert("plain-1", "sk-plain-1")
	insert("plain-2", "sk-plain-2")
	insert("enc-1", encExisting)
	insert("empty-1", "")

	require.NoError(t, fixProviderKeys(ctx, pool, testFixKey, zap.NewNop(), false))

	for _, tc := range []struct {
		id     string
		want   string // 期望解密后的明文；空表示保持原样
		prefix string
	}{
		{id: "plain-1", want: "sk-plain-1", prefix: "enc:v1:"},
		{id: "plain-2", want: "sk-plain-2", prefix: "enc:v1:"},
		{id: "enc-1", want: "sk-already-encrypted", prefix: "enc:v1:"},
		{id: "empty-1", want: "", prefix: ""},
	} {
		var stored string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT api_key FROM "tenant_`+tenantID+`".providers WHERE id=$1`, tc.id).Scan(&stored))
		if tc.prefix != "" {
			require.True(t, strings.HasPrefix(stored, tc.prefix), "row %s must be ciphertext", tc.id)
			plain, err := crypto.DecryptSecret(testFixKey, stored)
			require.NoError(t, err)
			require.Equal(t, tc.want, plain, "row %s must decrypt back", tc.id)
		} else {
			require.Equal(t, "", stored, "empty api key must stay untouched")
		}
	}
}

// TestFixProviderKeys_dryRun 验证 --dry-run 只预览不写入。
func TestFixProviderKeys_dryRun(t *testing.T) {
	pool := postgrestest.NewPool(t)
	ctx := context.Background()
	tenantID := postgrestest.CreateTestTenant(t, pool)

	_, err := pool.Exec(ctx,
		`INSERT INTO "tenant_`+tenantID+`".providers
		 (id, tenant_id, name, kind, base_url, api_key, default_model, enabled)
		 VALUES ($1,$2,$3,$4,$5,$6,'',true)`,
		"plain-1", tenantID, "plain-1", "openai", "https://plain-1.example.com", "sk-plain-1")
	require.NoError(t, err)

	require.NoError(t, fixProviderKeys(ctx, pool, testFixKey, zap.NewNop(), true))

	var stored string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT api_key FROM "tenant_`+tenantID+`".providers WHERE id=$1`, "plain-1").Scan(&stored))
	require.Equal(t, "sk-plain-1", stored, "dry-run must not rewrite")
}
