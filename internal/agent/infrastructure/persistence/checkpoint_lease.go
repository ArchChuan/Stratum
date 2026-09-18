package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/jackc/pgx/v5"
)

// 租约方法挂在 PgCheckpointStore 上而不是独立 repo：租约列与 checkpoint 同行，
// 拆表会让「抢占 generation」与「占用租约」失去同事务的原子性。端口侧仍是独立的
// ExecutionLeaseRepo，消费方不感知这次复用（spec D2）。
var _ port.ExecutionLeaseRepo = (*PgCheckpointStore)(nil)

// StampLease 为新建执行盖上租约并返回当前 generation。不推进 generation：
// NEW 路径的 Ensure/Upsert 已把 run_generation 初始化为 1。
func (s *PgCheckpointStore) StampLease(
	ctx context.Context, tenantID, executionID string, lease time.Duration,
) (int, error) {
	var generation int
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`UPDATE agent_execution_checkpoints
			    SET lease_expires_at = NOW() + $2::interval, updated_at = NOW()
			  WHERE execution_id = $1
			  RETURNING run_generation`,
			executionID, lease.String(),
		).Scan(&generation)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("checkpoint_store: stamp lease: no checkpoint %s", executionID)
	}
	if err != nil {
		return 0, fmt.Errorf("checkpoint_store: stamp lease: %w", err)
	}
	return generation, nil
}

// ClaimLease 在 run_generation == expect 时抢占。这是互斥的唯一来源：
// 并发的两个 claimant 传同一个 expect，恰好一个 RowsAffected == 1。
// 终态 checkpoint 不可抢占——恢复一个已完成/已失败的执行没有语义。
func (s *PgCheckpointStore) ClaimLease(
	ctx context.Context, tenantID, executionID string, expect int, lease time.Duration,
) (int, error) {
	var generation int
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`UPDATE agent_execution_checkpoints
			    SET lease_expires_at = NOW() + $3::interval,
			        run_generation = run_generation + 1,
			        updated_at = NOW()
			  WHERE execution_id = $1
			    AND run_generation = $2
			    AND status NOT IN ('completed', 'failed', 'expired')
			  RETURNING run_generation`,
			executionID, expect, lease.String(),
		).Scan(&generation)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, port.ErrLeaseConflict
	}
	if err != nil {
		return 0, fmt.Errorf("checkpoint_store: claim lease: %w", err)
	}
	return generation, nil
}

// RenewLease 续租。generation 不匹配即被抢——僵尸 runner 收到 ErrLeaseConflict
// 后必须立刻自取消，否则会继续烧 token（spec D3）。
func (s *PgCheckpointStore) RenewLease(
	ctx context.Context, tenantID, executionID string, generation int, lease time.Duration,
) error {
	var affected int64
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE agent_execution_checkpoints
			    SET lease_expires_at = NOW() + $3::interval, updated_at = NOW()
			  WHERE execution_id = $1 AND run_generation = $2`,
			executionID, generation, lease.String(),
		)
		if err != nil {
			return fmt.Errorf("checkpoint_store: renew lease: %w", err)
		}
		affected = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return err
	}
	if affected != 1 {
		return port.ErrLeaseConflict
	}
	return nil
}

// ReleaseLease 释放租约。generation 不匹配说明自己已被抢，无需清理。
// 用 generation 作为 guard 而非无条件清空：否则僵尸 runner 退出时会把
// 新 runner 刚占上的租约抹掉。
func (s *PgCheckpointStore) ReleaseLease(
	ctx context.Context, tenantID, executionID string, generation int,
) error {
	var affected int64
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE agent_execution_checkpoints
			    SET lease_expires_at = NULL, updated_at = NOW()
			  WHERE execution_id = $1 AND run_generation = $2`,
			executionID, generation,
		)
		if err != nil {
			return fmt.Errorf("checkpoint_store: release lease: %w", err)
		}
		affected = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return err
	}
	if affected != 1 {
		return port.ErrLeaseConflict
	}
	return nil
}

// LeaseStatus 读取 generation 与租约是否有效。缺行返回错误（fail closed）：
// 调用方在此之前已用 GetLatest 做过租户限定的存在性判断，走到这里还没行
// 说明状态已变，不应静默降级成 generation=0。
func (s *PgCheckpointStore) LeaseStatus(
	ctx context.Context, tenantID, executionID string,
) (port.LeaseStatus, error) {
	var status port.LeaseStatus
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT run_generation, (lease_expires_at IS NOT NULL AND lease_expires_at > NOW())
			   FROM agent_execution_checkpoints
			  WHERE execution_id = $1`,
			executionID,
		).Scan(&status.Generation, &status.Active)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return port.LeaseStatus{}, fmt.Errorf("checkpoint_store: lease status: no checkpoint %s", executionID)
	}
	if err != nil {
		return port.LeaseStatus{}, fmt.Errorf("checkpoint_store: lease status: %w", err)
	}
	return status, nil
}
