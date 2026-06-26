package workers_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
)

type stubExtractionQueue struct {
	dequeueFunc       func(context.Context, string) (*port.ExtractionTask, error)
	markCompletedFunc func(context.Context, string, int64) error
	markFailedFunc    func(context.Context, string, int64, string) error
}

func (q *stubExtractionQueue) Enqueue(ctx context.Context, tenantID string, task *port.ExtractionTask) error {
	return nil
}

func (q *stubExtractionQueue) Dequeue(ctx context.Context, tenantID string) (*port.ExtractionTask, error) {
	return q.dequeueFunc(ctx, tenantID)
}

func (q *stubExtractionQueue) MarkCompleted(ctx context.Context, tenantID string, taskID int64) error {
	return q.markCompletedFunc(ctx, tenantID, taskID)
}

func (q *stubExtractionQueue) MarkFailed(ctx context.Context, tenantID string, taskID int64, errMsg string) error {
	return q.markFailedFunc(ctx, tenantID, taskID, errMsg)
}

func (q *stubExtractionQueue) DeleteOldCompleted(ctx context.Context, tenantID string, retentionDays int) (int, error) {
	return 0, nil
}

type stubFactExtractor struct {
	extractFunc func(context.Context, *application.ExtractFactsRequest) error
}

func (e *stubFactExtractor) ExtractFacts(ctx context.Context, req *application.ExtractFactsRequest) error {
	return e.extractFunc(ctx, req)
}

func TestExtractionWorker_ProcessesTask(t *testing.T) {
	var extracted *application.ExtractFactsRequest
	extractor := &stubFactExtractor{
		extractFunc: func(ctx context.Context, req *application.ExtractFactsRequest) error {
			extracted = req
			return nil
		},
	}

	var completedID int64
	queue := &stubExtractionQueue{
		dequeueFunc: func(ctx context.Context, tenantID string) (*port.ExtractionTask, error) {
			return &port.ExtractionTask{
				ID:        123,
				TenantID:  "tenant1",
				UserID:    "user1",
				AgentID:   "agent1",
				MessageID: "msg1",
				Content:   "I like coffee",
			}, nil
		},
		markCompletedFunc: func(ctx context.Context, tenantID string, taskID int64) error {
			completedID = taskID
			return nil
		},
	}

	worker := workers.NewExtractionWorker("tenant1", queue, extractor, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	worker.Start(ctx)

	require.NotNil(t, extracted, "should call ExtractFacts")
	require.Equal(t, "user1", extracted.UserID)
	require.Equal(t, int64(123), completedID, "should mark completed")
}

func TestExtractionWorker_HandlesExtractionError(t *testing.T) {
	extractor := &stubFactExtractor{
		extractFunc: func(ctx context.Context, req *application.ExtractFactsRequest) error {
			return errors.New("llm timeout")
		},
	}

	var failedID int64
	var failReason string
	queue := &stubExtractionQueue{
		dequeueFunc: func(ctx context.Context, tenantID string) (*port.ExtractionTask, error) {
			return &port.ExtractionTask{ID: 456, TenantID: "tenant1", Content: "test"}, nil
		},
		markFailedFunc: func(ctx context.Context, tenantID string, taskID int64, errMsg string) error {
			failedID = taskID
			failReason = errMsg
			return nil
		},
	}

	worker := workers.NewExtractionWorker("tenant1", queue, extractor, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	worker.Start(ctx)

	require.Equal(t, int64(456), failedID, "should mark failed")
	require.Contains(t, failReason, "llm timeout")
}

func TestExtractionWorker_GracefulShutdown(t *testing.T) {
	queue := &stubExtractionQueue{
		dequeueFunc: func(ctx context.Context, tenantID string) (*port.ExtractionTask, error) {
			return nil, nil // idle
		},
	}

	worker := workers.NewExtractionWorker("tenant1", queue, &stubFactExtractor{}, zap.NewNop())

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
