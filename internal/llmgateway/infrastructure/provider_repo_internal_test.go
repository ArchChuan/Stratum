package infrastructure

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

func newMockProviderRepo(mock pgxmock.PgxPoolIface) *PgProviderRepo {
	return &PgProviderRepo{pool: mock}
}

func providerFixture() *domain.Provider {
	return &domain.Provider{
		ID: "p1", TenantID: "t1", Name: "Qwen", Kind: domain.ProviderOpenAICompat,
		BaseURL: "https://api.test", APIKey: "sk-test", DefaultModel: "qwen-turbo", Enabled: true,
	}
}

var providerColumns = []string{"id", "tenant_id", "name", "kind", "base_url", "api_key",
	"default_model", "enabled", "created_at", "updated_at"}

func providerRow(p *domain.Provider) []any {
	now := time.Now()
	return []any{p.ID, p.TenantID, p.Name, string(p.Kind), p.BaseURL, p.APIKey,
		p.DefaultModel, p.Enabled, now, now}
}

func TestProviderRepo_Create_success(t *testing.T) {
	mock := newFactMock(t)
	repo := newMockProviderRepo(mock)

	p := providerFixture()
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery("INSERT INTO providers").
		WithArgs(p.ID, "t1", p.Name, string(p.Kind), p.BaseURL, p.APIKey, p.DefaultModel, p.Enabled).
		WillReturnRows(pgxmock.NewRows([]string{"created_at", "updated_at"}).AddRow(now, now))
	mock.ExpectCommit()

	require.NoError(t, repo.Create(context.Background(), "t1", p))
	require.Equal(t, now, p.CreatedAt)
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
		WillReturnRows(pgxmock.NewRows(providerColumns).AddRow(providerRow(p)...))
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
		{name: "one provider", rows: pgxmock.NewRows(providerColumns).AddRow(providerRow(providerFixture())...), wantLen: 1},
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
