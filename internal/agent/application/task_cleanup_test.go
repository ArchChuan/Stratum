package application

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/pkg/observability"
	"go.uber.org/zap"
)

func TestTaskCleanupWorkerDeletesExpired(t *testing.T) {
	repo := &mockTaskRepo{deleteExpired: 3}
	worker := NewTaskCleanupWorker(
		func(context.Context) ([]string, error) { return []string{"tenant-1"}, nil },
		repo, 10*time.Millisecond, zap.NewNop(), observability.NoopMetrics{},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	worker.Start(ctx)
	select {
	case <-ctx.Done():
	case <-time.After(150 * time.Millisecond):
	}
	worker.Stop()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.deleteExpiredCalls == 0 {
		t.Fatal("DeleteExpired should have been called")
	}
}
