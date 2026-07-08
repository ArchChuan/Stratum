package middleware

import (
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestRateLimiterStoreStopTerminatesCleanup(t *testing.T) {
	store := NewRateLimiterStore(rate.Limit(1), 1)
	done := store.Stop()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("rate limiter cleanup goroutine did not stop")
	}

	select {
	case <-store.Stop():
	case <-time.After(time.Second):
		t.Fatal("second Stop call should return an already-closed channel")
	}
}
