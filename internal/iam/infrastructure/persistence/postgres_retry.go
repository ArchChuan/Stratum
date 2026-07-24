package persistence

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// isRetryablePostgresError reports whether PostgreSQL classified an error as a
// transient serialization/deadlock or connection failure. Wrapped pgx errors
// are intentionally supported because repository methods add operation context.
func isRetryablePostgresError(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	state := pgErr.SQLState()
	return state == "40001" || state == "40P01" || strings.HasPrefix(state, "08")
}

func retryPostgres(ctx context.Context, attempts int, backoff time.Duration, operation func() error) error {
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err = operation()
		if err == nil || !isRetryablePostgresError(err) || attempt == attempts {
			return err
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}
