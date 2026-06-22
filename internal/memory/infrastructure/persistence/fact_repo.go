package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FactRepo implements domain/port.FactRepo using PostgreSQL with tenant isolation.
type FactRepo struct {
	pool *pgxpool.Pool
}

// NewFactRepo creates a new fact repository.
func NewFactRepo(pool *pgxpool.Pool) *FactRepo {
	return &FactRepo{pool: pool}
}

// Create inserts a new fact into the tenant schema.
func (r *FactRepo) Create(ctx context.Context, fact *domain.MemoryFact) error {
	query := `
		INSERT INTO memory_facts (
			id, user_id, agent_id, scope, content, importance,
			status, superseded_by, access_count, last_accessed_at,
			created_at, updated_at, deleted_at, frecency_score
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10,
			$11, $12, $13, $14
		)`

	var agentID *string
	if fact.AgentID != "" {
		agentID = &fact.AgentID
	}

	var supersededBy *string
	if fact.SupersededBy != "" {
		supersededBy = &fact.SupersededBy
	}

	var deletedAt *time.Time
	if !fact.DeletedAt.IsZero() {
		deletedAt = &fact.DeletedAt
	}

	// Calculate frecency score (for now, just use importance as base)
	frecencyScore := fact.Importance

	_, err := r.pool.Exec(ctx, query,
		fact.ID, fact.UserID, agentID, string(fact.Scope), fact.Content, fact.Importance,
		fact.Status, supersededBy, fact.AccessCount, fact.LastAccessAt,
		fact.CreatedAt, fact.UpdatedAt, deletedAt, frecencyScore,
	)

	if err != nil {
		return translatePgError(err, "create fact")
	}

	return nil
}

// GetByID retrieves a fact by ID from the tenant schema.
func (r *FactRepo) GetByID(ctx context.Context, id string) (*domain.MemoryFact, error) {
	query := `
		SELECT id, user_id, agent_id, scope, content, importance,
			status, superseded_by, access_count, last_accessed_at,
			created_at, updated_at, deleted_at
		FROM memory_facts
		WHERE id = $1`

	var f domain.MemoryFact
	var agentID, supersededBy *string
	var deletedAt *time.Time
	var scope string

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&f.ID, &f.UserID, &agentID, &scope, &f.Content, &f.Importance,
		&f.Status, &supersededBy, &f.AccessCount, &f.LastAccessAt,
		&f.CreatedAt, &f.UpdatedAt, &deletedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrFactNotFound
		}
		return nil, fmt.Errorf("get fact by id: %w", err)
	}

	f.Scope = domain.Scope(scope)
	if agentID != nil {
		f.AgentID = *agentID
	}
	if supersededBy != nil {
		f.SupersededBy = *supersededBy
	}
	if deletedAt != nil {
		f.DeletedAt = *deletedAt
	}

	return &f, nil
}

// Update modifies an existing fact in the tenant schema.
func (r *FactRepo) Update(ctx context.Context, fact *domain.MemoryFact) error {
	query := `
		UPDATE memory_facts SET
			content = $2, importance = $3, status = $4, superseded_by = $5,
			access_count = $6, last_accessed_at = $7, updated_at = $8, deleted_at = $9
		WHERE id = $1`

	var supersededBy *string
	if fact.SupersededBy != "" {
		supersededBy = &fact.SupersededBy
	}

	var deletedAt *time.Time
	if !fact.DeletedAt.IsZero() {
		deletedAt = &fact.DeletedAt
	}

	tag, err := r.pool.Exec(ctx, query,
		fact.ID, fact.Content, fact.Importance, fact.Status, supersededBy,
		fact.AccessCount, fact.LastAccessAt, fact.UpdatedAt, deletedAt,
	)

	if err != nil {
		return translatePgError(err, "update fact")
	}

	if tag.RowsAffected() == 0 {
		return domain.ErrFactNotFound
	}

	return nil
}

// ListActive returns active facts within a scope.
func (r *FactRepo) ListActive(ctx context.Context, filter domain.ScopeFilter, limit int) ([]*domain.MemoryFact, error) {
	query := `
		SELECT id, user_id, agent_id, scope, content, importance,
			status, superseded_by, access_count, last_accessed_at,
			created_at, updated_at, deleted_at
		FROM memory_facts
		WHERE user_id = $1
			AND status = 'active'
			AND (
				(scope = 'user' AND $2 = true)
				OR (scope = 'agent' AND agent_id = $3 AND $4 = true)
			)
		ORDER BY last_accessed_at DESC
		LIMIT $5`

	rows, err := r.pool.Query(ctx, query,
		filter.UserID, filter.IncludeUserScope, filter.AgentID, filter.IncludeAgentScope, limit)
	if err != nil {
		return nil, fmt.Errorf("list active facts: %w", err)
	}
	defer rows.Close()

	return r.scanFacts(rows)
}

// SearchByContent performs full-text search on fact content.
func (r *FactRepo) SearchByContent(ctx context.Context, filter domain.ScopeFilter, query string, limit int) ([]*domain.MemoryFact, error) {
	sql := `
		SELECT id, user_id, agent_id, scope, content, importance,
			status, superseded_by, access_count, last_accessed_at,
			created_at, updated_at, deleted_at
		FROM memory_facts
		WHERE user_id = $1
			AND status = 'active'
			AND content ILIKE $2
			AND (
				(scope = 'user' AND $3 = true)
				OR (scope = 'agent' AND agent_id = $4 AND $5 = true)
			)
		ORDER BY importance DESC
		LIMIT $6`

	searchPattern := "%" + query + "%"
	rows, err := r.pool.Query(ctx, sql,
		filter.UserID, searchPattern, filter.IncludeUserScope, filter.AgentID, filter.IncludeAgentScope, limit)
	if err != nil {
		return nil, fmt.Errorf("search facts by content: %w", err)
	}
	defer rows.Close()

	return r.scanFacts(rows)
}

// FindSupersedeCandidates returns facts that may be superseded by new content.
// Uses trigram similarity (requires pg_trgm extension).
func (r *FactRepo) FindSupersedeCandidates(ctx context.Context, userID, agentID, content string, minSimilarity, maxCount float64) ([]*domain.MemoryFact, error) {
	query := `
		SELECT id, user_id, agent_id, scope, content, importance,
			status, superseded_by, access_count, last_accessed_at,
			created_at, updated_at, deleted_at,
			similarity(content, $2) as sim
		FROM memory_facts
		WHERE user_id = $1
			AND status = 'active'
			AND similarity(content, $2) > $3
		ORDER BY sim DESC
		LIMIT $4`

	rows, err := r.pool.Query(ctx, query, userID, content, minSimilarity, int(maxCount))
	if err != nil {
		return nil, fmt.Errorf("find supersede candidates: %w", err)
	}
	defer rows.Close()

	var facts []*domain.MemoryFact
	for rows.Next() {
		var f domain.MemoryFact
		var agentID, supersededBy *string
		var deletedAt *time.Time
		var scope string
		var sim float64

		err := rows.Scan(
			&f.ID, &f.UserID, &agentID, &scope, &f.Content, &f.Importance,
			&f.Status, &supersededBy, &f.AccessCount, &f.LastAccessAt,
			&f.CreatedAt, &f.UpdatedAt, &deletedAt, &sim,
		)
		if err != nil {
			return nil, fmt.Errorf("scan supersede candidate: %w", err)
		}

		f.Scope = domain.Scope(scope)
		if agentID != nil {
			f.AgentID = *agentID
		}
		if supersededBy != nil {
			f.SupersededBy = *supersededBy
		}
		if deletedAt != nil {
			f.DeletedAt = *deletedAt
		}

		facts = append(facts, &f)
	}

	return facts, rows.Err()
}

// CountByUser returns total fact count for a user.
func (r *FactRepo) CountByUser(ctx context.Context, userID string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM memory_facts WHERE user_id = $1 AND status = 'active'",
		userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count facts by user: %w", err)
	}
	return count, nil
}

// DeleteOldSoftDeleted removes soft-deleted facts older than retention days.
func (r *FactRepo) DeleteOldSoftDeleted(ctx context.Context, retentionDays int) (int, error) {
	query := `
		DELETE FROM memory_facts
		WHERE status = 'deleted'
			AND deleted_at < NOW() - ($1 || ' days')::INTERVAL`

	tag, err := r.pool.Exec(ctx, query, retentionDays)
	if err != nil {
		return 0, fmt.Errorf("delete old soft-deleted facts: %w", err)
	}

	return int(tag.RowsAffected()), nil
}

// scanFacts is a helper to scan multiple fact rows.
func (r *FactRepo) scanFacts(rows pgx.Rows) ([]*domain.MemoryFact, error) {
	var facts []*domain.MemoryFact

	for rows.Next() {
		var f domain.MemoryFact
		var agentID, supersededBy *string
		var deletedAt *time.Time
		var scope string

		err := rows.Scan(
			&f.ID, &f.UserID, &agentID, &scope, &f.Content, &f.Importance,
			&f.Status, &supersededBy, &f.AccessCount, &f.LastAccessAt,
			&f.CreatedAt, &f.UpdatedAt, &deletedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan fact: %w", err)
		}

		f.Scope = domain.Scope(scope)
		if agentID != nil {
			f.AgentID = *agentID
		}
		if supersededBy != nil {
			f.SupersededBy = *supersededBy
		}
		if deletedAt != nil {
			f.DeletedAt = *deletedAt
		}

		facts = append(facts, &f)
	}

	return facts, rows.Err()
}

// CountActive returns active (not superseded) fact count for a tenant.
func (r *FactRepo) CountActive(ctx context.Context, tenantID string) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM memory_facts WHERE superseded_by IS NULL AND deleted_at IS NULL`
	err := r.pool.QueryRow(ctx, query).Scan(&count)
	if err != nil {
		return 0, translatePgError(err, "count active facts")
	}
	return count, nil
}

// CountSuperseded returns superseded fact count for a tenant.
func (r *FactRepo) CountSuperseded(ctx context.Context, tenantID string) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM memory_facts WHERE superseded_by IS NOT NULL AND deleted_at IS NULL`
	err := r.pool.QueryRow(ctx, query).Scan(&count)
	if err != nil {
		return 0, translatePgError(err, "count superseded facts")
	}
	return count, nil
}

// translatePgError converts pgx errors to domain errors.
func translatePgError(err error, operation string) error {
	if err == nil {
		return nil
	}

	if pgErr, ok := err.(*pgconn.PgError); ok {
		switch pgErr.Code {
		case "23505": // unique_violation
			return fmt.Errorf("%s: duplicate entry: %w", operation, err)
		case "23503": // foreign_key_violation
			return fmt.Errorf("%s: foreign key violation: %w", operation, err)
		case "23514": // check_violation
			return fmt.Errorf("%s: constraint violation: %w", operation, err)
		}
	}

	return fmt.Errorf("%s: %w", operation, err)
}
