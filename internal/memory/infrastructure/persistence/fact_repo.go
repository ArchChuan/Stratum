package persistence

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type FactRepo struct {
	pool *pgxpool.Pool
}

func NewFactRepo(pool *pgxpool.Pool) *FactRepo {
	return &FactRepo{pool: pool}
}

func (r *FactRepo) execTenant(ctx context.Context, tenantID string, fn func(context.Context, pgx.Tx) error) error {
	if r.pool == nil || tenantID == "" {
		return nil
	}
	return pgstore.Wrap(r.pool).ExecTenant(ctx, tenantID, fn)
}

func (r *FactRepo) Create(ctx context.Context, tenantID string, fact *domain.MemoryFact) error {
	const query = `
		INSERT INTO memory_facts (
			id, user_id, agent_id, scope, conversation_id, content, importance,
			status, superseded_by, access_count, last_accessed_at,
			created_at, updated_at, frecency_score,
			category, confidence, source
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`

	var agentID, supersededBy, conversationID *string
	if fact.AgentID != "" {
		agentID = &fact.AgentID
	}
	if fact.ConversationID != "" {
		conversationID = &fact.ConversationID
	}
	if fact.SupersededBy != "" {
		supersededBy = &fact.SupersededBy
	}

	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, query,
			fact.ID, fact.UserID, agentID, string(fact.Scope), conversationID, fact.Content, fact.Importance,
			fact.Status, supersededBy, fact.AccessCount, fact.LastAccessAt,
			fact.CreatedAt, fact.UpdatedAt, fact.FrecencyScore,
			fact.Category, fact.Confidence, fact.Source,
		)
		return translatePgError(err, "create fact")
	})
}

func (r *FactRepo) GetByID(ctx context.Context, tenantID, id string) (*domain.MemoryFact, error) {
	const query = `
		SELECT id, user_id, agent_id, scope, conversation_id, content, importance,
			status, superseded_by, access_count, last_accessed_at,
			created_at, updated_at, frecency_score,
			category, confidence, source
		FROM memory_facts WHERE id = $1`

	var f *domain.MemoryFact
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var fact domain.MemoryFact
		var agentID, conversationID, supersededBy *string
		var scope string

		err := tx.QueryRow(ctx, query, id).Scan(
			&fact.ID, &fact.UserID, &agentID, &scope, &conversationID, &fact.Content, &fact.Importance,
			&fact.Status, &supersededBy, &fact.AccessCount, &fact.LastAccessAt,
			&fact.CreatedAt, &fact.UpdatedAt, &fact.FrecencyScore,
			&fact.Category, &fact.Confidence, &fact.Source,
		)
		if err != nil {
			if err == pgx.ErrNoRows {
				return domain.ErrFactNotFound
			}
			return fmt.Errorf("get fact by id: %w", err)
		}
		fact.Scope = domain.Scope(scope)
		if agentID != nil {
			fact.AgentID = *agentID
		}
		if conversationID != nil {
			fact.ConversationID = *conversationID
		}
		if supersededBy != nil {
			fact.SupersededBy = *supersededBy
		}
		f = &fact
		return nil
	})
	return f, err
}

func (r *FactRepo) Update(ctx context.Context, tenantID string, fact *domain.MemoryFact) error {
	const query = `
		UPDATE memory_facts SET
			content = $2, importance = $3, status = $4, superseded_by = $5,
			access_count = $6, last_accessed_at = $7, updated_at = $8,
			frecency_score = $9
		WHERE id = $1`

	var supersededBy *string
	if fact.SupersededBy != "" {
		supersededBy = &fact.SupersededBy
	}

	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, query,
			fact.ID, fact.Content, fact.Importance, fact.Status, supersededBy,
			fact.AccessCount, fact.LastAccessAt, fact.UpdatedAt, fact.FrecencyScore,
		)
		if err != nil {
			return translatePgError(err, "update fact")
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrFactNotFound
		}
		return nil
	})
}

func (r *FactRepo) ListActive(ctx context.Context, tenantID string, filter domain.ScopeFilter, limit int) ([]*domain.MemoryFact, error) {
	const query = `
		SELECT id, user_id, agent_id, scope, conversation_id, content, importance,
			status, superseded_by, access_count, last_accessed_at,
			created_at, updated_at, frecency_score,
			category, confidence, source
		FROM memory_facts
		WHERE user_id = $1 AND status = 'active'
			AND (
				(scope = 'user' AND $2 = true)
				OR (scope = 'agent' AND agent_id = $3 AND $4 = true)
			)
		ORDER BY last_accessed_at DESC LIMIT $5`

	var facts []*domain.MemoryFact
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query,
			filter.UserID, filter.IncludeUserScope, filter.AgentID, filter.IncludeAgentScope, limit)
		if err != nil {
			return fmt.Errorf("list active facts: %w", err)
		}
		defer rows.Close()
		facts, err = scanFacts(rows)
		return err
	})
	return facts, err
}

func (r *FactRepo) SearchByContent(ctx context.Context, tenantID string, filter domain.ScopeFilter, query string, limit int) ([]*domain.MemoryFact, error) {
	const sql = `
		SELECT id, user_id, agent_id, scope, conversation_id, content, importance,
			status, superseded_by, access_count, last_accessed_at,
			created_at, updated_at, frecency_score,
			category, confidence, source
		FROM memory_facts
		WHERE user_id = $1 AND status = 'active' AND content ILIKE $2
			AND (
				(scope = 'user' AND $3 = true)
				OR (scope = 'agent' AND agent_id = $4 AND $5 = true)
			)
		ORDER BY importance DESC LIMIT $6`

	searchPattern := "%" + query + "%"
	var facts []*domain.MemoryFact
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, sql,
			filter.UserID, searchPattern, filter.IncludeUserScope, filter.AgentID, filter.IncludeAgentScope, limit)
		if err != nil {
			return fmt.Errorf("search facts by content: %w", err)
		}
		defer rows.Close()
		facts, err = scanFacts(rows)
		return err
	})
	return facts, err
}

func (r *FactRepo) FindSupersedeCandidates(ctx context.Context, tenantID, userID, agentID, content string, minSimilarity, maxCount float64) ([]*port.SupersedeCandidate, error) {
	const query = `
		SELECT id, user_id, agent_id, scope, content, importance,
			status, superseded_by, access_count, last_accessed_at,
			created_at, updated_at,
			similarity(content, $2) as sim
		FROM memory_facts
		WHERE user_id = $1 AND status = 'active' AND similarity(content, $2) > $3
		ORDER BY sim DESC LIMIT $4`

	var candidates []*port.SupersedeCandidate
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query, userID, content, minSimilarity, int(maxCount))
		if err != nil {
			return fmt.Errorf("find supersede candidates: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var f domain.MemoryFact
			var aid, supersededBy *string
			var scope string
			var sim float64

			if err := rows.Scan(
				&f.ID, &f.UserID, &aid, &scope, &f.Content, &f.Importance,
				&f.Status, &supersededBy, &f.AccessCount, &f.LastAccessAt,
				&f.CreatedAt, &f.UpdatedAt, &sim,
			); err != nil {
				return fmt.Errorf("scan supersede candidate: %w", err)
			}
			f.Scope = domain.Scope(scope)
			if aid != nil {
				f.AgentID = *aid
			}
			if supersededBy != nil {
				f.SupersededBy = *supersededBy
			}
			candidates = append(candidates, &port.SupersedeCandidate{Fact: &f, Similarity: sim})
		}
		return rows.Err()
	})
	return candidates, err
}

func (r *FactRepo) CountByUser(ctx context.Context, tenantID, userID string) (int, error) {
	var count int
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			"SELECT COUNT(*) FROM memory_facts WHERE user_id = $1 AND status = 'active'",
			userID).Scan(&count)
	})
	if err != nil {
		return 0, fmt.Errorf("count facts by user: %w", err)
	}
	return count, nil
}

func (r *FactRepo) Delete(ctx context.Context, tenantID, id string) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM memory_facts WHERE id = $1`, id)
		return translatePgError(err, "delete fact")
	})
}

func (r *FactRepo) DeleteAllByUser(ctx context.Context, tenantID, userID string) ([]string, error) {
	const query = `DELETE FROM memory_facts WHERE user_id = $1 RETURNING id`

	var factIDs []string
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query, userID)
		if err != nil {
			return fmt.Errorf("delete all by user: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return fmt.Errorf("scan deleted fact id: %w", err)
			}
			factIDs = append(factIDs, id)
		}
		return rows.Err()
	})
	return factIDs, err
}

func (r *FactRepo) DeleteAllByAgent(ctx context.Context, tenantID, agentID string) ([]string, error) {
	const query = `DELETE FROM memory_facts WHERE agent_id = $1 RETURNING id`

	var factIDs []string
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query, agentID)
		if err != nil {
			return fmt.Errorf("delete all by agent: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return fmt.Errorf("scan deleted fact id: %w", err)
			}
			factIDs = append(factIDs, id)
		}
		return rows.Err()
	})
	return factIDs, err
}

func scanFacts(rows pgx.Rows) ([]*domain.MemoryFact, error) {
	var facts []*domain.MemoryFact
	for rows.Next() {
		var f domain.MemoryFact
		var agentID, conversationID, supersededBy *string
		var scope string

		if err := rows.Scan(
			&f.ID, &f.UserID, &agentID, &scope, &conversationID, &f.Content, &f.Importance,
			&f.Status, &supersededBy, &f.AccessCount, &f.LastAccessAt,
			&f.CreatedAt, &f.UpdatedAt, &f.FrecencyScore,
			&f.Category, &f.Confidence, &f.Source,
		); err != nil {
			return nil, fmt.Errorf("scan fact: %w", err)
		}
		f.Scope = domain.Scope(scope)
		if agentID != nil {
			f.AgentID = *agentID
		}
		if conversationID != nil {
			f.ConversationID = *conversationID
		}
		if supersededBy != nil {
			f.SupersededBy = *supersededBy
		}
		facts = append(facts, &f)
	}
	return facts, rows.Err()
}

func translatePgError(err error, operation string) error {
	if err == nil {
		return nil
	}
	if pgErr, ok := err.(*pgconn.PgError); ok {
		switch pgErr.Code {
		case "23505":
			return fmt.Errorf("%s: duplicate entry: %w", operation, err)
		case "23503":
			return fmt.Errorf("%s: foreign key violation: %w", operation, err)
		case "23514":
			return fmt.Errorf("%s: constraint violation: %w", operation, err)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
