package workers_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
)

func TestGCWorker_PurgesExpiredFacts(t *testing.T) {
	var purgedCount int

	repo := &stubFactRepo{
		deleteOldSoftDeleted: func(ctx context.Context, tenantID string, retentionDays int) (int, error) {
			purgedCount = 2
			return purgedCount, nil
		},
	}

	worker := workers.NewGCWorker("", repo, zap.NewNop())
	worker.RunOnce(context.Background())

	require.Equal(t, 2, purgedCount, "should report 2 purged facts")
}

func TestGCWorker_RecoversPanic(t *testing.T) {
	repo := &stubFactRepo{
		deleteOldSoftDeleted: func(ctx context.Context, tenantID string, retentionDays int) (int, error) {
			panic("database crashed")
		},
	}

	worker := workers.NewGCWorker("", repo, zap.NewNop())
	// Should not panic
	worker.RunOnce(context.Background())
}

func TestGCWorker_GracefulShutdown(t *testing.T) {
	repo := &stubFactRepo{
		deleteOldSoftDeleted: func(ctx context.Context, tenantID string, retentionDays int) (int, error) {
			return 0, nil
		},
	}

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
		// OK
	case <-time.After(1 * time.Second):
		t.Fatal("worker did not stop within 1s")
	}
}

func TestGCWorker_SkipsIfNoExpiredFacts(t *testing.T) {
	var callCount int

	repo := &stubFactRepo{
		deleteOldSoftDeleted: func(ctx context.Context, tenantID string, retentionDays int) (int, error) {
			callCount++
			return 0, nil
		},
	}

	worker := workers.NewGCWorker("", repo, zap.NewNop())
	worker.RunOnce(context.Background())

	require.Equal(t, 1, callCount, "should call DeleteOldSoftDeleted once")
}
