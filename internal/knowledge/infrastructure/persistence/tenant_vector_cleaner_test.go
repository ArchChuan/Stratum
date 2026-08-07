package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fakeDropper records the collections it was asked to delete, so tests can
// assert which collections cleanup targets without a live Milvus.
type fakeDropper struct {
	deleted []string
	err     error
}

func (f *fakeDropper) DeleteCollection(_ context.Context, name string) error {
	f.deleted = append(f.deleted, name)
	return f.err
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
	dropper := &fakeDropper{}
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
	require.Equal(t, []string{
		"memory_550e8400_e29b_41d4_a716_446655440000",
		"memory_facts_550e8400_e29b_41d4_a716_446655440000",
		"tenant_550e8400_e29b_41d4_a716_446655440000_kb",
		constants.CollectionName(tenantID, "ws-1"),
		constants.CollectionName(tenantID, "ws-2"),
	}, dropper.deleted)
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
