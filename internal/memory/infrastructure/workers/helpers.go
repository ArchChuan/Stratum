package workers

import (
	"context"
	"time"
)

// SleepCtx sleeps for duration d, returning early if ctx is cancelled or stopCh is closed.
// Returns true if the full duration elapsed, false if cancelled early.
func SleepCtx(ctx context.Context, stopCh <-chan struct{}, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-stopCh:
		return false
	case <-t.C:
		return true
	}
}
