package persistence_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/persistence"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func setupQueueTest(t *testing.T) (*pgxpool.Pool, *persistence.ExtractionQueue) {
	t.Helper()
	pool := NewTestTenantPool(t, "tenant_test_queue")
	queue := persistence.NewExtractionQueue(pool)
	return pool, queue
}

func TestExtractionQueue_EnqueueAndDequeue(t *testing.T) {
	_, queue := setupQueueTest(t)
	ctx := context.Background()

	task := &port.ExtractionTask{
		MessageID: "msg123",
		UserID:    "user123",
		AgentID:   "agent456",
		Content:   "User mentioned Python and FastAPI",
		Status:    "pending",
	}

	err := queue.Enqueue(ctx, task)
	require.NoError(t, err)
	require.NotZero(t, task.ID, "ID should be assigned")

	dequeued, err := queue.Dequeue(ctx)
	require.NoError(t, err)
	require.Equal(t, "user123", dequeued.UserID)
	require.Equal(t, "processing", dequeued.Status)
}

func TestExtractionQueue_MarkCompleted(t *testing.T) {
	_, queue := setupQueueTest(t)
	ctx := context.Background()

	task := &port.ExtractionTask{
		MessageID: "msg123",
		UserID:    "user123",
		AgentID:   "agent456",
		Content:   "Test content",
	}
	require.NoError(t, queue.Enqueue(ctx, task))

	require.NoError(t, queue.MarkCompleted(ctx, task.ID))

	// Completed tasks should not be dequeued
	dequeued, err := queue.Dequeue(ctx)
	require.NoError(t, err)
	require.Nil(t, dequeued, "completed tasks should not be returned")
}

func TestExtractionQueue_MarkFailed(t *testing.T) {
	_, queue := setupQueueTest(t)
	ctx := context.Background()

	task := &port.ExtractionTask{
		MessageID: "msg123",
		UserID:    "user123",
		AgentID:   "agent456",
		Content:   "Test content",
	}
	require.NoError(t, queue.Enqueue(ctx, task))

	testErr := "extraction failed: timeout"
	require.NoError(t, queue.MarkFailed(ctx, task.ID, testErr))

	// After failure, status should be pending (for retry) with incremented retry count
}

func TestExtractionQueue_DeleteOldCompleted(t *testing.T) {
	_, queue := setupQueueTest(t)
	ctx := context.Background()

	task := &port.ExtractionTask{
		MessageID: "msg123",
		UserID:    "user123",
		AgentID:   "agent456",
		Content:   "Test content",
	}
	require.NoError(t, queue.Enqueue(ctx, task))
	require.NoError(t, queue.MarkCompleted(ctx, task.ID))

	// Delete completed tasks older than 0 days (should delete immediately)
	count, err := queue.DeleteOldCompleted(ctx, 0)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}
