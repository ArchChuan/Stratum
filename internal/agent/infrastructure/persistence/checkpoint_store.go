package persistence

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/jackc/pgx/v5"
)

type PgCheckpointStore struct {
	pool chatPoolIface
}

func NewPgCheckpointStore(pool chatPoolIface) *PgCheckpointStore {
	return &PgCheckpointStore{pool: pool}
}

func (s *PgCheckpointStore) Upsert(
	ctx context.Context, tenantID string, checkpoint domain.AgentExecutionCheckpoint,
) error {
	return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if checkpoint.MessagesSnapshotJSON == nil {
			checkpoint.MessagesSnapshotJSON = json.RawMessage("[]")
		}
		if checkpoint.PendingToolCallsJSON == nil {
			checkpoint.PendingToolCallsJSON = json.RawMessage("[]")
		}
		if checkpoint.CompletedToolCallsJSON == nil {
			checkpoint.CompletedToolCallsJSON = json.RawMessage("[]")
		}
		if checkpoint.RuntimeStateJSON == nil {
			checkpoint.RuntimeStateJSON = json.RawMessage("{}")
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO agent_execution_checkpoints
			 (execution_id, trace_id, conversation_id, agent_id, user_id, current_node,
			  step_index, messages_snapshot_json, pending_tool_calls_json, completed_tool_calls_json,
			  runtime_state_json, status, resume_reason, expires_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			 ON CONFLICT (execution_id) DO UPDATE SET
			    trace_id = EXCLUDED.trace_id,
			    conversation_id = EXCLUDED.conversation_id,
			    agent_id = EXCLUDED.agent_id,
			    user_id = EXCLUDED.user_id,
			    current_node = EXCLUDED.current_node,
			    step_index = EXCLUDED.step_index,
			    messages_snapshot_json = EXCLUDED.messages_snapshot_json,
			    pending_tool_calls_json = EXCLUDED.pending_tool_calls_json,
			    completed_tool_calls_json = EXCLUDED.completed_tool_calls_json,
			    runtime_state_json = EXCLUDED.runtime_state_json,
			    status = EXCLUDED.status,
			    resume_reason = EXCLUDED.resume_reason,
			    updated_at = NOW(),
			    expires_at = EXCLUDED.expires_at`,
			checkpoint.ExecutionID, checkpoint.TraceID, checkpoint.ConversationID,
			checkpoint.AgentID, checkpoint.UserID, checkpoint.CurrentNode, checkpoint.StepIndex,
			string(checkpoint.MessagesSnapshotJSON), string(checkpoint.PendingToolCallsJSON),
			string(checkpoint.CompletedToolCallsJSON), string(checkpoint.RuntimeStateJSON),
			checkpoint.Status, checkpoint.ResumeReason, checkpoint.ExpiresAt,
		)
		if err != nil {
			return fmt.Errorf("checkpoint_store: upsert: %w", err)
		}
		return nil
	})
}

// DeleteExpired removes checkpoints whose expires_at has passed,
// excluding completed and failed ones. Returns the number of rows deleted.
func (s *PgCheckpointStore) DeleteExpired(ctx context.Context, tenantID string) (int64, error) {
	var deleted int64
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM agent_execution_checkpoints
			  WHERE expires_at < NOW()
			    AND status NOT IN ('completed', 'failed', 'expired')`)
		if err != nil {
			return fmt.Errorf("checkpoint_store: delete expired: %w", err)
		}
		deleted = tag.RowsAffected()
		return nil
	})
	return deleted, err
}

func (s *PgCheckpointStore) GetLatest(
	ctx context.Context, tenantID, executionID string,
) (*domain.AgentExecutionCheckpoint, error) {
	var checkpoint domain.AgentExecutionCheckpoint
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id, execution_id, trace_id, conversation_id, agent_id, user_id,
			        current_node, step_index, messages_snapshot_json, pending_tool_calls_json,
			        completed_tool_calls_json, runtime_state_json, status, resume_reason,
			        created_at, updated_at, expires_at
			   FROM agent_execution_checkpoints
			  WHERE execution_id = $1
			  ORDER BY updated_at DESC
			  LIMIT 1`,
			executionID,
		).Scan(
			&checkpoint.ID, &checkpoint.ExecutionID, &checkpoint.TraceID, &checkpoint.ConversationID,
			&checkpoint.AgentID, &checkpoint.UserID, &checkpoint.CurrentNode, &checkpoint.StepIndex,
			&checkpoint.MessagesSnapshotJSON, &checkpoint.PendingToolCallsJSON,
			&checkpoint.CompletedToolCallsJSON, &checkpoint.RuntimeStateJSON, &checkpoint.Status,
			&checkpoint.ResumeReason, &checkpoint.CreatedAt, &checkpoint.UpdatedAt, &checkpoint.ExpiresAt,
		)
	})
	if err != nil {
		return nil, fmt.Errorf("checkpoint_store: get latest: %w", err)
	}
	return &checkpoint, nil
}

func (s *PgCheckpointStore) MarkCompleted(ctx context.Context, tenantID, executionID string) error {
	return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE agent_execution_checkpoints
			    SET status = 'completed', updated_at = NOW()
			  WHERE execution_id = $1 AND status = 'running'`,
			executionID,
		)
		if err != nil {
			return fmt.Errorf("checkpoint_store: mark completed: %w", err)
		}
		return nil
	})
}

// UpdateStatus transitions a checkpoint to the given status. Only updates
// when the current status is 'running' to prevent overwriting terminal states.
func (s *PgCheckpointStore) UpdateStatus(ctx context.Context, tenantID, executionID, status string) error {
	return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE agent_execution_checkpoints
			    SET status = $1, updated_at = NOW()
			  WHERE execution_id = $2 AND status = 'running'`,
			status, executionID,
		)
		if err != nil {
			return fmt.Errorf("checkpoint_store: update status: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("checkpoint_store: update status: no running checkpoint for execution %s", executionID)
		}
		return nil
	})
}
