package workers_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
)

func TestSleepCtx_CancelledByContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stopCh := make(chan struct{})

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	ok := workers.SleepCtx(ctx, stopCh, 1*time.Second)
	require.False(t, ok, "should return false when context cancelled")
}

func TestSleepCtx_CancelledByStop(t *testing.T) {
	ctx := context.Background()
	stopCh := make(chan struct{})

	go func() {
		time.Sleep(10 * time.Millisecond)
		close(stopCh)
	}()

	ok := workers.SleepCtx(ctx, stopCh, 1*time.Second)
	require.False(t, ok, "should return false when stop signalled")
}

func TestSleepCtx_FullDuration(t *testing.T) {
	ctx := context.Background()
	stopCh := make(chan struct{})

	start := time.Now()
	ok := workers.SleepCtx(ctx, stopCh, 50*time.Millisecond)
	require.True(t, ok)
	require.GreaterOrEqual(t, time.Since(start).Milliseconds(), int64(50))
}
