package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/collab/domain"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgTaskStepRepo persists and claims task steps with lease-based locking.
type PgTaskStepRepo struct{ pool poolIface }

// NewPgTaskStepRepo constructs a Postgres-backed TaskStepRepo.
func NewPgTaskStepRepo(pool *pgxpool.Pool) *PgTaskStepRepo {
	return &PgTaskStepRepo{pool: pool}
}

const stepSelectColumns = `SELECT id, plan_id, agent_id, dependencies, status, input, output,
	delegation, claimed_by, lease_expires_at, retry_count, max_retries, generation, error,
	created_at, updated_at FROM task_steps`

// InsertBatch inserts steps in one tenant transaction.
func (r *PgTaskStepRepo) InsertBatch(ctx context.Context, tenantID string, steps []domain.TaskStep) error {
	err := exec(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		for _, step := range steps {
			deps, err := json.Marshal(step.Dependencies)
			if err != nil {
				return err
			}
			input, err := json.Marshal(step.Input)
			if err != nil {
				return err
			}
			output, err := json.Marshal(step.Output)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO task_steps (id, plan_id, agent_id, dependencies, status, input, output,
				 delegation, max_retries, created_at, updated_at)
				 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
				step.ID, step.PlanID, step.AgentID, string(deps), step.Status, string(input),
				string(output), step.Delegation, step.MaxRetries, step.CreatedAt, step.UpdatedAt); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("collab store insert steps: %w", err)
	}
	return nil
}

// getStep loads one step inside the caller's tenant context.
func (r *PgTaskStepRepo) getStep(ctx context.Context, tenantID, stepID string) (*domain.TaskStep, error) {
	var s domain.TaskStep
	var deps, input, output []byte
	err := exec(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, stepSelectColumns+` WHERE id=$1`, stepID).Scan(
			&s.ID, &s.PlanID, &s.AgentID, &deps, &s.Status, &input, &output, &s.Delegation,
			&s.ClaimedBy, &s.LeaseExpiresAt, &s.RetryCount, &s.MaxRetries, &s.Generation,
			&s.Error, &s.CreatedAt, &s.UpdatedAt)
	})
	if err != nil {
		return nil, fmt.Errorf("collab store get step: %w", err)
	}
	if err := json.Unmarshal(deps, &s.Dependencies); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(input, &s.Input); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(output, &s.Output); err != nil {
		return nil, err
	}
	return &s, nil
}

// ClaimNextTask claims one ready step across all tenants: public.tenants
// scan, then per-tenant advisory lock and lease reclamation (mirrors the
// workflow ClaimRun). A step is claimable when pending, or claimed/running
// with an expired lease, and its plan is still created|running. The claim
// bumps generation so a stale finalize is rejected by UpdateStatus.
func (r *PgTaskStepRepo) ClaimNextTask(ctx context.Context, owner string, lease time.Duration) (string, *domain.TaskStep, bool, error) {
	tenantIDs, err := r.listActiveTenants(ctx)
	if err != nil {
		return "", nil, false, err
	}
	for _, tenantID := range tenantIDs {
		tenantCtx := pgstore.WithTenant(ctx, &pgstore.TenantContext{TenantID: tenantID})
		stepID, err := r.claimInTenant(tenantCtx, tenantID, owner, lease)
		if err != nil {
			return "", nil, false, fmt.Errorf("collab store claim: %w", err)
		}
		if stepID == "" {
			continue
		}
		step, err := r.getStep(tenantCtx, tenantID, stepID)
		return tenantID, step, true, err
	}
	return "", nil, false, nil
}

// listActiveTenants loads the non-deleted active tenant IDs from public.tenants.
func (r *PgTaskStepRepo) listActiveTenants(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT id::text FROM public.tenants WHERE status='active' AND deleted_at IS NULL ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("collab store claim: list tenants: %w", err)
	}
	defer rows.Close()
	var tenantIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("collab store claim: scan tenant: %w", err)
		}
		tenantIDs = append(tenantIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("collab store claim: list tenants: %w", err)
	}
	return tenantIDs, nil
}

// claimInTenant attempts one claim inside a single tenant's schema. An empty
// stepID means nothing was claimable there; provisional-schema errors
// (42P01/3F000) and no-rows collapse to the same "no claim" outcome so the
// worker moves on to the next tenant.
func (r *PgTaskStepRepo) claimInTenant(ctx context.Context, tenantID, owner string, lease time.Duration) (string, error) {
	var stepID string
	err := pgstore.ExecTenantWith(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, lockErr := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext(current_schema()))`); lockErr != nil {
			return lockErr
		}
		return tx.QueryRow(ctx,
			`WITH candidate AS (
				SELECT id FROM task_steps
				WHERE (status='pending' OR (status IN ('claimed','running') AND lease_expires_at < NOW()))
				  AND EXISTS (SELECT 1 FROM collaborations c
				              WHERE c.id = task_steps.plan_id AND c.status IN ('created','running'))
				ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1
			) UPDATE task_steps s SET status='claimed', claimed_by=$1, lease_expires_at=NOW()+$2::interval,
				generation=s.generation+1, updated_at=NOW()
			FROM candidate WHERE s.id=candidate.id RETURNING s.id::text`,
			owner, lease.String()).Scan(&stepID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "42P01" || pgErr.Code == "3F000") {
		// tenant schema not provisioned yet (or mid-provisioning): skip
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return stepID, nil
}

// RenewLease extends the lease of a step still owned by owner with a live
// lease; a stale owner (re-claimed by another worker) is rejected with
// ErrCollabConflict, which cancels the executing step.
func (r *PgTaskStepRepo) RenewLease(ctx context.Context, tenantID, stepID, owner string, lease time.Duration) error {
	err := exec(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE task_steps SET lease_expires_at=NOW()+$1::interval, updated_at=NOW()
			 WHERE id=$2 AND claimed_by=$3 AND lease_expires_at > NOW()`,
			lease.String(), stepID, owner)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrCollabConflict
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("collab store renew lease: %w", err)
	}
	return nil
}

// UpdateStatus writes a terminal/release state guarded by the claim
// generation: a stale writer (step re-claimed by another worker) is
// rejected with ErrCollabConflict. A release write back to pending bumps
// retry_count; terminal writes leave it intact.
func (r *PgTaskStepRepo) UpdateStatus(ctx context.Context, tenantID, stepID string, expectedGeneration int64, status domain.TaskStatus, output map[string]any, errMsg string) error {
	rawOutput := ""
	if output != nil {
		b, err := json.Marshal(output)
		if err != nil {
			return fmt.Errorf("collab store update step: marshal output: %w", err)
		}
		rawOutput = string(b)
	}
	err := exec(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE task_steps SET status=$1,
			 output=CASE WHEN $2::text <> '' THEN $2::jsonb ELSE output END,
			 error=$3,
			 retry_count=CASE WHEN $1='pending' THEN retry_count+1 ELSE retry_count END,
			 updated_at=NOW()
			 WHERE id=$4 AND generation=$5`,
			status, rawOutput, errMsg, stepID, expectedGeneration)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrCollabConflict
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("collab store update step: %w", err)
	}
	return nil
}

// GetReadyTasks lists pending steps whose dependencies are all completed.
func (r *PgTaskStepRepo) GetReadyTasks(ctx context.Context, tenantID, planID string) ([]domain.TaskStep, error) {
	var out []domain.TaskStep
	err := exec(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, stepSelectColumns+
			` WHERE plan_id=$1 AND status='pending'
			   AND NOT EXISTS (
			       SELECT 1 FROM task_steps dep
			       WHERE dep.id IN (SELECT jsonb_array_elements_text(s.dependencies))
			         AND dep.status <> 'completed'
			   )
			   ORDER BY created_at, id`, planID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s domain.TaskStep
			var deps, input, output []byte
			if err := rows.Scan(&s.ID, &s.PlanID, &s.AgentID, &deps, &s.Status, &input, &output,
				&s.Delegation, &s.ClaimedBy, &s.LeaseExpiresAt, &s.RetryCount, &s.MaxRetries,
				&s.Generation, &s.Error, &s.CreatedAt, &s.UpdatedAt); err != nil {
				return err
			}
			if err := json.Unmarshal(deps, &s.Dependencies); err != nil {
				return err
			}
			if err := json.Unmarshal(input, &s.Input); err != nil {
				return err
			}
			if err := json.Unmarshal(output, &s.Output); err != nil {
				return err
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("collab store ready tasks: %w", err)
	}
	return out, nil
}

// CancelPending marks pending steps of a plan canceled; the worker refuses
// to claim them (they are also excluded by the plan-status guard).
func (r *PgTaskStepRepo) CancelPending(ctx context.Context, tenantID, planID string) error {
	err := exec(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE task_steps SET status='canceled', updated_at=NOW() WHERE plan_id=$1 AND status='pending'`,
			planID)
		return err
	})
	if err != nil {
		return fmt.Errorf("collab store cancel pending: %w", err)
	}
	return nil
}

// CountByStatus tallies step statuses for plan completion judgment.
func (r *PgTaskStepRepo) CountByStatus(ctx context.Context, tenantID, planID string) (map[domain.TaskStatus]int, error) {
	counts := map[domain.TaskStatus]int{}
	err := exec(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT status, COUNT(*) FROM task_steps WHERE plan_id=$1 GROUP BY status`, planID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var status domain.TaskStatus
			var n int
			if err := rows.Scan(&status, &n); err != nil {
				return err
			}
			counts[status] = n
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("collab store count steps: %w", err)
	}
	return counts, nil
}
