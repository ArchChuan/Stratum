package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
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
		// run_generation 的零值归一为 1（与列 DEFAULT 一致）：旧路径（非续跑）
		// 的 checkpoint 不关心分代，但显式写 0 会与 AdvanceRunGeneration(expect=1)
		// 的抢占断言错位。
		if checkpoint.RunGeneration == 0 {
			checkpoint.RunGeneration = 1
		}
		// conversation_id UUID 列不接受空字符串(SQLSTATE 22P02);无
		// conversation 的执行(如 evaluation run)必须写 NULL。
		var conversationID any
		if checkpoint.ConversationID == "" {
			conversationID = nil
		} else {
			conversationID = checkpoint.ConversationID
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO agent_execution_checkpoints
			 (execution_id, trace_id, conversation_id, agent_id, user_id, current_node,
			  step_index, messages_snapshot_json, pending_tool_calls_json, completed_tool_calls_json,
			  runtime_state_json, status, resume_reason, expires_at, user_query, run_generation)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
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
			checkpoint.ExecutionID, checkpoint.TraceID, conversationID,
			checkpoint.AgentID, checkpoint.UserID, checkpoint.CurrentNode, checkpoint.StepIndex,
			string(checkpoint.MessagesSnapshotJSON), string(checkpoint.PendingToolCallsJSON),
			string(checkpoint.CompletedToolCallsJSON), string(checkpoint.RuntimeStateJSON),
			checkpoint.Status, checkpoint.ResumeReason, checkpoint.ExpiresAt,
			checkpoint.UserQuery, checkpoint.RunGeneration,
		)
		if err != nil {
			return fmt.Errorf("checkpoint_store: upsert: %w", err)
		}
		return nil
	})
}

// DeleteExpired removes checkpoints whose expires_at has passed. Running/paused
// rows inherit PlanCheckpointTTL via Upsert; terminal rows (completed/failed/
// expired) gain CheckpointTerminalTTL at transition time, so finished executions
// are reclaimed a fixed window after they finish rather than kept forever.
// Returns the number of rows deleted.
func (s *PgCheckpointStore) DeleteExpired(ctx context.Context, tenantID string) (int64, error) {
	var deleted int64
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM agent_execution_checkpoints
			  WHERE expires_at < NOW()`)
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
			`SELECT id, execution_id, trace_id, COALESCE(conversation_id::text, '') AS conversation_id,
				        agent_id, user_id,
			        current_node, step_index, messages_snapshot_json, pending_tool_calls_json,
			        completed_tool_calls_json, runtime_state_json, status, resume_reason,
			        user_query, run_generation,
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
			&checkpoint.ResumeReason, &checkpoint.UserQuery, &checkpoint.RunGeneration,
			&checkpoint.CreatedAt, &checkpoint.UpdatedAt, &checkpoint.ExpiresAt,
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
			    SET status = 'completed', updated_at = NOW(),
			        expires_at = NOW() + $2::interval
			  WHERE execution_id = $1 AND status = 'running'`,
			executionID, constants.CheckpointTerminalTTL,
		)
		if err != nil {
			return fmt.Errorf("checkpoint_store: mark completed: %w", err)
		}
		return nil
	})
}

// UpdateStatus transitions a checkpoint to the given status. Only updates
// when the current status is 'running' to prevent overwriting terminal states.
// Terminal transitions (completed/failed/expired) refresh expires_at with
// CheckpointTerminalTTL so DeleteExpired reclaims them after the retention
// window instead of keeping them forever.
func (s *PgCheckpointStore) UpdateStatus(ctx context.Context, tenantID, executionID, status string) error {
	return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE agent_execution_checkpoints
			    SET status = $1, updated_at = NOW(),
			        expires_at = CASE WHEN $1 IN ('completed', 'failed', 'expired')
			                          THEN NOW() + $3::interval
			                          ELSE expires_at END
			  WHERE execution_id = $2 AND status = 'running'`,
			status, executionID, constants.CheckpointTerminalTTL,
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

// GetLatestActiveByConversation returns the freshest active checkpoint for a
// conversation. The freshness window guards running/paused rows only: a
// waiting_approval row does not advance updated_at while a human reviews the
// approval, so it is gated solely by expires_at. Returns (nil, nil) when no
// active checkpoint exists.
func (s *PgCheckpointStore) GetLatestActiveByConversation(
	ctx context.Context, tenantID, conversationID string,
) (*domain.AgentExecutionCheckpoint, error) {
	var checkpoint domain.AgentExecutionCheckpoint
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id, execution_id, trace_id, COALESCE(conversation_id::text, '') AS conversation_id,
			        agent_id, user_id,
			        current_node, step_index, messages_snapshot_json, pending_tool_calls_json,
			        completed_tool_calls_json, runtime_state_json, status, resume_reason,
			        user_query, run_generation,
			        created_at, updated_at, expires_at
			   FROM agent_execution_checkpoints
			  WHERE conversation_id = $1::uuid
			    AND status IN ('running', 'paused', 'waiting_approval')
			    AND expires_at > NOW()
			    AND (status = 'waiting_approval' OR updated_at > NOW() - $2::interval)
			  ORDER BY updated_at DESC
			  LIMIT 1`,
			conversationID, constants.ActiveExecutionFreshnessWindow,
		).Scan(
			&checkpoint.ID, &checkpoint.ExecutionID, &checkpoint.TraceID, &checkpoint.ConversationID,
			&checkpoint.AgentID, &checkpoint.UserID, &checkpoint.CurrentNode, &checkpoint.StepIndex,
			&checkpoint.MessagesSnapshotJSON, &checkpoint.PendingToolCallsJSON,
			&checkpoint.CompletedToolCallsJSON, &checkpoint.RuntimeStateJSON, &checkpoint.Status,
			&checkpoint.ResumeReason, &checkpoint.UserQuery, &checkpoint.RunGeneration,
			&checkpoint.CreatedAt, &checkpoint.UpdatedAt, &checkpoint.ExpiresAt,
		)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("checkpoint_store: get active by conversation: %w", err)
	}
	return &checkpoint, nil
}

// UpdateStatusFrom CAS-transitions a checkpoint between two statuses. Used to
// claim a waiting_approval checkpoint before resuming: only the winner of the
// CAS may continue the execution.
func (s *PgCheckpointStore) UpdateStatusFrom(ctx context.Context, tenantID, executionID, from, to string) error {
	return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE agent_execution_checkpoints
			    SET status = $1, updated_at = NOW(),
			        expires_at = CASE WHEN $1 IN ('completed', 'failed', 'expired')
			                          THEN NOW() + $3::interval
			                          ELSE expires_at END
			  WHERE execution_id = $2 AND status = $4`,
			to, executionID, constants.CheckpointTerminalTTL, from,
		)
		if err != nil {
			return fmt.Errorf("checkpoint_store: update status from: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("checkpoint_store: update status from %s to %s: no checkpoint %s (status %s)", from, to, executionID, from)
		}
		return nil
	})
}

// AdvanceRunGeneration atomically increments the resume-generation fence only
// when it still equals expect. A non-1 RowsAffected means a concurrent resume
// already won the race (double-tab/double-device protection).
func (s *PgCheckpointStore) AdvanceRunGeneration(ctx context.Context, tenantID, executionID string, expect int) error {
	return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE agent_execution_checkpoints
			    SET run_generation = run_generation + 1, updated_at = NOW()
			  WHERE execution_id = $1 AND run_generation = $2`,
			executionID, expect,
		)
		if err != nil {
			return fmt.Errorf("checkpoint_store: advance run generation: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("checkpoint_store: advance run generation: stale generation %d for execution %s", expect, executionID)
		}
		return nil
	})
}

// Terminate moves a checkpoint to a terminal status and refreshes expires_at
// with the terminal retention window so DeleteExpired reclaims it.
func (s *PgCheckpointStore) Terminate(ctx context.Context, tenantID, executionID, status string) error {
	return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE agent_execution_checkpoints
			    SET status = $1, updated_at = NOW(),
			        expires_at = NOW() + $2::interval
			  WHERE execution_id = $3 AND status IN ('running', 'paused', 'waiting_approval')`,
			status, constants.CheckpointTerminalTTL, executionID,
		)
		if err != nil {
			return fmt.Errorf("checkpoint_store: terminate: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("checkpoint_store: terminate: no active checkpoint for execution %s", executionID)
		}
		return nil
	})
}
