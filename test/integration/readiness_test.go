package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	storagemilvus "github.com/byteBuilderX/stratum/pkg/storage/milvus"
)

func TestWaitForMilvusReadyRetriesAvailabilityThenSucceeds(t *testing.T) {
	connectAttempts, probeAttempts := 0, 0
	err := waitForMilvusReady(context.Background(), true, time.Millisecond, func(context.Context) error {
		connectAttempts++
		return nil
	}, func(context.Context) error {
		probeAttempts++
		if probeAttempts < 3 {
			return &storagemilvus.UnavailableError{Op: "connect", Err: errors.New("proxy not ready")}
		}
		return nil
	})
	if err != nil || connectAttempts != 3 || probeAttempts != 3 {
		t.Fatalf("error = %v, connect attempts = %d, probe attempts = %d, want success after 3", err, connectAttempts, probeAttempts)
	}
}

func TestWaitForMilvusReadyTimesOutOnPersistentAvailability(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	attempts := 0
	err := waitForMilvusReady(ctx, true, time.Millisecond, func(context.Context) error { return nil }, func(context.Context) error {
		attempts++
		return &storagemilvus.UnavailableError{Op: "connect", Err: errors.New("still starting")}
	})
	if !errors.Is(err, ErrMilvusReadinessTimeout) || attempts < 2 {
		t.Fatalf("error = %v, attempts = %d, want bounded readiness timeout", err, attempts)
	}
}

func TestWaitForMilvusReadyFailsImmediatelyOnNonAvailability(t *testing.T) {
	want := errors.New("protocol mismatch")
	attempts := 0
	err := waitForMilvusReady(context.Background(), true, time.Millisecond, func(context.Context) error { return nil }, func(context.Context) error {
		attempts++
		return want
	})
	if !errors.Is(err, want) || attempts != 1 {
		t.Fatalf("error = %v, attempts = %d, want immediate source error", err, attempts)
	}
}

func TestWaitForMilvusReadyDoesNotRetryCallerCancellation(t *testing.T) {
	attempts := 0
	err := waitForMilvusReady(context.Background(), true, time.Millisecond, func(context.Context) error { return nil }, func(context.Context) error {
		attempts++
		return context.Canceled
	})
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("error = %v, attempts = %d, want immediate cancellation", err, attempts)
	}
}
