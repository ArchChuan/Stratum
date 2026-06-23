package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EntityRepo implements domain/port.EntityRepo using PostgreSQL with tenant isolation.
type EntityRepo struct {
	pool *pgxpool.Pool
}

// NewEntityRepo creates a new entity repository.
func NewEntityRepo(pool *pgxpool.Pool) *EntityRepo {
	return &EntityRepo{pool: pool}
}

// Create inserts a new entity into the tenant schema.
func (r *EntityRepo) Create(ctx context.Context, entity *domain.MemoryEntity) error {
	query := `
		INSERT INTO memory_entities (
			id, user_id, agent_id, scope, name, entity_type, profile,
			fact_count, last_seen_at, rebuild_after, status,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11,
			$12, $13
		)`

	var agentID *string
	if entity.AgentID != "" {
		agentID = &entity.AgentID
	}

	var rebuildAfter *time.Time
	if !entity.LastProfileRebuildAt.IsZero() {
		// Calculate rebuild_after based on last rebuild + interval
		nextRebuild := entity.LastProfileRebuildAt.Add(7 * 24 * time.Hour)
		rebuildAfter = &nextRebuild
	}

	_, err := r.pool.Exec(ctx, query,
		entity.ID, entity.UserID, agentID, string(entity.Scope), entity.Name, entity.EntityType, entity.Profile,
		entity.FactCount, entity.LastSeenAt, rebuildAfter, entity.Status,
		entity.CreatedAt, entity.UpdatedAt,
	)

	if err != nil {
		return translatePgError(err, "create entity")
	}

	return nil
}

// GetByID retrieves an entity by ID from the tenant schema.
func (r *EntityRepo) GetByID(ctx context.Context, id string) (*domain.MemoryEntity, error) {
	query := `
		SELECT id, user_id, agent_id, scope, name, entity_type, profile,
			fact_count, last_seen_at, rebuild_after, status,
			created_at, updated_at
		FROM memory_entities
		WHERE id = $1`

	var e domain.MemoryEntity
	var agentID *string
	var rebuildAfter *time.Time
	var scope string

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&e.ID, &e.UserID, &agentID, &scope, &e.Name, &e.EntityType, &e.Profile,
		&e.FactCount, &e.LastSeenAt, &rebuildAfter, &e.Status,
		&e.CreatedAt, &e.UpdatedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrEntityNotFound
		}
		return nil, fmt.Errorf("get entity by id: %w", err)
	}

	e.Scope = domain.Scope(scope)
	if agentID != nil {
		e.AgentID = *agentID
	}
	// Note: domain uses LastProfileRebuildAt, not rebuild_after field directly
	// We derive it from the rebuild schedule

	return &e, nil
}

// Update modifies an existing entity in the tenant schema.
func (r *EntityRepo) Update(ctx context.Context, entity *domain.MemoryEntity) error {
	query := `
		UPDATE memory_entities SET
			name = $2, entity_type = $3, profile = $4, fact_count = $5,
			last_seen_at = $6, rebuild_after = $7, status = $8, updated_at = $9
		WHERE id = $1`

	var rebuildAfter *time.Time
	if !entity.LastProfileRebuildAt.IsZero() {
		nextRebuild := entity.LastProfileRebuildAt.Add(7 * 24 * time.Hour)
		rebuildAfter = &nextRebuild
	}

	tag, err := r.pool.Exec(ctx, query,
		entity.ID, entity.Name, entity.EntityType, entity.Profile, entity.FactCount,
		entity.LastSeenAt, rebuildAfter, entity.Status, entity.UpdatedAt,
	)

	if err != nil {
		return translatePgError(err, "update entity")
	}

	if tag.RowsAffected() == 0 {
		return domain.ErrEntityNotFound
	}

	return nil
}

// FindByNameAndType finds an entity by fuzzy name match within a scope using trigram similarity.
func (r *EntityRepo) FindByNameAndType(ctx context.Context, userID, name, entityType string, threshold float64) (*domain.MemoryEntity, error) {
	query := `
		SELECT id, user_id, agent_id, scope, name, entity_type, profile,
			fact_count, last_seen_at, rebuild_after, status,
			created_at, updated_at,
			similarity(name, $2) as sim
		FROM memory_entities
		WHERE user_id = $1
			AND entity_type = $3
			AND status = 'active'
			AND similarity(name, $2) > $4
		ORDER BY sim DESC
		LIMIT 1`

	var e domain.MemoryEntity
	var agentID *string
	var rebuildAfter *time.Time
	var scope string
	var sim float64

	err := r.pool.QueryRow(ctx, query, userID, name, entityType, threshold).Scan(
		&e.ID, &e.UserID, &agentID, &scope, &e.Name, &e.EntityType, &e.Profile,
		&e.FactCount, &e.LastSeenAt, &rebuildAfter, &e.Status,
		&e.CreatedAt, &e.UpdatedAt, &sim,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrEntityNotFound
		}
		return nil, fmt.Errorf("find entity by name and type: %w", err)
	}

	e.Scope = domain.Scope(scope)
	if agentID != nil {
		e.AgentID = *agentID
	}

	return &e, nil
}

// ListProfiles returns entities with profiles for context injection.
func (r *EntityRepo) ListProfiles(ctx context.Context, filter domain.ScopeFilter, limit int) ([]*domain.MemoryEntity, error) {
	query := `
		SELECT id, user_id, agent_id, scope, name, entity_type, profile,
			fact_count, last_seen_at, rebuild_after, status,
			created_at, updated_at
		FROM memory_entities
		WHERE user_id = $1
			AND status = 'active'
			AND profile != ''
			AND (
				(scope = 'user' AND $2 = true)
				OR (scope = 'agent' AND agent_id = $3 AND $4 = true)
			)
		ORDER BY fact_count DESC
		LIMIT $5`

	rows, err := r.pool.Query(ctx, query,
		filter.UserID, filter.IncludeUserScope, filter.AgentID, filter.IncludeAgentScope, limit)
	if err != nil {
		return nil, fmt.Errorf("list entity profiles: %w", err)
	}
	defer rows.Close()

	return r.scanEntities(rows)
}

// CountByUser returns total entity count for a user.
func (r *EntityRepo) CountByUser(ctx context.Context, userID string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM memory_entities WHERE user_id = $1 AND status = 'active'",
		userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count entities by user: %w", err)
	}
	return count, nil
}

// scanEntities is a helper to scan multiple entity rows.
func (r *EntityRepo) scanEntities(rows pgx.Rows) ([]*domain.MemoryEntity, error) {
	var entities []*domain.MemoryEntity

	for rows.Next() {
		var e domain.MemoryEntity
		var agentID *string
		var rebuildAfter *time.Time
		var scope string

		err := rows.Scan(
			&e.ID, &e.UserID, &agentID, &scope, &e.Name, &e.EntityType, &e.Profile,
			&e.FactCount, &e.LastSeenAt, &rebuildAfter, &e.Status,
			&e.CreatedAt, &e.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan entity: %w", err)
		}

		e.Scope = domain.Scope(scope)
		if agentID != nil {
			e.AgentID = *agentID
		}

		entities = append(entities, &e)
	}

	return entities, rows.Err()
}
