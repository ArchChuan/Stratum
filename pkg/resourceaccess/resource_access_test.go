package resourceaccess

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

// newMockTx returns a pgxmock pool with an open transaction, the shape used by
// every call site (transaction-scoped, search_path already set by the caller).
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

// TestAllowedEditorRolesLocked pins the editor-eligibility role whitelist to the
// agent semantics established in #475. A future role addition must be reviewed
// across agent/knowledge/skill/mcp — it must never diverge per context.
func TestAllowedEditorRolesLocked(t *testing.T) {
	require.ElementsMatch(t, []string{"admin", "owner", "member"}, allowedEditorRoles)
}

func TestEditorEligible(t *testing.T) {
	roles := []struct {
		name string
		role string
		want bool
	}{
		{"admin", "admin", true},
		{"owner", "owner", true},
		{"member", "member", true},
		{"unknown_role_fails_closed", "viewer", false},
	}
	for _, tc := range roles {
		t.Run(tc.name, func(t *testing.T) {
			pool, tx := newMockTx(t)
			pool.ExpectQuery("public.tenant_members").
				WithArgs("t1", "u1", allowedEditorRoles).
				WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(tc.want))

			ok, err := EditorEligible(context.Background(), tx, "t1", "u1")
			require.NoError(t, err)
			require.Equal(t, tc.want, ok)
			require.NoError(t, pool.ExpectationsWereMet())
		})
	}
}

func TestEditorEligible_lookupErrorFailsClosed(t *testing.T) {
	pool, tx := newMockTx(t)
	pool.ExpectQuery("public.tenant_members").
		WithArgs("t1", "u1", allowedEditorRoles).
		WillReturnError(errors.New("boom"))

	_, err := EditorEligible(context.Background(), tx, "t1", "u1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "editor role check")
	require.NoError(t, pool.ExpectationsWereMet())
}

func TestInsertEditors(t *testing.T) {
	t.Run("all_eligible_inserted", func(t *testing.T) {
		pool, tx := newMockTx(t)
		pool.ExpectQuery("public.tenant_members").WithArgs("t1", "u1", allowedEditorRoles).
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
		pool.ExpectExec("resource_editors").WithArgs("agent", "r1", "u1", "creator").
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
		pool.ExpectQuery("public.tenant_members").WithArgs("t1", "u2", allowedEditorRoles).
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
		pool.ExpectExec("resource_editors").WithArgs("agent", "r1", "u2", "creator").
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		err := InsertEditors(context.Background(), tx, "t1", "agent", "r1", []string{"u1", "u2"}, "creator", errors.New("sentinel"))
		require.NoError(t, err)
		require.NoError(t, pool.ExpectationsWereMet())
	})

	t.Run("not_eligible_fails_closed", func(t *testing.T) {
		pool, tx := newMockTx(t)
		pool.ExpectQuery("public.tenant_members").WithArgs("t1", "u1", allowedEditorRoles).
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))

		notEligible := errors.New("domain.ErrEditorNotEligible")
		err := InsertEditors(context.Background(), tx, "t1", "agent", "r1", []string{"u1"}, "creator", notEligible)
		require.Error(t, err)
		require.ErrorIs(t, err, notEligible)
		require.NoError(t, pool.ExpectationsWereMet())
	})
}

func TestRevalidateEditorAccess(t *testing.T) {
	forbidden := errors.New("domain.ErrForbidden")

	t.Run("eligible_and_present", func(t *testing.T) {
		pool, tx := newMockTx(t)
		pool.ExpectQuery("public.tenant_members").WithArgs("t1", "u1", allowedEditorRoles).
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
		pool.ExpectQuery("resource_editors").WithArgs("agent", "r1", "u1").
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))

		err := RevalidateEditorAccess(context.Background(), tx, "t1", "agent", "r1", "u1", forbidden)
		require.NoError(t, err)
		require.NoError(t, pool.ExpectationsWereMet())
	})

	t.Run("not_eligible_forbidden", func(t *testing.T) {
		pool, tx := newMockTx(t)
		pool.ExpectQuery("public.tenant_members").WithArgs("t1", "u1", allowedEditorRoles).
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))

		err := RevalidateEditorAccess(context.Background(), tx, "t1", "agent", "r1", "u1", forbidden)
		require.ErrorIs(t, err, forbidden)
		require.NoError(t, pool.ExpectationsWereMet())
	})

	t.Run("not_in_editor_list_forbidden", func(t *testing.T) {
		pool, tx := newMockTx(t)
		pool.ExpectQuery("public.tenant_members").WithArgs("t1", "u1", allowedEditorRoles).
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
		pool.ExpectQuery("resource_editors").WithArgs("agent", "r1", "u1").
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))

		err := RevalidateEditorAccess(context.Background(), tx, "t1", "agent", "r1", "u1", forbidden)
		require.ErrorIs(t, err, forbidden)
		require.NoError(t, pool.ExpectationsWereMet())
	})
}

func TestInsertChangeAudit(t *testing.T) {
	valid := ChangeAudit{
		ResourceKind: "agent",
		ResourceID:   "a1",
		Operation:    "update",
		ActorID:      "u1",
		ActorType:    "user",
		Source:       "api",
		Before:       json.RawMessage(`{"v":1}`),
		After:        json.RawMessage(`{"v":2}`),
	}

	t.Run("complete_event_executes", func(t *testing.T) {
		pool, tx := newMockTx(t)
		pool.ExpectExec("resource_change_audits").
			WithArgs(pgxmock.AnyArg(), "t1", "agent", "a1", "update", "u1", "user", "api", "", json.RawMessage(`{"v":1}`), json.RawMessage(`{"v":2}`)).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		err := InsertChangeAudit(context.Background(), tx, "t1", "INSERT INTO resource_change_audits (id) VALUES ($1)", valid)
		require.NoError(t, err)
		require.NoError(t, pool.ExpectationsWereMet())
	})

	t.Run("incomplete_event_fails_closed", func(t *testing.T) {
		pool, tx := newMockTx(t)

		ev := valid
		ev.ResourceID = ""
		err := InsertChangeAudit(context.Background(), tx, "t1", "INSERT INTO resource_change_audits (id) VALUES ($1)", ev)
		require.Error(t, err)
		require.Contains(t, err.Error(), "incomplete event")
		require.NoError(t, pool.ExpectationsWereMet())
	})
}
