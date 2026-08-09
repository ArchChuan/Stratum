// Package persistence — Postgres adapter for the scheduled-task bounded
// context. All methods are tenant-scoped: exec injects the tenant
// search_path for the transaction (multi-tenant schema isolation), mirroring
// the collab and workflow stores.
package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/internal/scheduler/domain"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

// poolIface allows pgxmock injection in tests.
type poolIface interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

var _ poolIface = (*pgxpool.Pool)(nil)

// exec runs fn inside a tenant-schema transaction. The caller-supplied
// tenantID wins over a mismatching context (fail closed rather than
// silently crossing schemas).
func exec(ctx context.Context, pool poolIface, tenantID string, fn func(context.Context, pgx.Tx) error) error {
	tc, ok := pgstore.FromContext(ctx)
	if ok && tc.TenantID != tenantID {
		return fmt.Errorf("scheduler store: tenant context mismatch")
	}
	if !ok {
		ctx = pgstore.WithTenant(ctx, &pgstore.TenantContext{TenantID: tenantID})
	}
	return pgstore.ExecTenantWith(ctx, pool, tenantID, fn)
}

// PgScheduledTaskRepo persists scheduled tasks.
type PgScheduledTaskRepo struct{ pool poolIface }

// NewPgScheduledTaskRepo constructs a Postgres-backed scheduler repository.
func NewPgScheduledTaskRepo(pool *pgxpool.Pool) *PgScheduledTaskRepo {
	return &PgScheduledTaskRepo{pool: pool}
}

const taskColumns = `id, name, workflow_id, version_id, input_template, cron_expr,
	enabled, next_fire_at, last_run_at, last_run_status, last_error_message,
	created_by, created_at, updated_at`

// Insert creates a scheduled task row.
func (r *PgScheduledTaskRepo) Insert(ctx context.Context, tenantID string, task *domain.ScheduledTask) error {
	template, err := json.Marshal(task.InputTemplate)
	if err != nil {
		return fmt.Errorf("scheduler store insert: marshal input template: %w", err)
	}
	err = exec(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO scheduled_tasks (id, name, workflow_id, version_id, input_template,
				cron_expr, enabled, next_fire_at, last_run_status, created_by, created_at, updated_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			task.ID, task.Name, task.WorkflowID, task.VersionID, string(template),
			task.CronExpr, task.Enabled, task.NextFireAt, task.LastRunStatus,
			task.CreatedBy, task.CreatedAt, task.UpdatedAt)
		return err
	})
	if err != nil {
		return fmt.Errorf("scheduler store insert: %w", err)
	}
	return nil
}

// GetByID loads one task; ErrScheduledTaskNotFound when absent.
func (r *PgScheduledTaskRepo) GetByID(ctx context.Context, tenantID, id string) (*domain.ScheduledTask, error) {
	task, err := scanTask(ctx, r, tenantID, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrScheduledTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	return task, nil
}

// List returns tasks newest-first (created_at DESC, id DESC tiebreak) with
// the total count for pagination.
func (r *PgScheduledTaskRepo) List(ctx context.Context, tenantID string, limit, offset int) ([]domain.ScheduledTask, int, error) {
	var out []domain.ScheduledTask
	var total int
	err := exec(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM scheduled_tasks`).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx,
			`SELECT `+taskColumns+` FROM scheduled_tasks
			 ORDER BY created_at DESC, id DESC LIMIT $1 OFFSET $2`, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			task, err := scanTaskRow(rows)
			if err != nil {
				return err
			}
			out = append(out, *task)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, 0, fmt.Errorf("scheduler store list: %w", err)
	}
	return out, total, nil
}

// Update replaces every editable field and re-enables the task (an update
// restarts the schedule from the freshly computed next fire time).
func (r *PgScheduledTaskRepo) Update(ctx context.Context, tenantID string, task *domain.ScheduledTask) error {
	template, err := json.Marshal(task.InputTemplate)
	if err != nil {
		return fmt.Errorf("scheduler store update: marshal input template: %w", err)
	}
	err = exec(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE scheduled_tasks SET name=$2, workflow_id=$3, version_id=$4,
				input_template=$5, cron_expr=$6, enabled=$7, next_fire_at=$8,
				updated_at=NOW()
			 WHERE id=$1`,
			task.ID, task.Name, task.WorkflowID, task.VersionID, string(template),
			task.CronExpr, task.Enabled, task.NextFireAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrScheduledTaskNotFound
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scheduler store update: %w", err)
	}
	return nil
}

// Delete removes a scheduled task row.
func (r *PgScheduledTaskRepo) Delete(ctx context.Context, tenantID, id string) error {
	err := exec(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM scheduled_tasks WHERE id=$1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrScheduledTaskNotFound
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scheduler store delete: %w", err)
	}
	return nil
}

// SetEnabled flips the enabled flag; nextFireAt is required on re-enable
// (recomputed by the service) and nil on disable (the old value is kept but
// the enabled predicate removes the row from the due set).
func (r *PgScheduledTaskRepo) SetEnabled(ctx context.Context, tenantID, id string, enabled bool, nextFireAt *time.Time) error {
	err := exec(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE scheduled_tasks SET enabled=$2, next_fire_at=COALESCE($3, next_fire_at),
				updated_at=NOW()
			 WHERE id=$1`,
			id, enabled, nextFireAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrScheduledTaskNotFound
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scheduler store set enabled: %w", err)
	}
	return nil
}

// ListDue returns enabled tasks with next_fire_at <= now, oldest first,
// bounded by limit.
func (r *PgScheduledTaskRepo) ListDue(ctx context.Context, tenantID string, now time.Time, limit int) ([]domain.ScheduledTask, error) {
	var out []domain.ScheduledTask
	err := exec(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+taskColumns+` FROM scheduled_tasks
			 WHERE enabled AND next_fire_at <= $1 ORDER BY next_fire_at LIMIT $2`, now, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			task, err := scanTaskRow(rows)
			if err != nil {
				return err
			}
			out = append(out, *task)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("scheduler store list due: %w", err)
	}
	return out, nil
}

// RecordFire advances next_fire_at guarded on the row's current value:
// WHERE next_fire_at = oldNext. RowsAffected == 0 means a concurrent worker
// already advanced this fire — the loser returns (false, nil) and skips.
// Real storage errors are wrapped and propagated.
func (r *PgScheduledTaskRepo) RecordFire(ctx context.Context, tenantID, id string, firedAt time.Time, status, errorMsg string, oldNext, newNext time.Time) (bool, error) {
	var advanced bool
	err := exec(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE scheduled_tasks SET next_fire_at=$3, last_run_at=$4,
				last_run_status=$5, last_error_message=$6, updated_at=NOW()
			 WHERE id=$1 AND next_fire_at=$2`,
			id, oldNext, newNext, firedAt, status, errorMsg)
		if err != nil {
			return err
		}
		advanced = tag.RowsAffected() > 0
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("scheduler store record fire: %w", err)
	}
	return advanced, nil
}

// scanTask loads one task by id inside the tenant transaction.
func scanTask(ctx context.Context, r *PgScheduledTaskRepo, tenantID, id string) (*domain.ScheduledTask, error) {
	var task domain.ScheduledTask
	err := exec(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT `+taskColumns+` FROM scheduled_tasks WHERE id=$1`, id).Scan(
			&task.ID, &task.Name, &task.WorkflowID, &task.VersionID, &task.InputTemplate,
			&task.CronExpr, &task.Enabled, &task.NextFireAt, &task.LastRunAt,
			&task.LastRunStatus, &task.LastErrorMessage, &task.CreatedBy,
			&task.CreatedAt, &task.UpdatedAt)
	})
	if err != nil {
		return nil, fmt.Errorf("scheduler store get: %w", err)
	}
	return &task, nil
}

// scanTaskRow scans one row with the taskColumns projection.
func scanTaskRow(row pgx.Row) (*domain.ScheduledTask, error) {
	var task domain.ScheduledTask
	if err := row.Scan(&task.ID, &task.Name, &task.WorkflowID, &task.VersionID, &task.InputTemplate,
		&task.CronExpr, &task.Enabled, &task.NextFireAt, &task.LastRunAt,
		&task.LastRunStatus, &task.LastErrorMessage, &task.CreatedBy,
		&task.CreatedAt, &task.UpdatedAt); err != nil {
		return nil, err
	}
	return &task, nil
}
