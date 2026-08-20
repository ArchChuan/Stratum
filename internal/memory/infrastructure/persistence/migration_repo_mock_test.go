package persistence

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

// 裸 tenant ID；ExecTenantWith 会拼出 `tenant_<id>` 的 search_path。
const migrationTestTenant = "52c9b62d-4f66-4bc4-a1b8-eed81cdae7b2"

func newMigrationRepo(t *testing.T) (pgxmock.PgxPoolIface, *MigrationRepo) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	return mock, &MigrationRepo{pool: mock}
}

func migrationRow(m *domain.MemoryMigration) *pgxmock.Rows {
	return pgxmock.NewRows([]string{
		"id", "tenant_id", "from_model", "to_model", "status", "progress", "total_facts", "created_at", "updated_at",
	}).AddRow(m.ID, m.TenantID, m.FromModel, m.ToModel, string(m.Status), m.Progress, m.TotalFacts, m.CreatedAt, m.UpdatedAt)
}

func sampleMigration() *domain.MemoryMigration {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return &domain.MemoryMigration{
		ID: 7, TenantID: migrationTestTenant, FromModel: "text-embedding-v1",
		ToModel: "text-embedding-v3", Status: domain.MigrationStatusMigrating,
		Progress: 3, TotalFacts: 10, CreatedAt: now, UpdatedAt: now,
	}
}

func expectTenantTx(mock pgxmock.PgxPoolIface) {
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SET LOCAL search_path = "tenant_` + migrationTestTenant + `", public`)).
		WillReturnResult(pgxmock.NewResult("SET", 0))
}

// freshMigration 构造一个零进度迁移（与 NewMigration 产出一致，Create 持久化
// 的是这个初始形态）。
func freshMigration() *domain.MemoryMigration {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return &domain.MemoryMigration{
		TenantID: migrationTestTenant, FromModel: "text-embedding-v1",
		ToModel: "text-embedding-v3", Status: domain.MigrationStatusMigrating,
		Progress: 0, TotalFacts: 10, CreatedAt: now, UpdatedAt: now,
	}
}

func TestMigrationRepoCreateReturnsID(t *testing.T) {
	mock, repo := newMigrationRepo(t)
	expectTenantTx(mock)
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO memory_migrations`)).
		WithArgs(migrationTestTenant, "text-embedding-v1", "text-embedding-v3", "migrating", 0, 10).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(int64(7)))
	mock.ExpectCommit()

	id, err := repo.Create(context.Background(), migrationTestTenant, freshMigration())
	require.NoError(t, err)
	require.Equal(t, int64(7), id)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMigrationRepoCreateRollsBackOnError(t *testing.T) {
	mock, repo := newMigrationRepo(t)
	expectTenantTx(mock)
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO memory_migrations`)).
		WithArgs(migrationTestTenant, "text-embedding-v1", "text-embedding-v3", "migrating", 0, 10).
		WillReturnError(errors.New("db down"))
	mock.ExpectRollback()

	_, err := repo.Create(context.Background(), migrationTestTenant, freshMigration())
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMigrationRepoGetActiveFound(t *testing.T) {
	mock, repo := newMigrationRepo(t)
	expectTenantTx(mock)
	expected := sampleMigration()
	mock.ExpectQuery(regexp.QuoteMeta("WHERE tenant_id = $1 AND status = 'migrating'")).
		WithArgs(migrationTestTenant).WillReturnRows(migrationRow(expected))
	mock.ExpectCommit()

	got, err := repo.GetActive(context.Background(), migrationTestTenant)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, expected.ID, got.ID)
	require.Equal(t, expected.FromModel, got.FromModel)
	require.Equal(t, expected.Progress, got.Progress)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMigrationRepoGetActiveNoneReturnsNil(t *testing.T) {
	mock, repo := newMigrationRepo(t)
	expectTenantTx(mock)
	mock.ExpectQuery(regexp.QuoteMeta("WHERE tenant_id = $1 AND status = 'migrating'")).
		WithArgs(migrationTestTenant).WillReturnRows(pgxmock.NewRows([]string{
		"id", "tenant_id", "from_model", "to_model", "status", "progress", "total_facts", "created_at", "updated_at",
	}))
	mock.ExpectCommit()

	got, err := repo.GetActive(context.Background(), migrationTestTenant)
	require.NoError(t, err)
	require.Nil(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMigrationRepoGetLatest(t *testing.T) {
	mock, repo := newMigrationRepo(t)
	expectTenantTx(mock)
	expected := sampleMigration()
	expected.Status = domain.MigrationStatusDone
	mock.ExpectQuery(regexp.QuoteMeta("WHERE tenant_id = $1\n\t\tORDER BY id DESC")).
		WithArgs(migrationTestTenant).WillReturnRows(migrationRow(expected))
	mock.ExpectCommit()

	got, err := repo.GetLatest(context.Background(), migrationTestTenant)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, domain.MigrationStatusDone, got.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMigrationRepoGetByIDFound(t *testing.T) {
	mock, repo := newMigrationRepo(t)
	expectTenantTx(mock)
	expected := sampleMigration()
	mock.ExpectQuery(regexp.QuoteMeta("WHERE tenant_id = $1 AND id = $2")).
		WithArgs(migrationTestTenant, int64(7)).WillReturnRows(migrationRow(expected))
	mock.ExpectCommit()

	got, err := repo.GetByID(context.Background(), migrationTestTenant, 7)
	require.NoError(t, err)
	require.Equal(t, int64(7), got.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMigrationRepoGetByIDNotFound(t *testing.T) {
	mock, repo := newMigrationRepo(t)
	expectTenantTx(mock)
	mock.ExpectQuery(regexp.QuoteMeta("WHERE tenant_id = $1 AND id = $2")).
		WithArgs(migrationTestTenant, int64(99)).WillReturnRows(pgxmock.NewRows([]string{
		"id", "tenant_id", "from_model", "to_model", "status", "progress", "total_facts", "created_at", "updated_at",
	}))
	mock.ExpectRollback()

	_, err := repo.GetByID(context.Background(), migrationTestTenant, 99)
	require.ErrorIs(t, err, domain.ErrMigrationNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMigrationRepoTransitionHit(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []any
		sql  string
		run  func(*MigrationRepo) (bool, error)
	}{
		{name: "advance", args: []any{migrationTestTenant, int64(7), 5},
			sql: `UPDATE memory_migrations SET progress = $3, updated_at = NOW()`,
			run: func(r *MigrationRepo) (bool, error) {
				return r.Advance(context.Background(), migrationTestTenant, 7, 5)
			}},
		{name: "complete", args: []any{migrationTestTenant, int64(7), "done"},
			sql: `UPDATE memory_migrations SET status = $3, updated_at = NOW()`,
			run: func(r *MigrationRepo) (bool, error) {
				return r.Complete(context.Background(), migrationTestTenant, 7, domain.MigrationStatusDone)
			}},
		{name: "restart", args: []any{migrationTestTenant, int64(7)},
			sql: `UPDATE memory_migrations SET status = 'migrating', updated_at = NOW()`,
			run: func(r *MigrationRepo) (bool, error) { return r.Restart(context.Background(), migrationTestTenant, 7) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, repo := newMigrationRepo(t)
			expectTenantTx(mock)
			mock.ExpectExec(regexp.QuoteMeta(tc.sql)).
				WithArgs(tc.args...).
				WillReturnResult(pgxmock.NewResult("UPDATE", 1))
			mock.ExpectCommit()

			hit, err := tc.run(repo)
			require.NoError(t, err)
			require.True(t, hit)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestMigrationRepoTransitionMissWhenNotActive(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []any
		run  func(*MigrationRepo) (bool, error)
	}{
		{name: "advance", args: []any{migrationTestTenant, int64(7), 5},
			run: func(r *MigrationRepo) (bool, error) {
				return r.Advance(context.Background(), migrationTestTenant, 7, 5)
			}},
		{name: "complete", args: []any{migrationTestTenant, int64(7), "done"},
			run: func(r *MigrationRepo) (bool, error) {
				return r.Complete(context.Background(), migrationTestTenant, 7, domain.MigrationStatusDone)
			}},
		{name: "restart", args: []any{migrationTestTenant, int64(7)},
			run: func(r *MigrationRepo) (bool, error) { return r.Restart(context.Background(), migrationTestTenant, 7) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, repo := newMigrationRepo(t)
			expectTenantTx(mock)
			mock.ExpectExec(regexp.QuoteMeta("UPDATE memory_migrations")).
				WithArgs(tc.args...).
				WillReturnResult(pgxmock.NewResult("UPDATE", 0))
			mock.ExpectCommit()

			hit, err := tc.run(repo)
			require.NoError(t, err)
			require.False(t, hit)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
