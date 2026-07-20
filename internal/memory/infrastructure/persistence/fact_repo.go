package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type FactRepo struct {
	pool *pgxpool.Pool
}

// CreateExtracted atomically inserts one source-identified fact and applies its entity mutations.
// A replay with the same payload returns the persisted fact; a changed payload fails closed.
func (r *FactRepo) CreateExtracted(ctx context.Context, tenantID string, write *port.ExtractedFactWrite) (*domain.MemoryFact, bool, error) {
	if err := validateExtractedFactWrite(tenantID, write); err != nil {
		return nil, false, err
	}
	var result *domain.MemoryFact
	var created bool
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		fact := write.Fact
		var agentID, conversationID *string
		if fact.AgentID != "" {
			agentID = &fact.AgentID
		}
		if fact.ConversationID != "" {
			conversationID = &fact.ConversationID
		}
		var insertedID string
		err := tx.QueryRow(ctx, `
			INSERT INTO memory_facts (
				id,user_id,agent_id,scope,conversation_id,content,importance,status,
				access_count,last_accessed_at,created_at,updated_at,frecency_score,
				category,confidence,source,source_message_id,source_task_id,source_ordinal,source_payload_hash
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
			ON CONFLICT DO NOTHING RETURNING id::text`,
			fact.ID, fact.UserID, agentID, string(fact.Scope), conversationID, fact.Content, fact.Importance, fact.Status,
			fact.AccessCount, fact.LastAccessAt, fact.CreatedAt, fact.UpdatedAt, fact.FrecencyScore,
			fact.Category, fact.Confidence, fact.Source, write.Identity.MessageID, nullableTaskID(write.Identity.TaskID),
			write.Identity.Ordinal, write.PayloadHash).Scan(&insertedID)
		switch {
		case err == nil:
			created = true
			fact.SourceIdentity = &domain.FactSourceIdentity{MessageID: write.Identity.MessageID, TaskID: write.Identity.TaskID, Ordinal: write.Identity.Ordinal}
			fact.SourcePayloadHash = write.PayloadHash
			entityIDs, entityErr := mutateExtractedFactEntities(ctx, tx, fact, write.EntityNames)
			if entityErr != nil {
				return entityErr
			}
			fact.EntityNames = append([]string(nil), write.EntityNames...)
			fact.EntityIDs = entityIDs
			result = fact
			return nil
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("insert extracted fact: %w", err)
		}

		existing, existingHash, readErr := getExtractedFactByIdentity(ctx, tx, fact, write.Identity)
		if readErr != nil {
			return readErr
		}
		if existingHash != write.PayloadHash {
			return domain.ErrFactSourceConflict
		}
		existing.EntityNames = append([]string(nil), write.EntityNames...)
		result = existing
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return result, created, nil
}

func validateExtractedFactWrite(tenantID string, write *port.ExtractedFactWrite) error {
	if tenantID == "" || write == nil || write.Fact == nil || write.Fact.TenantID != tenantID || write.Fact.UserID == "" ||
		write.Identity.MessageID == "" || write.Identity.Ordinal < 0 || write.PayloadHash == "" {
		return domain.ErrInvalidFactSourceIdentity
	}
	switch write.Fact.Scope {
	case domain.ScopeUser:
		// Agent provenance is allowed on a user-owned fact but is not part of its ownership key.
	case domain.ScopeAgent:
		if write.Fact.AgentID == "" {
			return domain.ErrInvalidFactSourceIdentity
		}
	default:
		return domain.ErrInvalidFactSourceIdentity
	}
	return nil
}

func nullableTaskID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func getExtractedFactByIdentity(ctx context.Context, tx pgx.Tx, keyFact *domain.MemoryFact, identity domain.FactSourceIdentity) (*domain.MemoryFact, string, error) {
	query := `SELECT id::text,user_id,COALESCE(agent_id,''),scope,COALESCE(conversation_id::text,''),content,importance,
		status,COALESCE(superseded_by::text,''),access_count,last_accessed_at,created_at,updated_at,frecency_score,
		category,confidence,source,source_message_id,COALESCE(source_task_id,0),source_ordinal,source_payload_hash
		FROM memory_facts WHERE user_id=$1 AND source_message_id=$2 AND source_ordinal=$3 AND scope=$4`
	args := []any{keyFact.UserID, identity.MessageID, identity.Ordinal, string(keyFact.Scope)}
	if keyFact.Scope == domain.ScopeAgent {
		query += ` AND agent_id=$5`
		args = append(args, keyFact.AgentID)
	}
	query += ` FOR UPDATE`
	var fact domain.MemoryFact
	var scope, hash string
	var sourceTaskID int64
	if err := tx.QueryRow(ctx, query, args...).Scan(
		&fact.ID, &fact.UserID, &fact.AgentID, &scope, &fact.ConversationID, &fact.Content, &fact.Importance,
		&fact.Status, &fact.SupersededBy, &fact.AccessCount, &fact.LastAccessAt, &fact.CreatedAt, &fact.UpdatedAt, &fact.FrecencyScore,
		&fact.Category, &fact.Confidence, &fact.Source, &identity.MessageID, &sourceTaskID, &identity.Ordinal, &hash,
	); err != nil {
		return nil, "", fmt.Errorf("read extracted fact conflict: %w", err)
	}
	fact.TenantID = keyFact.TenantID
	fact.Scope = domain.Scope(scope)
	fact.SourceIdentity = &domain.FactSourceIdentity{MessageID: identity.MessageID, TaskID: sourceTaskID, Ordinal: identity.Ordinal}
	fact.SourcePayloadHash = hash
	return &fact, hash, nil
}

func mutateExtractedFactEntities(ctx context.Context, tx pgx.Tx, fact *domain.MemoryFact, names []string) ([]string, error) {
	ids := make([]string, 0, len(names))
	for _, name := range names {
		id, err := findExtractedFactEntityID(ctx, tx, fact, name)
		if err == nil {
			if err := tx.QueryRow(ctx, `UPDATE memory_entities SET fact_count=fact_count+1,
				fact_count_since_rebuild=fact_count_since_rebuild+1,last_seen_at=NOW(),updated_at=NOW()
				WHERE id=$1 RETURNING id::text`, id).Scan(&id); err != nil {
				return nil, fmt.Errorf("increment extracted fact entity: %w", err)
			}
			ids = append(ids, id)
			continue
		}
		if !errors.Is(err, domain.ErrEntityNotFound) {
			return nil, err
		}
		var agentID *string
		if fact.AgentID != "" {
			agentID = &fact.AgentID
		}
		if err := tx.QueryRow(ctx, `INSERT INTO memory_entities
			(user_id,agent_id,scope,name,entity_type,fact_count,fact_count_since_rebuild,last_seen_at)
			VALUES ($1,$2,$3,$4,'',1,1,NOW()) RETURNING id::text`,
			fact.UserID, agentID, string(fact.Scope), name).Scan(&id); err != nil {
			return nil, fmt.Errorf("create extracted fact entity %q: %w", name, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func findExtractedFactEntityID(ctx context.Context, tx pgx.Tx, fact *domain.MemoryFact, name string) (string, error) {
	query := `SELECT id::text FROM memory_entities WHERE user_id=$1 AND entity_type='' AND status='active'
		AND similarity(name,$2)>$3 AND scope=$4`
	args := []any{fact.UserID, name, constants.MemorySupersedeCandidateMin, string(fact.Scope)}
	if fact.Scope == domain.ScopeAgent {
		query += ` AND agent_id=$5`
		args = append(args, fact.AgentID)
	}
	query += ` ORDER BY similarity(name,$2) DESC LIMIT 1`
	var id string
	if err := tx.QueryRow(ctx, query, args...).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.ErrEntityNotFound
		}
		return "", fmt.Errorf("find extracted fact entity: %w", err)
	}
	return id, nil
}

func NewFactRepo(pool *pgxpool.Pool) *FactRepo {
	return &FactRepo{pool: pool}
}

func (r *FactRepo) execTenant(ctx context.Context, tenantID string, fn func(context.Context, pgx.Tx) error) error {
	if r.pool == nil {
		return fmt.Errorf("memory: fact persistence pool is nil")
	}
	if tenantID == "" {
		return fmt.Errorf("memory: tenant_id is empty")
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

func supersedeScopeClause(filter domain.ScopeFilter) string {
	if filter.IncludeAgentScope && !filter.IncludeUserScope {
		return "scope = 'agent' AND agent_id = $3"
	}
	return "scope = 'user'"
}

func supersedeQuery(filter domain.ScopeFilter, content string, minSimilarity, maxCount float64) (string, []any) {
	thresholdParam, limitParam := "$3", "$4"
	args := []any{filter.UserID, content, minSimilarity, int(maxCount)}
	if filter.IncludeAgentScope && !filter.IncludeUserScope {
		thresholdParam, limitParam = "$4", "$5"
		args = []any{filter.UserID, content, filter.AgentID, minSimilarity, int(maxCount)}
	}
	query := `
		SELECT id, user_id, agent_id, scope, content, importance,
			status, superseded_by, access_count, last_accessed_at,
			created_at, updated_at,
			similarity(content, $2) as sim
		FROM memory_facts
		WHERE user_id = $1 AND status = 'active' AND similarity(content, $2) > ` + thresholdParam + `
		  AND ` + supersedeScopeClause(filter) + `
		ORDER BY sim DESC LIMIT ` + limitParam
	return query, args
}

func (r *FactRepo) FindSupersedeCandidates(ctx context.Context, tenantID string, filter domain.ScopeFilter, content string, minSimilarity, maxCount float64) ([]*port.SupersedeCandidate, error) {
	query, args := supersedeQuery(filter, content, minSimilarity, maxCount)

	var candidates []*port.SupersedeCandidate
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query, args...)
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
	const query = `DELETE FROM memory_facts WHERE agent_id = $1 AND scope = 'agent' RETURNING id`

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

// PurgeSuperseded hard-deletes at most `limit` superseded facts whose updated_at
// predates `olderThan`. The cutoff is bound as a timestamp parameter (not via
// make_interval) to avoid the pgx int→interval OID encoding failure. Only
// status='superseded' rows are eligible — archived facts are long-term memory
// and must survive GC. Uses a bounded ctid subquery so a single pass never
// deletes an unbounded number of rows.
func (r *FactRepo) PurgeSuperseded(ctx context.Context, tenantID string, olderThan time.Time, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	const query = `
		DELETE FROM memory_facts
		WHERE ctid IN (
			SELECT ctid FROM memory_facts
			WHERE status = 'superseded' AND updated_at < $1
			LIMIT $2
		)`

	var n int
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, query, olderThan, limit)
		if err != nil {
			return translatePgError(err, "purge superseded facts")
		}
		n = int(tag.RowsAffected())
		return nil
	})
	return n, err
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
