package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

type recordingSchemaLockConn struct {
	calls     []string
	unlockErr error
}

func (c *recordingSchemaLockConn) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	switch sql {
	case schemaProvisionLockSQL:
		c.calls = append(c.calls, "lock")
		return pgconn.CommandTag{}, nil
	case schemaProvisionUnlockSQL:
		c.calls = append(c.calls, "unlock")
		return pgconn.CommandTag{}, c.unlockErr
	default:
		return pgconn.CommandTag{}, errors.New("unexpected SQL")
	}
}

func TestRunWithSchemaProvisionLockOrdersLockCallbackAndUnlock(t *testing.T) {
	conn := &recordingSchemaLockConn{}
	err := runWithSchemaProvisionLock(context.Background(), context.Background(), conn, func(context.Context) error {
		conn.calls = append(conn.calls, "callback")
		return nil
	})
	if err != nil {
		t.Fatalf("runWithSchemaProvisionLock: %v", err)
	}
	want := []string{"lock", "callback", "unlock"}
	if !reflect.DeepEqual(conn.calls, want) {
		t.Fatalf("calls = %v, want %v", conn.calls, want)
	}
}

func TestRunWithSchemaProvisionLockPreservesCallbackAndUnlockErrors(t *testing.T) {
	callbackErr := errors.New("provision failed")
	unlockErr := errors.New("unlock failed")
	conn := &recordingSchemaLockConn{unlockErr: unlockErr}

	err := runWithSchemaProvisionLock(context.Background(), context.Background(), conn, func(context.Context) error {
		return callbackErr
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("error %v does not preserve callback error", err)
	}
	if !errors.Is(err, unlockErr) {
		t.Fatalf("error %v does not include unlock error", err)
	}
}

func TestRunWithSchemaProvisionLockUsesParentContextForCallback(t *testing.T) {
	lockCtx, cancel := context.WithCancel(context.Background())
	cancel()
	conn := &recordingSchemaLockConn{}
	callbackCtx := context.Background()

	err := runWithSchemaProvisionLock(lockCtx, callbackCtx, conn, func(ctx context.Context) error {
		if ctx.Err() != nil {
			t.Fatalf("callback context is canceled: %v", ctx.Err())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("runWithSchemaProvisionLock: %v", err)
	}
}
