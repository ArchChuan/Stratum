package postgres

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
)

func TestExecTenantWithClassifiesCommitOutcome(t *testing.T) {
	tests := []struct {
		name        string
		commitErr   error
		wantUnknown bool
	}{
		{name: "success"},
		{name: "definite rollback", commitErr: pgx.ErrTxCommitRollback},
		{name: "unknown outcome", commitErr: errors.New("connection lost"), wantUnknown: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := pgxmock.NewPool()
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			pool.ExpectBegin()
			pool.ExpectExec(regexp.QuoteMeta(`SET LOCAL search_path = "tenant_tenant_1", public`)).
				WillReturnResult(pgxmock.NewResult("SET", 0))
			if tt.commitErr == nil {
				pool.ExpectCommit()
			} else {
				pool.ExpectCommit().WillReturnError(tt.commitErr)
			}
			err = ExecTenantWith(context.Background(), pool, "tenant_1", func(context.Context, pgx.Tx) error { return nil })
			if tt.commitErr == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.commitErr != nil && !errors.Is(err, tt.commitErr) {
				t.Fatalf("commit error not preserved: %v", err)
			}
			if errors.Is(err, ErrCommitOutcomeUnknown) != tt.wantUnknown {
				t.Fatalf("unknown classification=%v err=%v", errors.Is(err, ErrCommitOutcomeUnknown), err)
			}
			if err := pool.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
