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
