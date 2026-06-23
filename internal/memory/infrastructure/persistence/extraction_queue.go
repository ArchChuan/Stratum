package persistence

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ExtractionQueue implements domain/port.ExtractionQueue using PostgreSQL with FOR UPDATE SKIP LOCKED.
type ExtractionQueue struct {
	pool *pgxpool.Pool
}

// NewExtractionQueue creates a new extraction queue.
func NewExtractionQueue(pool *pgxpool.Pool) *ExtractionQueue {
	return &ExtractionQueue{pool: pool}
}

// Enqueue adds a new extraction task.
func (q *ExtractionQueue) Enqueue(ctx context.Context, task *port.ExtractionTask) error {
	query := `
		INSERT INTO memory_extraction_queue (
			message_id, user_id, agent_id, content, status, retry_count
		) VALUES (
			$1, $2, $3, $4, 'pending', 0
		) RETURNING id`

	var agentID *string
	if task.AgentID != "" {
		agentID = &task.AgentID
	}

	err := q.pool.QueryRow(ctx, query,
		task.MessageID, task.UserID, agentID, task.Content,
	).Scan(&task.ID)

	if err != nil {
		return fmt.Errorf("enqueue extraction task: %w", err)
	}

	return nil
}

// Dequeue fetches the next pending task using FOR UPDATE SKIP LOCKED to prevent concurrent processing.
func (q *ExtractionQueue) Dequeue(ctx context.Context) (*port.ExtractionTask, error) {
	query := `
		UPDATE memory_extraction_queue
		SET status = 'processing', updated_at = NOW()
		WHERE id = (
			SELECT id FROM memory_extraction_queue
			WHERE status = 'pending'
			ORDER BY created_at ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, message_id, user_id, agent_id, content, status, retry_count, error_msg, created_at, updated_at`

	var task port.ExtractionTask
	var agentID, errorMsg *string

	err := q.pool.QueryRow(ctx, query).Scan(
		&task.ID, &task.MessageID, &task.UserID, &agentID, &task.Content,
		&task.Status, &task.RetryCount, &errorMsg, &task.CreatedAt, &task.UpdatedAt,
	)

	if err != nil {
		// No rows means queue is empty
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("dequeue extraction task: %w", err)
	}

	if agentID != nil {
		task.AgentID = *agentID
	}

	return &task, nil
}

// MarkCompleted marks a task as successfully processed.
func (q *ExtractionQueue) MarkCompleted(ctx context.Context, taskID int64) error {
	query := `
		UPDATE memory_extraction_queue
		SET status = 'completed', updated_at = NOW()
		WHERE id = $1`

	tag, err := q.pool.Exec(ctx, query, taskID)
	if err != nil {
		return fmt.Errorf("mark task completed: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("task %d not found", taskID)
	}

	return nil
}

// MarkFailed increments retry count or marks permanently failed.
func (q *ExtractionQueue) MarkFailed(ctx context.Context, taskID int64, errMsg string) error {
	// Retry up to 3 times, then mark as permanently failed
	query := `
		UPDATE memory_extraction_queue
		SET
			status = CASE
				WHEN retry_count < 2 THEN 'pending'
				ELSE 'failed'
			END,
			retry_count = retry_count + 1,
			error_msg = $2,
			updated_at = NOW()
		WHERE id = $1`

	tag, err := q.pool.Exec(ctx, query, taskID, errMsg)
	if err != nil {
		return fmt.Errorf("mark task failed: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("task %d not found", taskID)
	}

	return nil
}

// DeleteOldCompleted removes completed tasks older than retention days.
func (q *ExtractionQueue) DeleteOldCompleted(ctx context.Context, retentionDays int) (int, error) {
	query := `
		DELETE FROM memory_extraction_queue
		WHERE status = 'completed'
			AND updated_at < NOW() - ($1 || ' days')::INTERVAL`

	tag, err := q.pool.Exec(ctx, query, retentionDays)
	if err != nil {
		return 0, fmt.Errorf("delete old completed tasks: %w", err)
	}

	return int(tag.RowsAffected()), nil
}
