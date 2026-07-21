package postgres

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/pashagolub/pgxmock/v2"
)

func TestRunWithSchemaProvisionLockCommitsTransactionScopedLock(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec(regexp.QuoteMeta(schemaProvisionLockSQL)).
		WithArgs(schemaProvisionLockKey).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	pool.ExpectCommit()

	callbackCalled := false
	err = runWithSchemaProvisionLock(context.Background(), context.Background(), pool, func(context.Context) error {
		callbackCalled = true
		return nil
	})
	if err != nil {
		t.Fatalf("runWithSchemaProvisionLock: %v", err)
	}
	if !callbackCalled {
		t.Fatal("schema callback was not called")
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRunWithSchemaProvisionLockRollsBackCallbackFailure(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	callbackErr := errors.New("provision failed")
	pool.ExpectBegin()
	pool.ExpectExec(regexp.QuoteMeta(schemaProvisionLockSQL)).
		WithArgs(schemaProvisionLockKey).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	pool.ExpectRollback()

	err = runWithSchemaProvisionLock(context.Background(), context.Background(), pool, func(context.Context) error {
		return callbackErr
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("error %v does not preserve callback error", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRunWithSchemaProvisionLockRollsBackCallbackPanic(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec(regexp.QuoteMeta(schemaProvisionLockSQL)).
		WithArgs(schemaProvisionLockKey).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	pool.ExpectRollback()

	func() {
		defer func() {
			if recovered := recover(); recovered != "callback panic" {
				t.Fatalf("recovered = %v, want callback panic", recovered)
			}
		}()
		_ = runWithSchemaProvisionLock(context.Background(), context.Background(), pool, func(context.Context) error {
			panic("callback panic")
		})
	}()
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRunWithSchemaProvisionLockRollsBackLockFailure(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	lockErr := errors.New("lock failed")
	pool.ExpectBegin()
	pool.ExpectExec(regexp.QuoteMeta(schemaProvisionLockSQL)).
		WithArgs(schemaProvisionLockKey).
		WillReturnError(lockErr)
	pool.ExpectRollback()

	err = runWithSchemaProvisionLock(context.Background(), context.Background(), pool, func(context.Context) error {
		t.Fatal("callback must not run when lock acquisition fails")
		return nil
	})
	if !errors.Is(err, lockErr) {
		t.Fatalf("error %v does not preserve lock error", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRunWithSchemaProvisionLockReturnsBeginFailure(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	beginErr := errors.New("begin failed")
	pool.ExpectBegin().WillReturnError(beginErr)

	err = runWithSchemaProvisionLock(context.Background(), context.Background(), pool, func(context.Context) error {
		t.Fatal("callback must not run when transaction begin fails")
		return nil
	})
	if !errors.Is(err, beginErr) {
		t.Fatalf("error %v does not preserve begin error", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRunWithSchemaProvisionLockPreservesCallbackAndRollbackFailures(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	callbackErr := errors.New("provision failed")
	rollbackErr := errors.New("rollback failed")
	pool.ExpectBegin()
	pool.ExpectExec(regexp.QuoteMeta(schemaProvisionLockSQL)).
		WithArgs(schemaProvisionLockKey).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	pool.ExpectRollback().WillReturnError(rollbackErr)

	err = runWithSchemaProvisionLock(context.Background(), context.Background(), pool, func(context.Context) error {
		return callbackErr
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("error %v does not preserve callback error", err)
	}
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("error %v does not preserve rollback error", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRunWithSchemaProvisionLockReturnsCommitFailure(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	commitErr := errors.New("commit failed")
	pool.ExpectBegin()
	pool.ExpectExec(regexp.QuoteMeta(schemaProvisionLockSQL)).
		WithArgs(schemaProvisionLockKey).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	pool.ExpectCommit().WillReturnError(commitErr)
	pool.ExpectRollback()

	err = runWithSchemaProvisionLock(context.Background(), context.Background(), pool, func(context.Context) error {
		return nil
	})
	if !errors.Is(err, commitErr) {
		t.Fatalf("error %v does not preserve commit error", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRunWithSchemaProvisionLockUsesParentContextForCallback(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec(regexp.QuoteMeta(schemaProvisionLockSQL)).
		WithArgs(schemaProvisionLockKey).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	pool.ExpectCommit()
	callbackCtx := context.Background()

	err = runWithSchemaProvisionLock(context.Background(), callbackCtx, pool, func(ctx context.Context) error {
		if ctx != callbackCtx {
			t.Fatal("callback did not receive the parent context")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("runWithSchemaProvisionLock: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
