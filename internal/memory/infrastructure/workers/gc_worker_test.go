package workers_test

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
)

func TestGCWorker_RunOnce_NoPanic(t *testing.T) {
	repo := &stubFactRepo{}
	worker := workers.NewGCWorker("", repo, zap.NewNop())
	worker.RunOnce(context.Background())
}

func TestGCWorker_PurgeSuperseded_DrainsInBatches(t *testing.T) {
	// Simulate a backlog of 250 superseded facts; batch size is 100, so the
	// worker must call PurgeSuperseded 3 times (100, 100, 50) and stop once a
	// short batch signals the backlog is drained.
	remaining := 250
	calls := 0
	var lastCutoff time.Time
	repo := &stubFactRepo{
		purgeFunc: func(_ context.Context, _ string, cutoff time.Time, limit int) (int, error) {
			calls++
			lastCutoff = cutoff
			n := remaining
			if n > limit {
				n = limit
			}
			remaining -= n
			return n, nil
		},
	}
	worker := workers.NewGCWorker("tenant-a", repo, zap.NewNop())
	worker.RunOnce(context.Background())

	if calls != 3 {
		t.Fatalf("expected 3 purge batches, got %d", calls)
	}
	if remaining != 0 {
		t.Fatalf("expected backlog fully drained, %d left", remaining)
	}
	// Cutoff must be in the past (retention window applied), never in the future.
	if !lastCutoff.Before(time.Now()) {
		t.Fatalf("cutoff %v should predate now", lastCutoff)
	}
}

func TestGCWorker_PurgeSuperseded_StopsOnShortBatch(t *testing.T) {
	// A single short batch (fewer rows than the limit) means no more work.
	calls := 0
	repo := &stubFactRepo{
		purgeFunc: func(_ context.Context, _ string, _ time.Time, _ int) (int, error) {
			calls++
			return 5, nil
		},
	}
	worker := workers.NewGCWorker("tenant-a", repo, zap.NewNop())
	worker.RunOnce(context.Background())

	if calls != 1 {
		t.Fatalf("expected exactly 1 purge call on short batch, got %d", calls)
	}
}

func TestGCWorker_GracefulShutdown(t *testing.T) {
	repo := &stubFactRepo{}
	worker := workers.NewGCWorker("", repo, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Start(ctx)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("worker did not stop within 1s")
	}
}
