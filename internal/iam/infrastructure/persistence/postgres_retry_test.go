package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsRetryablePostgresError(t *testing.T) {
	tests := []struct {
		name string
		code string
		want bool
	}{
		{name: "serialization failure", code: "40001", want: true},
		{name: "deadlock detected", code: "40P01", want: true},
		{name: "connection exception", code: "08006", want: true},
		{name: "connection does not exist", code: "08003", want: true},
		{name: "unique violation", code: "23505", want: false},
		{name: "invalid text representation", code: "22P02", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := errors.New("wrapped: " + tt.code)
			if tt.want {
				err = &pgconn.PgError{Code: tt.code}
			}
			if got := isRetryablePostgresError(err); got != tt.want {
				t.Fatalf("isRetryablePostgresError(%q) = %v, want %v", tt.code, got, tt.want)
			}
		})
	}
}

func TestRetryPostgresStopsOnCancellationDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls int
	err := retryPostgres(ctx, 3, time.Hour, func() error {
		calls++
		cancel()
		return &pgconn.PgError{Code: "40001"}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("retryPostgres error = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("retryPostgres calls = %d, want 1", calls)
	}
}
