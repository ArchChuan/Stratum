// Package persistence — Postgres adapter for the collab bounded context.
// All methods are tenant-scoped: exec injects the tenant search_path for the
// transaction (multi-tenant schema isolation), mirroring the workflow store.
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
		return fmt.Errorf("collab store: tenant context mismatch")
	}
	if !ok {
		ctx = pgstore.WithTenant(ctx, &pgstore.TenantContext{TenantID: tenantID})
	}
	return pgstore.ExecTenantWith(ctx, pool, tenantID, fn)
}

// PgCollaborationRepo persists collaboration plans.
type PgCollaborationRepo struct{ pool poolIface }

// NewPgCollaborationRepo constructs a Postgres-backed CollaborationRepo.
func NewPgCollaborationRepo(pool *pgxpool.Pool) *PgCollaborationRepo {
	return &PgCollaborationRepo{pool: pool}
}

// Insert creates a collaboration plan row.
func (r *PgCollaborationRepo) Insert(ctx context.Context, c domain.Collaboration) error {
	participants, err := json.Marshal(c.Participants)
	if err != nil {
		return fmt.Errorf("collab store insert: marshal participants: %w", err)
	}
	err = exec(ctx, r.pool, c.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO collaborations (id, task_description, strategy, status, created_by, participants, created_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			c.ID, c.TaskDescription, c.Strategy, c.Status, c.CreatedBy, string(participants), c.CreatedAt)
		return err
	})
	if err != nil {
		return fmt.Errorf("collab store insert: %w", err)
	}
	return nil
}

// GetByID loads one plan; ErrCollabNotFound when absent.
func (r *PgCollaborationRepo) GetByID(ctx context.Context, tenantID, id string) (*domain.Collaboration, error) {
	var c domain.Collaboration
	var participants []byte
	err := exec(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id, task_description, strategy, status, created_by, participants, created_at, started_at, completed_at
			 FROM collaborations WHERE id=$1`, id).
			Scan(&c.ID, &c.TaskDescription, &c.Strategy, &c.Status, &c.CreatedBy,
				&participants, &c.CreatedAt, &c.StartedAt, &c.CompletedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrCollabNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("collab store get: %w", err)
	}
	if err := json.Unmarshal(participants, &c.Participants); err != nil {
		return nil, fmt.Errorf("collab store get: unmarshal participants: %w", err)
	}
	c.TenantID = tenantID
	return &c, nil
}

// ListByTenant returns plans newest-first with pagination.
func (r *PgCollaborationRepo) ListByTenant(ctx context.Context, tenantID string, limit, offset int) ([]domain.Collaboration, error) {
	var out []domain.Collaboration
	err := exec(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, task_description, strategy, status, created_by, participants, created_at, started_at, completed_at
			 FROM collaborations ORDER BY created_at DESC, id DESC LIMIT $1 OFFSET $2`, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c domain.Collaboration
			var participants []byte
			if err := rows.Scan(&c.ID, &c.TaskDescription, &c.Strategy, &c.Status, &c.CreatedBy,
				&participants, &c.CreatedAt, &c.StartedAt, &c.CompletedAt); err != nil {
				return err
			}
			if err := json.Unmarshal(participants, &c.Participants); err != nil {
				return err
			}
			c.TenantID = tenantID
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("collab store list: %w", err)
	}
	return out, nil
}

// UpdateStatus advances plan status guarded by WHERE status IN ('created',
// 'running'): a terminal plan is never rewritten and a worker's stale
// completion on a canceled plan is a tolerated no-op. startedAt/completedAt
// are COALESCEd so only the migrating transition writes them.
func (r *PgCollaborationRepo) UpdateStatus(ctx context.Context, tenantID, id string, status domain.CollabStatus, startedAt, completedAt *time.Time) error {
	err := exec(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE collaborations SET status=$1, started_at=COALESCE($2, started_at),
			 completed_at=COALESCE($3, completed_at)
			 WHERE id=$4 AND status IN ('created','running')`,
			status, startedAt, completedAt, id)
		return err
	})
	if err != nil {
		return fmt.Errorf("collab store update status: %w", err)
	}
	return nil
}
