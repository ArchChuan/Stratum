package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/jackc/pgx/v5"
)

// PgTaskRepo persists agent_tasks with lease-based claim and generation fence,
// mirroring the workflow_runs / task_steps concurrency pattern.
type PgTaskRepo struct {
	pool chatPoolIface
}

// NewPgTaskRepo constructs a Postgres-backed TaskRepo.
func NewPgTaskRepo(pool chatPoolIface) *PgTaskRepo {
	return &PgTaskRepo{pool: pool}
}

const taskSelectColumns = `id, agent_id, user_id, goal, current_phase, completed_steps,
 next_action, status, claimed_by, lease_expires_at, generation,
 COALESCE(last_conversation_id::text, ''), last_execution_id, fail_count,
 created_at, updated_at, expires_at`

func scanTask(row pgx.Row) (*domain.Task, error) {
	var t domain.Task
	var completedSteps []byte
	var leaseExpiresAt *time.Time
	err := row.Scan(&t.ID, &t.AgentID, &t.UserID, &t.Goal, &t.CurrentPhase, &completedSteps,
		&t.NextAction, &t.Status, &t.ClaimedBy, &leaseExpiresAt, &t.Generation,
		&t.LastConversationID, &t.LastExecutionID, &t.FailCount,
		&t.CreatedAt, &t.UpdatedAt, &t.ExpiresAt)
	if err != nil {
		return nil, err
	}
	if leaseExpiresAt != nil {
		t.LeaseExpiresAt = *leaseExpiresAt
	}
	if err := json.Unmarshal(completedSteps, &t.CompletedSteps); err != nil {
		return nil, fmt.Errorf("task_store: decode completed_steps: %w", err)
	}
	return &t, nil
}

// Claim 原子抢占/续约并 bump generation 作 fence。条件：status=active、
// 未过期、且（本会话 / 无主 / lease 过期）。返回 claim 后 task（含新
// generation）与是否成功；无行或不可 claim → (nil, false, nil)。
func (r *PgTaskRepo) Claim(
	ctx context.Context, tenantID, taskID, conversationID string, lease time.Duration,
) (*domain.Task, bool, error) {
	var task *domain.Task
	err := execTenantID(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`UPDATE agent_tasks
			    SET claimed_by = $2,
			        lease_expires_at = NOW() + $3::interval,
			        generation = generation + 1,
			        updated_at = NOW()
			  WHERE id = $1
			    AND status = 'active' AND expires_at > NOW()
			    AND (claimed_by = $2 OR claimed_by = '' OR lease_expires_at < NOW())
			  RETURNING `+taskSelectColumns,
			taskID, conversationID, lease.String())
		claimed, err := scanTask(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		task = claimed
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("task_store: claim: %w", err)
	}
	return task, task != nil, nil
}

// Save 新建或乐观锁写回。新行 INSERT（generation=task.Generation）；已存在行
// 仅当 generation==expectedGeneration 时更新，冲突返回 ErrGenerationConflict。
func (r *PgTaskRepo) Save(ctx context.Context, tenantID string, task domain.Task, expectedGeneration int64) error {
	steps, err := json.Marshal(task.CompletedSteps)
	if err != nil {
		return fmt.Errorf("task_store: encode completed_steps: %w", err)
	}
	var conversationID any
	if task.LastConversationID == "" {
		conversationID = nil
	} else {
		conversationID = task.LastConversationID
	}
	err = execTenantID(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`INSERT INTO agent_tasks
			    (id, agent_id, user_id, goal, current_phase, completed_steps, next_action,
			     status, claimed_by, lease_expires_at, generation, last_conversation_id,
			     last_execution_id, fail_count, created_at, updated_at, expires_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
			 ON CONFLICT (id) DO UPDATE SET
			    goal = EXCLUDED.goal,
			    current_phase = EXCLUDED.current_phase,
			    completed_steps = EXCLUDED.completed_steps,
			    next_action = EXCLUDED.next_action,
			    status = EXCLUDED.status,
			    claimed_by = EXCLUDED.claimed_by,
			    lease_expires_at = EXCLUDED.lease_expires_at,
			    last_conversation_id = EXCLUDED.last_conversation_id,
			    last_execution_id = EXCLUDED.last_execution_id,
			    fail_count = EXCLUDED.fail_count,
			    updated_at = NOW(),
			    expires_at = EXCLUDED.expires_at
			 WHERE agent_tasks.generation = $18`,
			task.ID, task.AgentID, task.UserID, task.Goal, task.CurrentPhase, string(steps),
			task.NextAction, string(task.Status), task.ClaimedBy, task.LeaseExpiresAt,
			task.Generation, conversationID, task.LastExecutionID, task.FailCount,
			task.CreatedAt, task.UpdatedAt, task.ExpiresAt, expectedGeneration)
		if err != nil {
			return fmt.Errorf("task_store: save: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrGenerationConflict
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// Get 加载单个 task；不存在返回 (nil, nil)。
func (r *PgTaskRepo) Get(ctx context.Context, tenantID, taskID string) (*domain.Task, error) {
	var task *domain.Task
	err := execTenantID(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT `+taskSelectColumns+` FROM agent_tasks WHERE id = $1`, taskID)
		loaded, err := scanTask(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		task = loaded
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("task_store: get: %w", err)
	}
	return task, nil
}

// GetLatestActiveForOwner 返回该 owner 最新的活跃 task（updated_at DESC），
// 无则 (nil, nil)。恢复入口。
func (r *PgTaskRepo) GetLatestActiveForOwner(
	ctx context.Context, tenantID, agentID, userID string,
) (*domain.Task, error) {
	var task *domain.Task
	err := execTenantID(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+taskSelectColumns+`
			   FROM agent_tasks
			  WHERE agent_id = $1 AND user_id = $2 AND status = 'active'
			  ORDER BY updated_at DESC
			  LIMIT 1`, agentID, userID)
		loaded, err := scanTask(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		task = loaded
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("task_store: get latest active: %w", err)
	}
	return task, nil
}

// DetachConversation 解除某会话的 task 引用：claimed_by 清空、lease 置空，
// task 本身保留 active。空 conversationID 必须拒绝（fail closed），否则
// last_conversation_id IS NULL 会误伤未关联会话的 task。
func (r *PgTaskRepo) DetachConversation(ctx context.Context, tenantID, conversationID string) error {
	if conversationID == "" {
		return domain.ErrTaskConversationGone
	}
	err := execTenantID(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE agent_tasks
			    SET claimed_by = '', lease_expires_at = NULL, updated_at = NOW()
			  WHERE last_conversation_id = $1::uuid`, conversationID)
		if err != nil {
			return fmt.Errorf("task_store: detach conversation: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// DeleteExpired 回收 expires_at 已过的 task，返回删除行数。
func (r *PgTaskRepo) DeleteExpired(ctx context.Context, tenantID string) (int64, error) {
	var deleted int64
	err := execTenantID(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM agent_tasks WHERE expires_at < NOW()`)
		if err != nil {
			return fmt.Errorf("task_store: delete expired: %w", err)
		}
		deleted = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}
