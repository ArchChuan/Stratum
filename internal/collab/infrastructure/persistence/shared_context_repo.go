package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/collab/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgSharedContextRepo provides optimistic-lock access to plan-wide state.
type PgSharedContextRepo struct{ pool poolIface }

// NewPgSharedContextRepo constructs a Postgres-backed SharedContextRepo.
func NewPgSharedContextRepo(pool *pgxpool.Pool) *PgSharedContextRepo {
	return &PgSharedContextRepo{pool: pool}
}

// Get loads the shared context; (nil, nil) when the plan has no row yet
// (the first successful step upserts it).
func (r *PgSharedContextRepo) Get(ctx context.Context, tenantID, planID string) (*domain.SharedContext, error) {
	var sc domain.SharedContext
	var data []byte
	err := exec(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT plan_id, data, version FROM shared_contexts WHERE plan_id=$1`, planID).
			Scan(&sc.PlanID, &data, &sc.Version)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("collab store get shared context: %w", err)
	}
	sc.Data = json.RawMessage(data)
	return &sc, nil
}

// Update is an upsert guarded by the optimistic version: UPDATE ... WHERE
// plan_id AND version = sc.Version bumps version; when no row matches (first
// writer, or a concurrent first-insert lost the race) an INSERT with
// ON CONFLICT DO NOTHING is attempted — zero rows means a conflicting writer
// already landed, which surfaces as ErrCollabConflict for a bounded retry.
func (r *PgSharedContextRepo) Update(ctx context.Context, tenantID string, sc domain.SharedContext) error {
	data := string(sc.Data)
	err := exec(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE shared_contexts SET data=$1::jsonb, version=version+1
			 WHERE plan_id=$2 AND version=$3`, data, sc.PlanID, sc.Version)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			return nil
		}
		tag, err = tx.Exec(ctx,
			`INSERT INTO shared_contexts (plan_id, data, version) VALUES ($1, $2::jsonb, 0)
			 ON CONFLICT (plan_id) DO NOTHING`, sc.PlanID, data)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrCollabConflict
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("collab store update shared context: %w", err)
	}
	return nil
}
