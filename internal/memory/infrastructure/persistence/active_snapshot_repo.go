package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

type ActiveSnapshotRepo struct {
	pool tenantPool
}

func NewActiveSnapshotRepo(pool *pgxpool.Pool) *ActiveSnapshotRepo {
	return &ActiveSnapshotRepo{pool: pool}
}

func (r *ActiveSnapshotRepo) execTenant(ctx context.Context, tenantID string, fn func(context.Context, pgx.Tx) error) error {
	return (&MemoryRepo{pool: r.pool}).execTenant(ctx, tenantID, fn)
}

func (r *ActiveSnapshotRepo) Upsert(ctx context.Context, snapshot *domain.ActiveSnapshot) error {
	if err := snapshot.Validate(); err != nil {
		return err
	}
	source, err := json.Marshal(snapshot.Source)
	if err != nil {
		return fmt.Errorf("memory: marshal active snapshot source: %w", err)
	}
	return r.execTenant(ctx, snapshot.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO memory_active_snapshots
			(user_id, agent_id, work_context, personal_context, top_of_mind, source, expires_at, updated_at, status)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (user_id, agent_id) DO UPDATE SET
				work_context = EXCLUDED.work_context,
				personal_context = EXCLUDED.personal_context,
				top_of_mind = EXCLUDED.top_of_mind,
				source = EXCLUDED.source,
				expires_at = EXCLUDED.expires_at,
				updated_at = EXCLUDED.updated_at,
				version = memory_active_snapshots.version + 1,
				status = EXCLUDED.status
			WHERE EXCLUDED.updated_at > memory_active_snapshots.updated_at`,
			snapshot.UserID, snapshot.AgentID, snapshot.WorkContext, snapshot.PersonalContext,
			snapshot.TopOfMind, source, snapshot.ExpiresAt, snapshot.UpdatedAt, snapshot.Status)
		return err
	})
}

func (r *ActiveSnapshotRepo) Get(ctx context.Context, tenantID, userID, agentID string) (*domain.ActiveSnapshot, error) {
	if r.pool == nil {
		return nil, nil
	}
	var snapshot *domain.ActiveSnapshot
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var source []byte
		s := &domain.ActiveSnapshot{TenantID: tenantID, UserID: userID, AgentID: agentID}
		err := tx.QueryRow(ctx, `SELECT work_context, personal_context, top_of_mind, source, expires_at, updated_at, version, status
			FROM memory_active_snapshots
			WHERE user_id = $1 AND agent_id = $2 AND status = 'active' AND expires_at > NOW()`, userID, agentID).
			Scan(&s.WorkContext, &s.PersonalContext, &s.TopOfMind, &source, &s.ExpiresAt, &s.UpdatedAt, &s.Version, &s.Status)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := json.Unmarshal(source, &s.Source); err != nil {
			return fmt.Errorf("memory: decode active snapshot source: %w", err)
		}
		snapshot = s
		return nil
	})
	return snapshot, err
}

func (r *ActiveSnapshotRepo) Delete(ctx context.Context, tenantID, userID, agentID string) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM memory_active_snapshots WHERE user_id = $1 AND agent_id = $2`, userID, agentID)
		return err
	})
}
