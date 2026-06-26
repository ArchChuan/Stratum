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

const testQueueTenant = "tenant_test_queue"

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

	err := queue.Enqueue(ctx, testQueueTenant, task)
	require.NoError(t, err)
	require.NotZero(t, task.ID, "ID should be assigned")

	dequeued, err := queue.Dequeue(ctx, testQueueTenant)
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
	require.NoError(t, queue.Enqueue(ctx, testQueueTenant, task))

	require.NoError(t, queue.MarkCompleted(ctx, testQueueTenant, task.ID))

	dequeued, err := queue.Dequeue(ctx, testQueueTenant)
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
	require.NoError(t, queue.Enqueue(ctx, testQueueTenant, task))

	testErr := "extraction failed: timeout"
	require.NoError(t, queue.MarkFailed(ctx, testQueueTenant, task.ID, testErr))
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
	require.NoError(t, queue.Enqueue(ctx, testQueueTenant, task))
	require.NoError(t, queue.MarkCompleted(ctx, testQueueTenant, task.ID))

	count, err := queue.DeleteOldCompleted(ctx, testQueueTenant, 0)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}
