package infrastructure

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

// testAESKey 是固定的测试密钥；仓库在写入时用它加密 API key，
// 因此 providerRow 预置密文后 Get/List 才能解密回明文。
var testAESKey = [32]byte{
	1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
	17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32,
}

func newMockProviderRepo(mock pgxmock.PgxPoolIface) *PgProviderRepo {
	return &PgProviderRepo{
		pool:    mock,
		key:     testAESKey,
		logger:  zap.NewNop(),
		metrics: observability.NoopMetrics{},
	}
}

func providerFixture() *domain.Provider {
	return &domain.Provider{
		ID: "p1", TenantID: "t1", Name: "Qwen", Kind: domain.ProviderOpenAICompat,
		BaseURL: "https://api.test", APIKey: "sk-test", DefaultModel: "qwen-turbo", Enabled: true,
	}
}

var providerColumns = []string{"id", "tenant_id", "name", "kind", "base_url", "api_key",
	"default_model", "enabled", "created_at", "updated_at"}

func providerRow(t *testing.T, p *domain.Provider) []any {
	now := time.Now()
	// DB 行中存的是密文，Get/List 解密后应还原 p.APIKey 明文。
	apiKey, err := crypto.EncryptSecret(testAESKey, p.APIKey)
	require.NoError(t, err)
	return []any{p.ID, p.TenantID, p.Name, string(p.Kind), p.BaseURL, apiKey,
		p.DefaultModel, p.Enabled, now, now}
}

func TestProviderRepo_Create_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockProviderRepo(mock)

	p := providerFixture()
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	// api_key 参数是加密后的随机密文，无法等值匹配，用 AnyArg 校验参数个数与位置。
	mock.ExpectQuery("INSERT INTO providers").
		WithArgs(p.ID, "t1", p.Name, string(p.Kind), p.BaseURL, pgxmock.AnyArg(), p.DefaultModel, p.Enabled).
		WillReturnRows(pgxmock.NewRows([]string{"created_at", "updated_at"}).AddRow(now, now))
	mock.ExpectCommit()

	require.NoError(t, repo.Create(context.Background(), "t1", p))
	require.Equal(t, now, p.CreatedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

// prefixArg 匹配以 prefix 开头且不等于 not 的字符串参数，
// 用于断言写入 DB 的 api key 是"带前缀的密文而非明文"。
// 密文带随机 nonce 无法等值匹配，因此用结构约束代替。
type prefixArg struct {
	prefix string
	not    string
}

func (p prefixArg) Match(v any) bool {
	s, ok := v.(string)
	return ok && strings.HasPrefix(s, p.prefix) && s != p.not
}

// TestProviderRepo_Create_writesCiphertext 验证落库值不是明文：
// INSERT 的 api_key 参数必须是 enc:v1: 前缀密文且不等于明文。
func TestProviderRepo_Create_writesCiphertext(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockProviderRepo(mock)

	p := providerFixture()
	now := time.Now()

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("INSERT INTO providers").
		WithArgs(p.ID, "t1", p.Name, string(p.Kind), p.BaseURL,
			prefixArg{prefix: "enc:v1:", not: p.APIKey}, p.DefaultModel, p.Enabled).
		WillReturnRows(pgxmock.NewRows([]string{"created_at", "updated_at"}).AddRow(now, now))
	mock.ExpectCommit()

	require.NoError(t, repo.Create(context.Background(), "t1", p))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestProviderRepo_Get_legacyPlaintextFailsClosed 验证历史明文 fail closed：
// 无前缀的存量值不得按明文返回，必须报错提示重新保存。
func TestProviderRepo_Get_legacyPlaintextFailsClosed(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockProviderRepo(mock)

	p := providerFixture()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM providers WHERE id=\\$1").
		WithArgs("p1").
		WillReturnRows(pgxmock.NewRows(providerColumns).
			AddRow(p.ID, p.TenantID, p.Name, string(p.Kind), p.BaseURL, "sk-legacy-plaintext",
				p.DefaultModel, p.Enabled, time.Now(), time.Now()))
	// 解密发生在事务提交之后；SQL 层无错误，事务正常 commit。
	mock.ExpectCommit()

	_, err := repo.Get(context.Background(), "t1", "p1")
	require.ErrorContains(t, err, "请重新保存")
	require.ErrorContains(t, err, "legacy plaintext")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestProviderRepo_Get_corruptedCiphertextFailsClosed 验证损坏密文 fail closed。
func TestProviderRepo_Get_corruptedCiphertextFailsClosed(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockProviderRepo(mock)

	p := providerFixture()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM providers WHERE id=\\$1").
		WithArgs("p1").
		WillReturnRows(pgxmock.NewRows(providerColumns).
			AddRow(p.ID, p.TenantID, p.Name, string(p.Kind), p.BaseURL, "enc:v1:not-base64!!!",
				p.DefaultModel, p.Enabled, time.Now(), time.Now()))
	// 解密发生在事务提交之后；SQL 层无错误，事务正常 commit。
	mock.ExpectCommit()

	_, err := repo.Get(context.Background(), "t1", "p1")
	require.ErrorContains(t, err, "请重新保存")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProviderRepo_Create_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockProviderRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("INSERT INTO providers").
		WithArgs(anyArgs(8)...).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.Create(context.Background(), "t1", providerFixture())
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProviderRepo_Get_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockProviderRepo(mock)

	p := providerFixture()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM providers WHERE id=\\$1").
		WithArgs("p1").
		WillReturnRows(pgxmock.NewRows(providerColumns).AddRow(providerRow(t, p)...))
	mock.ExpectCommit()

	got, err := repo.Get(context.Background(), "t1", "p1")
	require.NoError(t, err)
	require.Equal(t, domain.ProviderOpenAICompat, got.Kind)
	require.Equal(t, "sk-test", got.APIKey)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProviderRepo_Get_notFound(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockProviderRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM providers WHERE id=\\$1").
		WithArgs("nope").WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	_, err := repo.Get(context.Background(), "t1", "nope")
	require.ErrorContains(t, err, "get provider")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProviderRepo_List_successAndEmpty(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rows    *pgxmock.Rows
		wantLen int
	}{
		{name: "one provider", rows: pgxmock.NewRows(providerColumns).AddRow(providerRow(t, providerFixture())...), wantLen: 1},
		{name: "empty returns empty slice", rows: pgxmock.NewRows(providerColumns), wantLen: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newFactMock(t)
			repo := newMockProviderRepo(mock)

			mock.ExpectBegin()
			mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
			mock.ExpectQuery("FROM providers ORDER BY created_at").
				WithArgs().WillReturnRows(tc.rows)
			mock.ExpectCommit()

			providers, err := repo.List(context.Background(), "t1")
			require.NoError(t, err)
			require.Len(t, providers, tc.wantLen)
			require.NotNil(t, providers)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestProviderRepo_List_queryFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockProviderRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM providers ORDER BY created_at").
		WithArgs().WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.List(context.Background(), "t1")
	require.ErrorContains(t, err, "list providers")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProviderRepo_List_scanFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockProviderRepo(mock)

	bad := providerFixture()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM providers ORDER BY created_at").
		WithArgs().
		WillReturnRows(pgxmock.NewRows(providerColumns).
			AddRow(42, bad.TenantID, bad.Name, string(bad.Kind), bad.BaseURL, bad.APIKey,
				bad.DefaultModel, bad.Enabled, time.Now(), time.Now()))
	mock.ExpectRollback()

	_, err := repo.List(context.Background(), "t1")
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProviderRepo_Update_successAndNotFound(t *testing.T) {
	for _, tc := range []struct {
		name       string
		affected   int64
		wantErrMsg string
	}{
		{name: "success", affected: 1},
		{name: "not found", affected: 0, wantErrMsg: "provider not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newFactMock(t)
			repo := newMockProviderRepo(mock)

			mock.ExpectBegin()
			mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
			mock.ExpectExec("UPDATE providers SET").
				WithArgs(anyArgs(8)...).
				WillReturnResult(pgxmock.NewResult("UPDATE", tc.affected))
			if tc.wantErrMsg != "" {
				mock.ExpectRollback()
			} else {
				mock.ExpectCommit()
			}

			err := repo.Update(context.Background(), "t1", providerFixture())
			if tc.wantErrMsg != "" {
				require.ErrorContains(t, err, tc.wantErrMsg)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// TestProviderRepo_GetMeta_legacyPlaintextStillReadable 验证存量明文 key 的
// provider 元数据仍可读取（APIKey 置空）：Update 因此能带新 key 重存，
// 不被 Get 的 fail-closed 解密锁死。
func TestProviderRepo_GetMeta_legacyPlaintextStillReadable(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockProviderRepo(mock)

	p := providerFixture()
	metaColumns := []string{"id", "tenant_id", "name", "kind", "base_url",
		"default_model", "enabled", "created_at", "updated_at"}
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("FROM providers WHERE id=\\$1").
		WithArgs("p1").
		WillReturnRows(pgxmock.NewRows(metaColumns).
			AddRow(p.ID, p.TenantID, p.Name, string(p.Kind), p.BaseURL,
				p.DefaultModel, p.Enabled, time.Now(), time.Now()))
	mock.ExpectCommit()

	got, err := repo.GetMeta(context.Background(), "t1", "p1")
	require.NoError(t, err)
	require.Equal(t, p.Name, got.Name)
	require.Equal(t, p.BaseURL, got.BaseURL)
	require.Empty(t, got.APIKey, "GetMeta 不得返回或解密 api key")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestProviderRepo_Update_keepsCiphertextWhenEmptyAPIKey 验证 APIKey 为空的
// Update 不覆盖已存储的密文（CASE WHEN $4=” THEN api_key）：改名字不丢 key。
func TestProviderRepo_Update_keepsCiphertextWhenEmptyAPIKey(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockProviderRepo(mock)

	p := providerFixture()
	p.APIKey = ""
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("UPDATE providers SET").
		WithArgs(p.Name, string(p.Kind), p.BaseURL, "", p.DefaultModel, p.Enabled, p.ID, "t1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Update(context.Background(), "t1", p))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestProviderRepo_Update_writesNewCiphertext 验证带新 APIKey 的 Update 落库为
// 前缀密文而非明文（明文重存场景的核心加密路径）。
func TestProviderRepo_Update_writesNewCiphertext(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockProviderRepo(mock)

	p := providerFixture()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("UPDATE providers SET").
		WithArgs(p.Name, string(p.Kind), p.BaseURL,
			prefixArg{prefix: "enc:v1:", not: p.APIKey}, p.DefaultModel, p.Enabled, p.ID, "t1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Update(context.Background(), "t1", p))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProviderRepo_Update_execFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockProviderRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("UPDATE providers SET").
		WithArgs(anyArgs(8)...).WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.Update(context.Background(), "t1", providerFixture())
	require.ErrorContains(t, err, "update provider")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProviderRepo_Delete_successAndNotFound(t *testing.T) {
	for _, tc := range []struct {
		name       string
		affected   int64
		wantErrMsg string
	}{
		{name: "success", affected: 1},
		{name: "not found", affected: 0, wantErrMsg: "provider not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newFactMock(t)
			repo := newMockProviderRepo(mock)

			mock.ExpectBegin()
			mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
			mock.ExpectExec("DELETE FROM providers").
				WithArgs("p1", "t1").
				WillReturnResult(pgxmock.NewResult("DELETE", tc.affected))
			if tc.wantErrMsg != "" {
				mock.ExpectRollback()
			} else {
				mock.ExpectCommit()
			}

			err := repo.Delete(context.Background(), "t1", "p1")
			if tc.wantErrMsg != "" {
				require.ErrorContains(t, err, tc.wantErrMsg)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestProviderRepo_Delete_execFails(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockProviderRepo(mock)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec("DELETE FROM providers").
		WithArgs("p1", "t1").WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := repo.Delete(context.Background(), "t1", "p1")
	require.ErrorContains(t, err, "delete provider")
	require.NoError(t, mock.ExpectationsWereMet())
}
