package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

// ExtractionQueue implements domain/port.ExtractionQueue using PostgreSQL with FOR UPDATE SKIP LOCKED.
type ExtractionQueue struct {
	pool *pgxpool.Pool
}

func NewExtractionQueue(pool *pgxpool.Pool) *ExtractionQueue {
	return &ExtractionQueue{pool: pool}
}

func (q *ExtractionQueue) execTenant(ctx context.Context, tenantID string, fn func(pgx.Tx) error) error {
	return pgstore.Wrap(q.pool).ExecTenant(ctx, tenantID, func(_ context.Context, tx pgx.Tx) error {
		return fn(tx)
	})
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
	enqueue := func(conversationID *string) error {
		return q.execTenant(ctx, tenantID, func(tx pgx.Tx) error {
			// Serialize by stable source identity so a successful enqueue followed by
			// a Redis cleanup failure cannot create a second extraction task.
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, task.MessageID); err != nil {
				return fmt.Errorf("lock extraction message: %w", err)
			}
			if err := tx.QueryRow(ctx, `SELECT id FROM memory_extraction_queue WHERE message_id=$1 LIMIT 1`, task.MessageID).Scan(&task.ID); err == nil {
				return nil
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("find extraction message: %w", err)
			}
			return tx.QueryRow(ctx, `
				INSERT INTO memory_extraction_queue
					(message_id, user_id, agent_id, conversation_id, scope, content, status, retry_count)
				VALUES ($1, $2, $3, $4, $5, $6, 'pending', 0)
				RETURNING id`,
				task.MessageID, task.UserID, agentID, conversationID, task.Scope, task.Content,
			).Scan(&task.ID)
		})
	}
	err := enqueue(conversationID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == "memory_extraction_queue_conversation_id_fkey" {
			// conversation was deleted; enqueue without the stale reference
			return enqueue(nil)
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
			   OR (status = 'processing' AND updated_at < NOW() - make_interval(secs => $1))
				ORDER BY created_at ASC
				LIMIT 1
				FOR UPDATE SKIP LOCKED
			)
			RETURNING id, message_id, user_id, agent_id, conversation_id, scope, content,
			          status, retry_count, error_msg, created_at, updated_at`,
			constants.MemoryExtractionLease.Seconds()).Scan(&task.ID, &task.MessageID, &task.UserID, &agentID, &conversationID,
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

func (q *ExtractionQueue) MarkCompleted(ctx context.Context, tenantID string, taskID int64, claimedAt time.Time) error {
	return q.execTenant(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE memory_extraction_queue SET status='completed', error_msg=NULL, updated_at=NOW() WHERE id=$1 AND status='processing' AND updated_at=$2`, taskID, claimedAt)
		if err != nil {
			return fmt.Errorf("mark completed: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("task %d claim expired", taskID)
		}
		return nil
	})
}

func (q *ExtractionQueue) MarkFailed(ctx context.Context, tenantID string, taskID int64, claimedAt time.Time, errMsg string) error {
	errMsg = safeExtractionErrorCode(errMsg)
	return q.execTenant(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE memory_extraction_queue SET
				status = CASE WHEN retry_count < 2 THEN 'pending' ELSE 'failed' END,
				retry_count = retry_count + 1,
				error_msg = $3,
				updated_at = NOW()
			WHERE id = $1 AND status = 'processing' AND updated_at=$2`, taskID, claimedAt, errMsg)
		if err != nil {
			return fmt.Errorf("mark failed: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("task %d claim expired", taskID)
		}
		return nil
	})
}

func safeExtractionErrorCode(value string) string {
	switch value {
	case "extraction_failed", "extraction_panic", "invalid_payload":
		return value
	default:
		return "extraction_failed"
	}
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
