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
