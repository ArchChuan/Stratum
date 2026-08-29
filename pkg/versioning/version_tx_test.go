package versioning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

// newMockTx returns a pgxmock pool with an open transaction（同 resourceaccess 测试）。
func newMockTx(t *testing.T) (pgxmock.PgxPoolIface, pgx.Tx) {
	t.Helper()
	pool, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })
	pool.ExpectBegin()
	tx, err := pool.Begin(context.Background())
	require.NoError(t, err)
	return pool, tx
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func mustHash(t *testing.T, payload map[string]any) string {
	t.Helper()
	b, err := json.Marshal(payload)
	require.NoError(t, err)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestProductTableRef(t *testing.T) {
	ref, ok := ProductTableRef("agent")
	require.True(t, ok)
	require.Equal(t, "agents", ref.Table)
	require.Equal(t, "active_version_id", ref.ActiveColumn)

	// knowledge 已接入：productTables 注册 rag_workspaces.active_version_id。
	ref, ok = ProductTableRef("knowledge")
	require.True(t, ok)
	require.Equal(t, "rag_workspaces", ref.Table)
	require.Equal(t, "active_version_id", ref.ActiveColumn)

	// 未接入的 kind 必须 fail-closed（读侧 is_current / 写侧 SetActiveTx 报错）。
	for _, kind := range []string{"skill", "mcp"} {
		_, ok := ProductTableRef(kind)
		require.False(t, ok)
	}
}

func TestInsertVersionTx(t *testing.T) {
	t.Run("derives_revision_parent_hash", func(t *testing.T) {
		pool, tx := newMockTx(t)
		payload := map[string]any{"name": "assistant", "temperature": 0.7}
		summary := map[string]any{"kind": "agent"}
		// revision_no = MAX+1
		pool.ExpectQuery("SELECT COALESCE\\(MAX\\(revision_no\\), 0\\) \\+ 1").
			WithArgs("agent", "a1").WillReturnRows(pgxmock.NewRows([]string{"next"}).AddRow(3))
		// parent = 上一最高版本号行
		pool.ExpectQuery("SELECT COALESCE\\(\\(SELECT id FROM resource_versions").
			WithArgs("agent", "a1", "v3").WillReturnRows(pgxmock.NewRows([]string{"parent"}).AddRow("v2"))
		pool.ExpectExec("INSERT INTO resource_versions").
			WithArgs("v3", "agent", "a1", "v2", 3, "published", "manual", mustHash(t, payload), mustJSON(t, payload), mustJSON(t, summary), "u1").
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		saved, err := InsertVersionTx(context.Background(), tx, VersionRow{
			ID: "v3", ResourceKind: "agent", ResourceID: "a1",
			Status: "published", Source: "manual", Payload: payload, SafeSummary: summary, CreatedBy: "u1",
		})
		require.NoError(t, err)
		require.Equal(t, 3, saved.RevisionNo)
		require.Equal(t, "v2", saved.ParentVersionID)
		require.Equal(t, mustHash(t, payload), saved.ContentHash)
		require.NoError(t, pool.ExpectationsWereMet())
	})

	t.Run("first_version_no_parent", func(t *testing.T) {
		pool, tx := newMockTx(t)
		payload := map[string]any{"name": "first"}
		pool.ExpectQuery("SELECT COALESCE\\(MAX\\(revision_no\\), 0\\) \\+ 1").
			WithArgs("agent", "a1").WillReturnRows(pgxmock.NewRows([]string{"next"}).AddRow(1))
		pool.ExpectQuery("SELECT COALESCE\\(\\(SELECT id FROM resource_versions").
			WithArgs("agent", "a1", "v1").WillReturnRows(pgxmock.NewRows([]string{"parent"}).AddRow(""))
		pool.ExpectExec("INSERT INTO resource_versions").
			WithArgs("v1", "agent", "a1", "", 1, "published", "manual", mustHash(t, payload), mustJSON(t, payload), "{}", "u1").
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		saved, err := InsertVersionTx(context.Background(), tx, VersionRow{
			ID: "v1", ResourceKind: "agent", ResourceID: "a1",
			Status: "published", Source: "manual", Payload: payload, CreatedBy: "u1",
		})
		require.NoError(t, err)
		require.Equal(t, 1, saved.RevisionNo)
		require.Empty(t, saved.ParentVersionID)
		require.NoError(t, pool.ExpectationsWereMet())
	})

	t.Run("unique_violation_propagates", func(t *testing.T) {
		pool, tx := newMockTx(t)
		payload := map[string]any{"name": "n"}
		pool.ExpectQuery("SELECT COALESCE\\(MAX\\(revision_no\\), 0\\) \\+ 1").
			WithArgs("agent", "a1").WillReturnRows(pgxmock.NewRows([]string{"next"}).AddRow(2))
		pool.ExpectQuery("SELECT COALESCE\\(\\(SELECT id FROM resource_versions").
			WithArgs("agent", "a1", "v2").WillReturnRows(pgxmock.NewRows([]string{"parent"}).AddRow(""))
		pool.ExpectExec("INSERT INTO resource_versions").
			WithArgs("v2", "agent", "a1", "", 2, "published", "manual", mustHash(t, payload), mustJSON(t, payload), "{}", "u1").
			WillReturnError(errors.New("unique_violation"))

		_, err := InsertVersionTx(context.Background(), tx, VersionRow{
			ID: "v2", ResourceKind: "agent", ResourceID: "a1",
			Status: "published", Source: "manual", Payload: payload, CreatedBy: "u1",
		})
		require.Error(t, err)
		require.NoError(t, pool.ExpectationsWereMet())
	})
}

func TestDemoteCurrentTx(t *testing.T) {
	t.Run("demotes_published", func(t *testing.T) {
		pool, tx := newMockTx(t)
		pool.ExpectExec("UPDATE resource_versions SET status='deprecated'").
			WithArgs("agent", "a1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))

		require.NoError(t, DemoteCurrentTx(context.Background(), tx, "agent", "a1"))
		require.NoError(t, pool.ExpectationsWereMet())
	})

	t.Run("no_published_is_ok", func(t *testing.T) {
		pool, tx := newMockTx(t)
		pool.ExpectExec("UPDATE resource_versions SET status='deprecated'").
			WithArgs("agent", "a1").WillReturnResult(pgxmock.NewResult("UPDATE", 0))

		require.NoError(t, DemoteCurrentTx(context.Background(), tx, "agent", "a1"))
		require.NoError(t, pool.ExpectationsWereMet())
	})
}

func TestRollbackVersionTx(t *testing.T) {
	t.Run("demotes_and_promotes_target", func(t *testing.T) {
		pool, tx := newMockTx(t)
		pool.ExpectExec("UPDATE resource_versions SET status='deprecated'").
			WithArgs("agent", "a1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		pool.ExpectExec("UPDATE resource_versions SET status='published'").
			WithArgs("agent", "a1", "v1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))

		require.NoError(t, RollbackVersionTx(context.Background(), tx, "agent", "a1", "v1"))
		require.NoError(t, pool.ExpectationsWereMet())
	})

	t.Run("target_not_deprecated_fails", func(t *testing.T) {
		pool, tx := newMockTx(t)
		pool.ExpectExec("UPDATE resource_versions SET status='deprecated'").
			WithArgs("agent", "a1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		pool.ExpectExec("UPDATE resource_versions SET status='published'").
			WithArgs("agent", "a1", "ghost").WillReturnResult(pgxmock.NewResult("UPDATE", 0))

		err := RollbackVersionTx(context.Background(), tx, "agent", "a1", "ghost")
		require.ErrorIs(t, err, ErrVersionNotFound)
		require.NoError(t, pool.ExpectationsWereMet())
	})
}

func TestSetActiveTx(t *testing.T) {
	t.Run("updates_product_table_pointer", func(t *testing.T) {
		pool, tx := newMockTx(t)
		pool.ExpectExec("UPDATE agents SET active_version_id=\\$2, updated_at=NOW\\(\\) WHERE id=\\$1").
			WithArgs("a1", "v3").WillReturnResult(pgxmock.NewResult("UPDATE", 1))

		require.NoError(t, SetActiveTx(context.Background(), tx, "agent", "a1", "v3"))
		require.NoError(t, pool.ExpectationsWereMet())
	})

	t.Run("unregistered_kind_fails_closed", func(t *testing.T) {
		pool, tx := newMockTx(t)
		err := SetActiveTx(context.Background(), tx, "skill", "s1", "v1")
		require.ErrorIs(t, err, ErrVersionKindUnsupported)
		require.NoError(t, pool.ExpectationsWereMet())
	})
}
