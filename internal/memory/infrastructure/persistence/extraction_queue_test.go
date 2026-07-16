package persistence_test

import (
	"context"
	"testing"
	"time"

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

func TestExtractionQueue_EnqueueIsIdempotentByMessageID(t *testing.T) {
	pool, queue := setupQueueTest(t)
	ctx := context.Background()
	first := &port.ExtractionTask{MessageID: "stable-message", UserID: "user123", Content: "first"}
	duplicate := &port.ExtractionTask{MessageID: "stable-message", UserID: "user123", Content: "duplicate"}

	require.NoError(t, queue.Enqueue(ctx, testQueueTenant, first))
	require.NoError(t, queue.Enqueue(ctx, testQueueTenant, duplicate))
	require.Equal(t, first.ID, duplicate.ID)

	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM tenant_test_queue.memory_extraction_queue WHERE message_id=$1`, first.MessageID).Scan(&count))
	require.Equal(t, 1, count)
}

func TestExtractionQueue_DequeueReclaimsExpiredProcessingTask(t *testing.T) {
	pool, queue := setupQueueTest(t)
	ctx := context.Background()
	task := &port.ExtractionTask{MessageID: "abandoned-message", UserID: "user123", Content: "content"}
	require.NoError(t, queue.Enqueue(ctx, testQueueTenant, task))
	require.NoError(t, pool.QueryRow(ctx, `
		UPDATE tenant_test_queue.memory_extraction_queue
		SET status='processing', updated_at=NOW() - interval '10 minutes'
		WHERE id=$1 RETURNING updated_at`, task.ID).Scan(&task.UpdatedAt))

	dequeued, err := queue.Dequeue(ctx, testQueueTenant)
	require.NoError(t, err)
	require.NotNil(t, dequeued)
	require.Equal(t, task.ID, dequeued.ID)
	require.WithinDuration(t, time.Now(), dequeued.UpdatedAt, 5*time.Second)
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
	claimed, err := queue.Dequeue(ctx, testQueueTenant)
	require.NoError(t, err)

	require.NoError(t, queue.MarkCompleted(ctx, testQueueTenant, task.ID, claimed.UpdatedAt))

	dequeued, err := queue.Dequeue(ctx, testQueueTenant)
	require.NoError(t, err)
	require.Nil(t, dequeued, "completed tasks should not be returned")
}

func TestExtractionQueue_MarkFailed(t *testing.T) {
	pool, queue := setupQueueTest(t)
	ctx := context.Background()

	task := &port.ExtractionTask{
		MessageID: "msg123",
		UserID:    "user123",
		AgentID:   "agent456",
		Content:   "Test content",
	}
	require.NoError(t, queue.Enqueue(ctx, testQueueTenant, task))
	claimed, err := queue.Dequeue(ctx, testQueueTenant)
	require.NoError(t, err)

	testErr := "provider rejected secret-token-123"
	require.NoError(t, queue.MarkFailed(ctx, testQueueTenant, task.ID, claimed.UpdatedAt, testErr))

	var status, storedError string
	var retryCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT status, retry_count, error_msg FROM tenant_test_queue.memory_extraction_queue WHERE id=$1`, task.ID).Scan(&status, &retryCount, &storedError))
	require.Equal(t, "pending", status)
	require.Equal(t, 1, retryCount)
	require.Equal(t, "extraction_failed", storedError)
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
	claimed, err := queue.Dequeue(ctx, testQueueTenant)
	require.NoError(t, err)
	require.NoError(t, queue.MarkCompleted(ctx, testQueueTenant, task.ID, claimed.UpdatedAt))

	count, err := queue.DeleteOldCompleted(ctx, testQueueTenant, 0)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestExtractionQueue_RejectsFinalizationFromExpiredClaim(t *testing.T) {
	pool, queue := setupQueueTest(t)
	ctx := context.Background()
	task := &port.ExtractionTask{MessageID: "lease-race", UserID: "user123", Content: "content"}
	require.NoError(t, queue.Enqueue(ctx, testQueueTenant, task))

	firstClaim, err := queue.Dequeue(ctx, testQueueTenant)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE tenant_test_queue.memory_extraction_queue SET updated_at=NOW() - interval '10 minutes' WHERE id=$1`, task.ID)
	require.NoError(t, err)
	secondClaim, err := queue.Dequeue(ctx, testQueueTenant)
	require.NoError(t, err)

	require.Error(t, queue.MarkCompleted(ctx, testQueueTenant, task.ID, firstClaim.UpdatedAt))
	require.NoError(t, queue.MarkCompleted(ctx, testQueueTenant, task.ID, secondClaim.UpdatedAt))
}
