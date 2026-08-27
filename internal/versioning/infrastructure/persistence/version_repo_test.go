package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/versioning/domain"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

// newVersionMock 与 skill persistence mock 测试同模式:mock 实现 poolIface.Begin。
func newVersionMock(t *testing.T) pgxmock.PgxPoolIface {
	t.Helper()
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	return mock
}

func beginVersionTenantTx(t *testing.T, mock pgxmock.PgxPoolIface) {
	t.Helper()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").
		WillReturnResult(pgxmock.NewResult("SET", 0))
}

func newVersionRepo(mock pgxmock.PgxPoolIface) *PgVersionRepo {
	return &PgVersionRepo{pool: mock}
}

// versionCols 对齐 listVersionsSQL 的 14 列投影。
var versionCols = []string{
	"id", "resource_kind", "resource_id", "parent_version_id", "revision_no",
	"status", "source", "content_hash", "payload", "safe_summary",
	"created_by", "created_at", "published_at", "is_current",
}

// versionRow 返回一行版本数据(payload/safe_summary 为 JSONB 字节)。
func versionRow(id string, current bool) []any {
	createdAt := time.Unix(100, 0)
	// pgxmock 对 *time.Time 扫描目标需要指针值;set=true 传指针,false 传 nil。
	publishedAt := &createdAt
	if !current {
		publishedAt = nil
	}
	return []any{
		id, "agent", "a1", "v1", 2, "published", "manual", "h-2",
		[]byte(`{"name":"assistant","temperature":0.7}`),
		[]byte(`{"kind":"agent"}`),
		"u1", createdAt, publishedAt, current,
	}
}

func TestPgVersionRepo_ListVersions_success(t *testing.T) {
	mock := newVersionMock(t)
	repo := newVersionRepo(mock)
	beginVersionTenantTx(t, mock)

	mock.ExpectQuery("FROM resource_versions r").
		WithArgs("agent", "a1").
		WillReturnRows(pgxmock.NewRows(versionCols).
			AddRow(versionRow("v2", true)...).
			AddRow(versionRow("v1", false)...))
	mock.ExpectCommit()

	rows, err := repo.ListVersions(context.Background(), "t1", domain.ResourceKindAgent, "a1")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "v2", rows[0].ID)
	require.True(t, rows[0].IsCurrent)
	require.Equal(t, domain.VersionStatusPublished, rows[0].Status)
	require.Equal(t, domain.VersionSourceManual, rows[0].Source)
	require.Equal(t, "v1", rows[0].ParentVersionID)
	require.Equal(t, 2, rows[0].RevisionNo)
	require.Equal(t, "assistant", rows[0].Payload["name"])
	require.Equal(t, "agent", rows[0].SafeSummary["kind"])
	require.NotNil(t, rows[0].PublishedAt)
	require.False(t, rows[1].IsCurrent)
	require.Equal(t, "u1", rows[1].CreatedBy)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgVersionRepo_ListVersions_queryFails(t *testing.T) {
	mock := newVersionMock(t)
	repo := newVersionRepo(mock)
	beginVersionTenantTx(t, mock)

	mock.ExpectQuery("FROM resource_versions r").
		WithArgs("agent", "a1").
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	_, err := repo.ListVersions(context.Background(), "t1", domain.ResourceKindAgent, "a1")
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgVersionRepo_ListVersions_unregisteredKindFailsClosed(t *testing.T) {
	mock := newVersionMock(t)
	repo := newVersionRepo(mock)
	beginVersionTenantTx(t, mock)

	// skill 尚未接入产品表映射 → fail-closed,不静默返回空历史。
	_, err := repo.ListVersions(context.Background(), "t1", domain.ResourceKindSkill, "s1")
	require.ErrorIs(t, err, domain.ErrVersionKindUnsupported)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgVersionRepo_GetVersion_found(t *testing.T) {
	mock := newVersionMock(t)
	repo := newVersionRepo(mock)
	beginVersionTenantTx(t, mock)

	mock.ExpectQuery("AND r.id=\\$3").
		WithArgs("agent", "a1", "v2").
		WillReturnRows(pgxmock.NewRows(versionCols).AddRow(versionRow("v2", true)...))
	mock.ExpectCommit()

	v, found, err := repo.GetVersion(context.Background(), "t1", domain.ResourceKindAgent, "a1", "v2")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "v2", v.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgVersionRepo_GetVersion_notFound(t *testing.T) {
	mock := newVersionMock(t)
	repo := newVersionRepo(mock)
	beginVersionTenantTx(t, mock)

	mock.ExpectQuery("AND r.id=\\$3").
		WithArgs("agent", "a1", "ghost").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectCommit()

	_, found, err := repo.GetVersion(context.Background(), "t1", domain.ResourceKindAgent, "a1", "ghost")
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgVersionRepo_missingTenantFailsClosed(t *testing.T) {
	mock := newVersionMock(t)
	repo := newVersionRepo(mock)

	// tenantID 为空 → 在任何 SQL 之前 fail-closed。
	_, err := repo.ListVersions(context.Background(), "", domain.ResourceKindAgent, "a1")
	require.ErrorContains(t, err, "missing tenant id")
	require.NoError(t, mock.ExpectationsWereMet())
}
