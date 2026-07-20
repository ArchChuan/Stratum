package workers_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
)

type stubExtractionQueue struct {
	dequeueFunc       func(context.Context, string) (*port.ExtractionTask, error)
	markCompletedFunc func(context.Context, string, int64, time.Time) error
	markFailedFunc    func(context.Context, string, int64, time.Time, string) error
}

func (q *stubExtractionQueue) Enqueue(ctx context.Context, tenantID string, task *port.ExtractionTask) error {
	return nil
}

func (q *stubExtractionQueue) Dequeue(ctx context.Context, tenantID string) (*port.ExtractionTask, error) {
	return q.dequeueFunc(ctx, tenantID)
}

func (q *stubExtractionQueue) MarkCompleted(ctx context.Context, tenantID string, taskID int64, claimedAt time.Time) error {
	return q.markCompletedFunc(ctx, tenantID, taskID, claimedAt)
}

func (q *stubExtractionQueue) MarkFailed(ctx context.Context, tenantID string, taskID int64, claimedAt time.Time, errMsg string) error {
	return q.markFailedFunc(ctx, tenantID, taskID, claimedAt, errMsg)
}

func (q *stubExtractionQueue) DeleteOldCompleted(ctx context.Context, tenantID string, retentionDays int) (int, error) {
	return 0, nil
}

type stubFactExtractor struct {
	extractFunc func(context.Context, *port.ExtractFactsRequest) error
}

func (e *stubFactExtractor) ExtractFacts(ctx context.Context, req *port.ExtractFactsRequest) error {
	return e.extractFunc(ctx, req)
}

func TestExtractionWorker_ProcessesTask(t *testing.T) {
	var extracted *port.ExtractFactsRequest
	extractor := &stubFactExtractor{
		extractFunc: func(ctx context.Context, req *port.ExtractFactsRequest) error {
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
		markCompletedFunc: func(ctx context.Context, tenantID string, taskID int64, claimedAt time.Time) error {
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
	require.Equal(t, "msg1", extracted.SourceMessageID)
	require.Equal(t, int64(123), extracted.SourceTaskID)
	require.Equal(t, int64(123), completedID, "should mark completed")
}

func TestExtractionWorker_MarkCompletedFailureReplaysSameSourceIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	claimedAt := time.Now().UTC()
	task := &port.ExtractionTask{ID: 123, TenantID: "tenant1", UserID: "user1", MessageID: "msg1", Content: "fact", UpdatedAt: claimedAt}
	var requests []*port.ExtractFactsRequest
	extractor := &stubFactExtractor{extractFunc: func(_ context.Context, req *port.ExtractFactsRequest) error {
		copy := *req
		requests = append(requests, &copy)
		return nil
	}}
	dequeues := 0
	completions := 0
	queue := &stubExtractionQueue{
		dequeueFunc: func(context.Context, string) (*port.ExtractionTask, error) {
			dequeues++
			if dequeues <= 2 {
				copy := *task
				return &copy, nil
			}
			cancel()
			return nil, nil
		},
		markCompletedFunc: func(context.Context, string, int64, time.Time) error {
			completions++
			if completions == 1 {
				return errors.New("commit status unavailable")
			}
			return nil
		},
	}

	workers.NewExtractionWorker("tenant1", queue, extractor, zap.NewNop()).Start(ctx)
	require.Len(t, requests, 2)
	require.Equal(t, "msg1", requests[0].SourceMessageID)
	require.Equal(t, requests[0].SourceMessageID, requests[1].SourceMessageID)
	require.Equal(t, int64(123), requests[0].SourceTaskID)
	require.Equal(t, requests[0].SourceTaskID, requests[1].SourceTaskID)
}

func TestExtractionWorker_HandlesExtractionError(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	extractor := &stubFactExtractor{
		extractFunc: func(ctx context.Context, req *port.ExtractFactsRequest) error {
			return errors.New("llm timeout: secret-token-123")
		},
	}

	var failedID int64
	var failReason string
	queue := &stubExtractionQueue{
		dequeueFunc: func(ctx context.Context, tenantID string) (*port.ExtractionTask, error) {
			return &port.ExtractionTask{ID: 456, TenantID: "tenant1", Content: "test"}, nil
		},
		markFailedFunc: func(ctx context.Context, tenantID string, taskID int64, claimedAt time.Time, errMsg string) error {
			failedID = taskID
			failReason = errMsg
			return nil
		},
	}

	worker := workers.NewExtractionWorker("tenant1", queue, extractor, logger)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	worker.Start(ctx)

	require.Equal(t, int64(456), failedID, "should mark failed")
	require.Equal(t, "extraction_failed", failReason)
	for _, entry := range observed.All() {
		require.NotContains(t, entry.Message+fmt.Sprint(entry.ContextMap()), "secret-token-123")
	}
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
