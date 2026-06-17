package graph

import (
	"context"
	"errors"
	"time"
)

// RetryConfig controls retry behaviour.
type RetryConfig struct {
	Attempts int
	Base     time.Duration
	Max      time.Duration
}

// DefaultRetry is 3 attempts, 100ms base, 10s max.
var DefaultRetry = RetryConfig{Attempts: 3, Base: 100 * time.Millisecond, Max: 10 * time.Second}

// RetryFn calls fn up to cfg.Attempts times with exponential backoff.
// Returns the first successful result or the last error.
func RetryFn[T any](ctx context.Context, cfg RetryConfig, fn func() (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	delay := cfg.Base
	var lastErr error
	for i := 0; i < cfg.Attempts; i++ {
		result, err := fn()
		if err == nil {
			return result, nil
		}
		lastErr = err
		if i < cfg.Attempts-1 {
			select {
			case <-ctx.Done():
				return zero, errors.Join(lastErr, ctx.Err())
			case <-time.After(delay):
			}
			delay *= 2
			if delay > cfg.Max {
				delay = cfg.Max
			}
		}
	}
	return zero, lastErr
}
