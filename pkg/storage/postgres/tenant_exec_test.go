package postgres

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
)

func TestExecTenantOnPoolSetsSearchPathAndCommits(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec(regexp.QuoteMeta(`SET LOCAL search_path = "tenant_abc-1", public`)).
		WillReturnResult(pgxmock.NewResult("SET", 1))
	pool.ExpectCommit()

	var seenTx pgx.Tx
	callbackCtx := context.Background()
	err = execTenantOnPool(callbackCtx, pool, "abc-1", func(ctx context.Context, tx pgx.Tx) error {
		seenTx = tx
		current, ok := ctx.Value(tenantTxKey{}).(tenantTxContext)
		if !ok {
			t.Fatal("callback context missing nested tenant transaction key")
		}
		if current.tenantID != "abc-1" {
			t.Fatalf("nested tenantID = %q, want abc-1", current.tenantID)
		}
		if current.tx != tx {
			t.Fatal("nested tx does not match the tx handed to the callback")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("execTenantOnPool: %v", err)
	}
	if seenTx == nil {
		t.Fatal("callback did not receive a transaction")
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecTenantOnPoolRejectsUnsafeTenantIDsBeforeBegin(t *testing.T) {
	unsafe := []string{
		"Tenant-1",               // uppercase
		"tenant_租户",              // non-ASCII
		"abc;DROP SCHEMA public", // SQL injection attempt
		`abc" OR "1"="1`,         // quoted injection attempt
		"abc' OR '1'='1",         // quoted injection attempt
	}
	for _, id := range unsafe {
		t.Run(id, func(t *testing.T) {
			pool, err := pgxmock.NewPool()
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()

			called := false
			err = execTenantOnPool(context.Background(), pool, id, func(context.Context, pgx.Tx) error {
				called = true
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), "invalid tenant_id") {
				t.Fatalf("execTenantOnPool(%q) error = %v, want invalid tenant_id", id, err)
			}
			if called {
				t.Fatalf("callback ran for unsafe tenant_id %q; must fail closed before Begin", id)
			}
			if err := pool.ExpectationsWereMet(); err != nil {
				t.Fatalf("pool was touched for unsafe tenant_id %q: %v", id, err)
			}
		})
	}
}

func TestExecTenantWithRejectsEmptyTenantID(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	called := false
	err = ExecTenantWith(context.Background(), pool, "", func(context.Context, pgx.Tx) error {
		called = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "tenant_id is empty") {
		t.Fatalf("ExecTenantWith error = %v, want tenant_id is empty", err)
	}
	if called {
		t.Fatal("callback ran for empty tenant_id")
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("pool was touched for empty tenant_id: %v", err)
	}
}

func TestExecTenantOnPoolRollsBackWhenCallbackFails(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	callbackErr := errors.New("callback failed")
	pool.ExpectBegin()
	pool.ExpectExec(regexp.QuoteMeta(`SET LOCAL search_path = "tenant_abc-1", public`)).
		WillReturnResult(pgxmock.NewResult("SET", 1))
	pool.ExpectRollback()

	err = execTenantOnPool(context.Background(), pool, "abc-1", func(context.Context, pgx.Tx) error {
		return callbackErr
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("error %v does not preserve callback error", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecTenantOnPoolRollsBackWhenSetSearchPathFails(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	setErr := errors.New("set search_path failed")
	pool.ExpectBegin()
	pool.ExpectExec(regexp.QuoteMeta(`SET LOCAL search_path = "tenant_abc-1", public`)).
		WillReturnError(setErr)
	pool.ExpectRollback()

	called := false
	err = execTenantOnPool(context.Background(), pool, "abc-1", func(context.Context, pgx.Tx) error {
		called = true
		return nil
	})
	if !errors.Is(err, setErr) {
		t.Fatalf("error %v does not preserve set search_path error", err)
	}
	if called {
		t.Fatal("callback ran despite search_path failure")
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecTenantReusesNestedTransactionWithoutNewBegin(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// The mock pool itself satisfies pgx.Tx, so it stands in for the outer
	// transaction that is expected to already exist on the context.
	nested := context.WithValue(context.Background(), tenantTxKey{}, tenantTxContext{tenantID: "abc-1", tx: pool})
	tenantCtx := WithTenant(nested, &TenantContext{TenantID: "abc-1"})

	var gotTx pgx.Tx
	err = ExecTenant(tenantCtx, nil, func(ctx context.Context, tx pgx.Tx) error {
		gotTx = tx
		current, ok := ctx.Value(tenantTxKey{}).(tenantTxContext)
		if !ok || current.tx != tx {
			t.Fatal("nested transaction key not preserved in callback context")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ExecTenant nested: %v", err)
	}
	if gotTx != pool {
		t.Fatal("nested ExecTenant did not reuse the existing transaction")
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("nested ExecTenant touched the pool: %v", err)
	}
}

func TestExecTenantRejectsNestedTransactionForDifferentTenant(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	nested := context.WithValue(context.Background(), tenantTxKey{}, tenantTxContext{tenantID: "other-tenant", tx: pool})
	tenantCtx := WithTenant(nested, &TenantContext{TenantID: "abc-1"})

	called := false
	err = ExecTenant(tenantCtx, nil, func(context.Context, pgx.Tx) error {
		called = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "tenant transaction context mismatch") {
		t.Fatalf("ExecTenant error = %v, want tenant transaction context mismatch", err)
	}
	if called {
		t.Fatal("callback ran despite tenant transaction context mismatch")
	}
}

func TestExecTenantFailsClosedWithoutTenantContext(t *testing.T) {
	err := ExecTenant(context.Background(), nil, func(context.Context, pgx.Tx) error {
		t.Fatal("callback must not run without a tenant context")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "missing tenant context") {
		t.Fatalf("ExecTenant error = %v, want missing tenant context", err)
	}

	globalAdmin := WithTenant(context.Background(), &TenantContext{TenantID: ""})
	err = ExecTenant(globalAdmin, nil, func(context.Context, pgx.Tx) error {
		t.Fatal("callback must not run for global_admin")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "tenant_id is empty") {
		t.Fatalf("ExecTenant error = %v, want tenant_id is empty", err)
	}
}

func TestExecTenantWithReusesNestedTransactionWithoutNewBegin(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// Same rule as ExecTenant: a nested ExecTenantWith call must reuse the
	// enclosing tenant transaction instead of beginning a new one. The mock
	// pool stands in for the outer transaction on the context.
	nested := context.WithValue(context.Background(), tenantTxKey{}, tenantTxContext{tenantID: "abc-1", tx: pool})

	var gotTx pgx.Tx
	err = ExecTenantWith(nested, pool, "abc-1", func(ctx context.Context, tx pgx.Tx) error {
		gotTx = tx
		current, ok := ctx.Value(tenantTxKey{}).(tenantTxContext)
		if !ok || current.tx != tx {
			t.Fatal("nested transaction key not preserved in callback context")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ExecTenantWith nested: %v", err)
	}
	if gotTx != pool {
		t.Fatal("nested ExecTenantWith did not reuse the existing transaction")
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("nested ExecTenantWith touched the pool: %v", err)
	}
}

func TestExecTenantWithRejectsNestedTransactionForDifferentTenant(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	nested := context.WithValue(context.Background(), tenantTxKey{}, tenantTxContext{tenantID: "other-tenant", tx: pool})

	called := false
	err = ExecTenantWith(nested, pool, "abc-1", func(context.Context, pgx.Tx) error {
		called = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "tenant transaction context mismatch") {
		t.Fatalf("ExecTenantWith error = %v, want tenant transaction context mismatch", err)
	}
	if called {
		t.Fatal("callback ran despite tenant transaction context mismatch")
	}
}
