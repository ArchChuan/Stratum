package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

// ExtractionQueue implements domain/port.ExtractionQueue using PostgreSQL with FOR UPDATE SKIP LOCKED.
type ExtractionQueue struct {
	pool *pgxpool.Pool
}

func NewExtractionQueue(pool *pgxpool.Pool) *ExtractionQueue {
	return &ExtractionQueue{pool: pool}
}

func (q *ExtractionQueue) execTenant(ctx context.Context, tenantID string, fn func(pgx.Tx) error) error {
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, fmt.Sprintf(`SET LOCAL search_path = "tenant_%s", public`, tenantID)); err != nil {
		return fmt.Errorf("set search_path: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (q *ExtractionQueue) Enqueue(ctx context.Context, tenantID string, task *port.ExtractionTask) error {
	var agentID *string
	if task.AgentID != "" {
		agentID = &task.AgentID
	}
	var conversationID *string
	if task.ConversationID != "" {
		conversationID = &task.ConversationID
	}
	err := q.execTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO memory_extraction_queue
				(message_id, user_id, agent_id, conversation_id, scope, content, status, retry_count)
			VALUES ($1, $2, $3, $4, $5, $6, 'pending', 0)
			RETURNING id`,
			task.MessageID, task.UserID, agentID, conversationID, task.Scope, task.Content,
		).Scan(&task.ID)
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == "memory_extraction_queue_conversation_id_fkey" {
			// conversation was deleted; enqueue without the stale reference
			return q.execTenant(ctx, tenantID, func(tx pgx.Tx) error {
				return tx.QueryRow(ctx, `
					INSERT INTO memory_extraction_queue
						(message_id, user_id, agent_id, conversation_id, scope, content, status, retry_count)
					VALUES ($1, $2, $3, NULL, $4, $5, 'pending', 0)
					RETURNING id`,
					task.MessageID, task.UserID, agentID, task.Scope, task.Content,
				).Scan(&task.ID)
			})
		}
	}
	return err
}

func (q *ExtractionQueue) Dequeue(ctx context.Context, tenantID string) (*port.ExtractionTask, error) {
	var result *port.ExtractionTask
	err := q.execTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var task port.ExtractionTask
		var agentID, conversationID, errorMsg *string
		err := tx.QueryRow(ctx, `
			UPDATE memory_extraction_queue
			SET status = 'processing', updated_at = NOW()
			WHERE id = (
				SELECT id FROM memory_extraction_queue
				WHERE status = 'pending'
				ORDER BY created_at ASC
				LIMIT 1
				FOR UPDATE SKIP LOCKED
			)
			RETURNING id, message_id, user_id, agent_id, conversation_id, scope, content,
			          status, retry_count, error_msg, created_at, updated_at`,
		).Scan(&task.ID, &task.MessageID, &task.UserID, &agentID, &conversationID,
			&task.Scope, &task.Content, &task.Status, &task.RetryCount, &errorMsg,
			&task.CreatedAt, &task.UpdatedAt)
		if err != nil {
			if err.Error() == "no rows in result set" {
				return nil
			}
			return fmt.Errorf("dequeue: %w", err)
		}
		if agentID != nil {
			task.AgentID = *agentID
		}
		if conversationID != nil {
			task.ConversationID = *conversationID
		}
		task.TenantID = tenantID
		result = &task
		return nil
	})
	return result, err
}

func (q *ExtractionQueue) MarkCompleted(ctx context.Context, tenantID string, taskID int64) error {
	return q.execTenant(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE memory_extraction_queue SET status='completed', updated_at=NOW() WHERE id=$1`, taskID)
		if err != nil {
			return fmt.Errorf("mark completed: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("task %d not found", taskID)
		}
		return nil
	})
}

func (q *ExtractionQueue) MarkFailed(ctx context.Context, tenantID string, taskID int64, errMsg string) error {
	return q.execTenant(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE memory_extraction_queue SET
				status = CASE WHEN retry_count < 2 THEN 'pending' ELSE 'failed' END,
				retry_count = retry_count + 1,
				error_msg = $2,
				updated_at = NOW()
			WHERE id = $1`, taskID, errMsg)
		if err != nil {
			return fmt.Errorf("mark failed: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("task %d not found", taskID)
		}
		return nil
	})
}

func (q *ExtractionQueue) DeleteOldCompleted(ctx context.Context, tenantID string, retentionDays int) (int, error) {
	var n int
	err := q.execTenant(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			DELETE FROM memory_extraction_queue
			WHERE status = 'completed'
			  AND updated_at < NOW() - ($1 * interval '1 day')`, retentionDays)
		if err != nil {
			return fmt.Errorf("delete old completed: %w", err)
		}
		n = int(tag.RowsAffected())
		return nil
	})
	return n, err
}
