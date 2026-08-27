package persistence

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EntityRepo implements domain/port.EntityRepo using PostgreSQL with tenant isolation.
type EntityRepo struct {
	pool tenantPool
}

// NewEntityRepo creates a new entity repository.
func NewEntityRepo(pool *pgxpool.Pool) *EntityRepo {
	return &EntityRepo{pool: pool}
}

// Create inserts a new entity into the tenant schema.
// profile/rebuild_after 列保留在 DB（历史数据），Go 侧不再写入，
// 依赖 DDL 的 DEFAULT ” 与 nullable 兜底。
func (r *EntityRepo) Create(ctx context.Context, tenantID string, entity *domain.MemoryEntity) error {
	const query = `
		INSERT INTO memory_entities (
			id, user_id, agent_id, scope, name, entity_type,
			fact_count, last_seen_at, status,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9,
			$10, $11
		)`

	var agentID *string
	if entity.AgentID != "" {
		agentID = &entity.AgentID
	}

	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, query,
			entity.ID, entity.UserID, agentID, string(entity.Scope), entity.Name, entity.EntityType,
			entity.FactCount, entity.LastSeenAt, entity.Status,
			entity.CreatedAt, entity.UpdatedAt,
		)
		return translatePgError(err, "create entity")
	})
}

// GetByID retrieves an entity by ID from the tenant schema.
func (r *EntityRepo) GetByID(ctx context.Context, tenantID, id string) (*domain.MemoryEntity, error) {
	const query = `
		SELECT id, user_id, agent_id, scope, name, entity_type,
			fact_count, last_seen_at, status,
			created_at, updated_at
		FROM memory_entities
		WHERE id = $1`

	var out *domain.MemoryEntity
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var e domain.MemoryEntity
		var agentID *string
		var scope string
		if err := tx.QueryRow(ctx, query, id).Scan(
			&e.ID, &e.UserID, &agentID, &scope, &e.Name, &e.EntityType,
			&e.FactCount, &e.LastSeenAt, &e.Status,
			&e.CreatedAt, &e.UpdatedAt,
		); err != nil {
			if err == pgx.ErrNoRows {
				return domain.ErrEntityNotFound
			}
			return fmt.Errorf("get entity by id: %w", err)
		}
		e.Scope = domain.Scope(scope)
		if agentID != nil {
			e.AgentID = *agentID
		}
		out = &e
		return nil
	})
	return out, err
}

// Update modifies an existing entity in the tenant schema.
func (r *EntityRepo) Update(ctx context.Context, tenantID string, entity *domain.MemoryEntity) error {
	const query = `
		UPDATE memory_entities SET
			name = $2, entity_type = $3, fact_count = $4,
			last_seen_at = $5, status = $6, updated_at = $7
		WHERE id = $1`

	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, query,
			entity.ID, entity.Name, entity.EntityType, entity.FactCount,
			entity.LastSeenAt, entity.Status, entity.UpdatedAt,
		)
		if err != nil {
			return translatePgError(err, "update entity")
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrEntityNotFound
		}
		return nil
	})
}

// FindByNameAndType finds an entity by fuzzy name match within a scope using trigram similarity.
func entityScopeClause(filter domain.ScopeFilter) string {
	if filter.IncludeAgentScope && !filter.IncludeUserScope {
		return "scope = 'agent' AND agent_id = $5"
	}
	return "scope = 'user'"
}

func (r *EntityRepo) FindByNameAndType(ctx context.Context, tenantID string, filter domain.ScopeFilter, name, entityType string, threshold float64) (*domain.MemoryEntity, error) {
	query := `
		SELECT id, user_id, agent_id, scope, name, entity_type,
			fact_count, last_seen_at, status,
			created_at, updated_at,
			similarity(name, $2) as sim
		FROM memory_entities
		WHERE user_id = $1
			AND entity_type = $3
			AND status = 'active'
			AND similarity(name, $2) > $4
			AND ` + entityScopeClause(filter) + `
		ORDER BY sim DESC
		LIMIT 1`

	var out *domain.MemoryEntity
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var e domain.MemoryEntity
		var agentID *string
		var scope string
		var sim float64
		args := []any{filter.UserID, name, entityType, threshold}
		if filter.IncludeAgentScope && !filter.IncludeUserScope {
			args = append(args, filter.AgentID)
		}
		if err := tx.QueryRow(ctx, query, args...).Scan(
			&e.ID, &e.UserID, &agentID, &scope, &e.Name, &e.EntityType,
			&e.FactCount, &e.LastSeenAt, &e.Status,
			&e.CreatedAt, &e.UpdatedAt, &sim,
		); err != nil {
			if err == pgx.ErrNoRows {
				return domain.ErrEntityNotFound
			}
			return fmt.Errorf("find entity by name and type: %w", err)
		}
		e.Scope = domain.Scope(scope)
		if agentID != nil {
			e.AgentID = *agentID
		}
		out = &e
		return nil
	})
	return out, err
}

// ListUserEntities lists the user's active user-scope entities as lightweight
// topic tags, newest-seen first. 与 ListUserFacts 同口径（scope='user' AND active）。
func (r *EntityRepo) ListUserEntities(ctx context.Context, tenantID, userID string, limit, offset int) ([]*domain.MemoryEntity, error) {
	const query = `
		SELECT id, name, entity_type, fact_count, last_seen_at
		FROM memory_entities
		WHERE user_id = $1 AND scope = 'user' AND status = 'active'
		ORDER BY last_seen_at DESC
		LIMIT $2 OFFSET $3`

	var out []*domain.MemoryEntity
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query, userID, limit, offset)
		if err != nil {
			return fmt.Errorf("list user entities: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var e domain.MemoryEntity
			if err := rows.Scan(&e.ID, &e.Name, &e.EntityType, &e.FactCount, &e.LastSeenAt); err != nil {
				return fmt.Errorf("scan user entity: %w", err)
			}
			out = append(out, &e)
		}
		return rows.Err()
	})
	return out, err
}

// CountUserEntities returns the user's active user-scope entity count,
// 与 ListUserEntities 同口径。
func (r *EntityRepo) CountUserEntities(ctx context.Context, tenantID, userID string) (int, error) {
	var count int
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			"SELECT COUNT(*) FROM memory_entities WHERE user_id = $1 AND scope = 'user' AND status = 'active'",
			userID).Scan(&count)
	})
	if err != nil {
		return 0, fmt.Errorf("count user entities: %w", err)
	}
	return count, nil
}

func (r *EntityRepo) execTenant(ctx context.Context, tenantID string, fn func(context.Context, pgx.Tx) error) error {
	return pgstore.ExecTenantWith(ctx, r.pool, tenantID, fn)
}

// DeleteAllByUser hard-deletes every memory_entities row owned by userID within the tenant schema.
func (r *EntityRepo) DeleteAllByUser(ctx context.Context, tenantID, userID string) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM memory_entities WHERE user_id = $1`, userID)
		if err != nil {
			return fmt.Errorf("delete entities by user: %w", err)
		}
		return nil
	})
}

// DeleteAllByAgent hard-deletes every memory_entities row owned by agentID within the tenant schema.
func (r *EntityRepo) DeleteAllByAgent(ctx context.Context, tenantID, agentID string) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM memory_entities WHERE agent_id = $1 AND scope = 'agent'`, agentID)
		if err != nil {
			return fmt.Errorf("delete entities by agent: %w", err)
		}
		return nil
	})
}

// Delete removes a single entity by id. A zero-row delete maps to
// domain.ErrEntityNotFound so callers can distinguish a miss from a no-op.
func (r *EntityRepo) Delete(ctx context.Context, tenantID, id string) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM memory_entities WHERE id = $1`, id)
		if err != nil {
			return translatePgError(err, "delete entity")
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrEntityNotFound
		}
		return nil
	})
}
