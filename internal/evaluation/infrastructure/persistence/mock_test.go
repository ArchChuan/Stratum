package persistence

import (
	"testing"

	"github.com/pashagolub/pgxmock/v2"
)

// newMockRepo returns a pgxmock pool used as poolIface by the persistence repos.
func newMockRepo(t *testing.T) pgxmock.PgxPoolIface {
	t.Helper()
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// expectTenantTx expects BEGIN + SET LOCAL search_path inside a tenant transaction.
func expectTenantTx(mock pgxmock.PgxPoolIface) {
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
}
