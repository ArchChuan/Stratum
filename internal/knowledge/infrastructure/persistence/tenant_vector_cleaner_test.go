package persistence

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/pkg/constants"
	storagemilvus "github.com/byteBuilderX/stratum/pkg/storage/milvus"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fakeDropper records the collections it was asked to delete and the prefixes
// it was asked to list, so tests can assert which collections cleanup targets
// without a live Milvus.
type fakeDropper struct {
	deleted        []string
	lists          map[string][]string
	listCalls      []string
	err            error
	listErr        error
	notFoundPrefix string // 命中该前缀的删除返回 ErrCollectionNotFound（模拟存量集合缺失）
}

func (f *fakeDropper) DeleteCollection(_ context.Context, name string) error {
	f.deleted = append(f.deleted, name)
	if f.notFoundPrefix != "" && strings.HasPrefix(name, f.notFoundPrefix) {
		return storagemilvus.ErrCollectionNotFound
	}
	return f.err
}

func (f *fakeDropper) ListCollections(_ context.Context, prefix string) ([]string, error) {
	f.listCalls = append(f.listCalls, prefix)
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.lists[prefix], nil
}

func newVectorCleaner(mock pgxmock.PgxPoolIface, dropper collectionDropper) *TenantVectorCleaner {
	return &TenantVectorCleaner{pool: mock, vs: dropper, logger: zap.NewNop()}
}

// TestDropTenantCollections_invalidTenantID asserts fail-closed validation:
// the schema name is built from tenantID, so anything that is not a UUID is
// rejected before any SQL runs or any collection is touched.
func TestDropTenantCollections_invalidTenantID(t *testing.T) {
	for _, tc := range []struct {
		name     string
		tenantID string
	}{
		{name: "empty", tenantID: ""},
		{name: "short", tenantID: "abc123"},
		{name: "non UUID", tenantID: "550e8400e29b41d4a716446655440000"},
		{name: "uppercase UUID", tenantID: "550E8400-E29B-41D4-A716-446655440000"},
		{name: "schema prefix", tenantID: "tenant_550e8400-e29b-41d4-a716-446655440000"},
		{name: "sql injection", tenantID: `x'; DROP TABLE rag_workspaces; --`},
		{name: "quoted schema", tenantID: `tenant_x"; DROP SCHEMA public CASCADE; --`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRepoMock(t)
			dropper := &fakeDropper{}
			cleaner := newVectorCleaner(mock, dropper)
			// Zero SQL expectations: any pool access would fail the test.
			err := cleaner.DropTenantCollections(context.Background(), tc.tenantID)
			require.ErrorContains(t, err, "invalid tenantID")
			require.Empty(t, dropper.deleted)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestDropTenantCollections_validTenantNoWorkspaces(t *testing.T) {
	mock := newRepoMock(t)
	dropper := &fakeDropper{}
	cleaner := newVectorCleaner(mock, dropper)

	// The rag_workspaces lookup must go through the tenant boundary
	// (search_path isolation) instead of interpolating the schema name.
	repoBeginTenant(mock)
	mock.ExpectQuery("SELECT id FROM rag_workspaces").
		WillReturnRows(pgxmock.NewRows([]string{"id"}))
	mock.ExpectCommit()

	err := cleaner.DropTenantCollections(context.Background(), "550e8400-e29b-41d4-a716-446655440000")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	// Fixed-name collections are always dropped; no workspace collections.
	require.Equal(t, []string{
		"memory_550e8400_e29b_41d4_a716_446655440000",
		"memory_facts_550e8400_e29b_41d4_a716_446655440000",
		"tenant_550e8400_e29b_41d4_a716_446655440000_kb",
	}, dropper.deleted)
}

func TestDropTenantCollections_workspaceCollectionsDropped(t *testing.T) {
	mock := newRepoMock(t)
	dropper := &fakeDropper{lists: map[string][]string{
		"kb_ws_1_": {"kb_ws_1_text_embedding_v3", "kb_ws_1_embedding_3"},
		"kb_ws_2_": {"kb_ws_2_text_embedding_v2"},
	}}
	cleaner := newVectorCleaner(mock, dropper)
	tenantID := "550e8400-e29b-41d4-a716-446655440000"

	repoBeginTenant(mock)
	mock.ExpectQuery("SELECT id FROM rag_workspaces").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).
			AddRow("ws-1").
			AddRow("ws-2"))
	mock.ExpectCommit()

	err := cleaner.DropTenantCollections(context.Background(), tenantID)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	// Per workspace: legacy name first, then every model-suffixed name listed
	// by prefix (kb_<san(wsID)>_，含尾下划线，避免 ws-1 误匹配 ws-10)。
	require.Equal(t, []string{
		"memory_550e8400_e29b_41d4_a716_446655440000",
		"memory_facts_550e8400_e29b_41d4_a716_446655440000",
		"tenant_550e8400_e29b_41d4_a716_446655440000_kb",
		constants.CollectionLegacyName(tenantID, "ws-1"),
		"kb_ws_1_text_embedding_v3",
		"kb_ws_1_embedding_3",
		constants.CollectionLegacyName(tenantID, "ws-2"),
		"kb_ws_2_text_embedding_v2",
	}, dropper.deleted)
	require.Equal(t, []string{"kb_ws_1_", "kb_ws_2_"}, dropper.listCalls)
}

func TestDropTenantCollections_workspaceDropFailureIsCollected(t *testing.T) {
	mock := newRepoMock(t)
	dropper := &fakeDropper{err: errors.New("drop boom")}
	cleaner := newVectorCleaner(mock, dropper)
	tenantID := "550e8400-e29b-41d4-a716-446655440000"

	repoBeginTenant(mock)
	mock.ExpectQuery("SELECT id FROM rag_workspaces").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("ws-1"))
	mock.ExpectCommit()

	// Drop failures are joined and surfaced, not silently ignored.
	err := cleaner.DropTenantCollections(context.Background(), tenantID)
	require.ErrorContains(t, err, "drop tenant collections")
	require.ErrorContains(t, err, "drop boom")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDropTenantCollections_listFailureIsSurfaced(t *testing.T) {
	mock := newRepoMock(t)
	dropper := &fakeDropper{listErr: errors.New("list boom")}
	cleaner := newVectorCleaner(mock, dropper)
	tenantID := "550e8400-e29b-41d4-a716-446655440000"

	repoBeginTenant(mock)
	mock.ExpectQuery("SELECT id FROM rag_workspaces").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("ws-1"))
	mock.ExpectCommit()

	// 列不出来不能静默漏删：固定名与 legacy 名仍先删，list 失败必须暴露。
	err := cleaner.DropTenantCollections(context.Background(), tenantID)
	require.ErrorContains(t, err, "list collections")
	require.ErrorContains(t, err, "list boom")
	require.NoError(t, mock.ExpectationsWereMet())
	require.Equal(t, []string{
		"memory_550e8400_e29b_41d4_a716_446655440000",
		"memory_facts_550e8400_e29b_41d4_a716_446655440000",
		"tenant_550e8400_e29b_41d4_a716_446655440000_kb",
		constants.CollectionLegacyName(tenantID, "ws-1"),
	}, dropper.deleted)
}

func TestDropTenantCollections_notFoundIsTolerated(t *testing.T) {
	mock := newRepoMock(t)
	dropper := &fakeDropper{
		notFoundPrefix: "kb_",
		lists: map[string][]string{
			"kb_ws_1_": {"kb_ws_1_text_embedding_v3"},
		},
	}
	cleaner := newVectorCleaner(mock, dropper)
	tenantID := "550e8400-e29b-41d4-a716-446655440000"

	repoBeginTenant(mock)
	mock.ExpectQuery("SELECT id FROM rag_workspaces").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("ws-1"))
	mock.ExpectCommit()

	// collection 不存在是清理的正常情况：全部容忍，不报错。
	err := cleaner.DropTenantCollections(context.Background(), tenantID)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Equal(t, []string{
		"memory_550e8400_e29b_41d4_a716_446655440000",
		"memory_facts_550e8400_e29b_41d4_a716_446655440000",
		"tenant_550e8400_e29b_41d4_a716_446655440000_kb",
		constants.CollectionLegacyName(tenantID, "ws-1"),
		"kb_ws_1_text_embedding_v3",
	}, dropper.deleted)
}

func TestDropTenantCollections_queryFailureIsWarnedNotFatal(t *testing.T) {
	mock := newRepoMock(t)
	dropper := &fakeDropper{}
	cleaner := newVectorCleaner(mock, dropper)

	// Best-effort cleanup: a failed workspace lookup is logged and the
	// fixed-name collections are still dropped; the error must not be
	// silently returned as success.
	repoBeginTenant(mock)
	mock.ExpectQuery("SELECT id FROM rag_workspaces").WillReturnError(context.DeadlineExceeded)
	mock.ExpectRollback()

	err := cleaner.DropTenantCollections(context.Background(), "550e8400-e29b-41d4-a716-446655440000")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Len(t, dropper.deleted, 3)
}
