package workers

import (
	"context"
	"time"

	"go.uber.org/zap"
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

// runWithRestart executes fn in a supervisor loop: recovers from panics and restarts
// with exponential backoff, exits cleanly on ctx cancel or stopCh close.
func runWithRestart(ctx context.Context, stopCh chan struct{}, logger *zap.Logger, name string, fn func(context.Context)) {
	const (
		baseBackoff       = 100 * time.Millisecond
		maxBackoff        = 30 * time.Second
		fastExitThreshold = 5
		fastExitWindow    = 5 * time.Second
	)
	backoff := baseBackoff
	fastExits := 0
	for {
		start := time.Now()
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Error(name+".panic", zap.Any("panic", r), zap.Stack("stack"))
				}
			}()
			fn(ctx)
		}()
		select {
		case <-ctx.Done():
			return
		case <-stopCh:
			return
		default:
		}
		runtime := time.Since(start)
		switch {
		case runtime > time.Minute:
			backoff = baseBackoff
			fastExits = 0
		case runtime < fastExitWindow:
			fastExits++
			if fastExits >= fastExitThreshold {
				backoff = maxBackoff
				fastExits = 0
			}
		default:
			fastExits = 0
		}
		logger.Warn(name+".restarting", zap.Duration("backoff", backoff))
		select {
		case <-ctx.Done():
			return
		case <-stopCh:
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}
