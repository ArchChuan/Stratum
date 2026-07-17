package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const historyBatchQuery = `
	WITH eligible AS (
		SELECT e.conversation_id, e.user_id, COALESCE(e.agent_id, '') AS agent_id, e.scope
		FROM memory_entries e
		WHERE e.enriched_at IS NOT NULL AND e.conversation_id IS NOT NULL
		  AND e.created_at > COALESCE((SELECT MAX(s.period_end) FROM memory_summaries s
			WHERE s.user_id=e.user_id AND s.agent_id=COALESCE(e.agent_id, '') AND s.scope=e.scope
			  AND s.conversation_id=e.conversation_id
			  AND s.status='active' AND s.aggregation_key IS NOT NULL), '1970-01-01')
		GROUP BY e.conversation_id, e.user_id, COALESCE(e.agent_id, ''), e.scope
		HAVING COUNT(*) >= $1
		ORDER BY MIN(e.created_at) ASC LIMIT 1
	)
	SELECT e.conversation_id::text, e.user_id, COALESCE(e.agent_id, ''), e.scope,
	       e.id::text, e.content, e.created_at
	FROM memory_entries e JOIN eligible x ON x.conversation_id=e.conversation_id
	 AND x.user_id=e.user_id AND x.agent_id=COALESCE(e.agent_id, '') AND x.scope=e.scope
	WHERE e.created_at > COALESCE((SELECT MAX(s.period_end) FROM memory_summaries s
		WHERE s.user_id=e.user_id AND s.agent_id=COALESCE(e.agent_id, '') AND s.scope=e.scope
			  AND s.conversation_id=e.conversation_id
		  AND s.status='active' AND s.aggregation_key IS NOT NULL), '1970-01-01')
	ORDER BY created_at ASC LIMIT $2`

const historyUpsertQuery = `
	INSERT INTO memory_summaries (conversation_id,user_id,agent_id,scope,summary,covered_until,token_count,
		tier,period_start,period_end,source_start,source_end,aggregation_key,importance,confidence,status,updated_at)
	VALUES ($1,$2,$3,$4,$5,$6,0,$7,$8,$9,$10,$11,$12,$13,$14,$15,NOW())
	ON CONFLICT (aggregation_key) WHERE aggregation_key IS NOT NULL DO NOTHING`

const historyPromotionQuery = `UPDATE memory_summaries SET tier=$3, updated_at=NOW()
	WHERE tier = $1 AND period_end < $2 AND status = 'active' AND aggregation_key IS NOT NULL`

const historyOverflowQuery = `
	WITH ranked AS (
		SELECT *, row_number() OVER (
			PARTITION BY user_id,agent_id,scope,tier
			ORDER BY importance DESC,confidence DESC,period_end DESC
		) rn, CASE tier WHEN 'recent_months' THEN $1 WHEN 'earlier_context' THEN $2 END max_segments
		FROM memory_summaries
		WHERE tier IN ('recent_months','earlier_context') AND status='active' AND aggregation_key IS NOT NULL
	), groups AS (
		SELECT user_id,agent_id,scope,tier,MIN(period_start) oldest
		FROM ranked WHERE rn > max_segments
		GROUP BY user_id,agent_id,scope,tier
	), chosen AS (
		SELECT user_id,agent_id,scope,tier FROM groups ORDER BY oldest LIMIT 1
	)
	SELECT r.id::text,r.conversation_id::text,r.user_id,r.agent_id,r.scope,r.tier,r.summary,
		r.period_start,r.period_end,r.source_start,r.source_end,r.importance,r.confidence
	FROM ranked r JOIN chosen g USING (user_id,agent_id,scope,tier)
	WHERE r.rn > r.max_segments
	ORDER BY r.period_start,r.id LIMIT $3`

const historyReplacementQuery = `
	WITH inserted AS (
		INSERT INTO memory_summaries (conversation_id,user_id,agent_id,scope,summary,covered_until,token_count,
			tier,period_start,period_end,source_start,source_end,aggregation_key,importance,confidence,status,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,0,$7,$8,$9,$10,$11,$12,$13,$14,$15,NOW())
		ON CONFLICT (aggregation_key) WHERE aggregation_key IS NOT NULL DO NOTHING
		RETURNING aggregation_key
	), present AS (
		SELECT aggregation_key FROM inserted
		UNION ALL
		SELECT aggregation_key FROM memory_summaries WHERE aggregation_key=$12 AND status='active'
	)
	UPDATE memory_summaries SET status='archived',updated_at=NOW()
	WHERE status='active' AND id::text = ANY($16::text[])
	  AND EXISTS (SELECT 1 FROM present)`

const archiveColdFactsQuery = `UPDATE memory_facts SET status='archived', updated_at=NOW()
	WHERE status='active' AND last_accessed_at < $1 AND importance < $2 AND confidence < $3
	  AND category <> 'preference' AND source <> 'explicit_user'`

const historyRelevanceQuery = `SELECT summary,tier FROM memory_summaries
	WHERE user_id = $1 AND status='active' AND aggregation_key IS NOT NULL
	  AND (scope = 'user' OR (scope='agent' AND agent_id = $2))
	ORDER BY similarity(summary, $3) DESC, importance DESC, confidence DESC, period_end DESC LIMIT $4`

type HistoryRepo struct{ pool *pgxpool.Pool }

func NewHistoryRepo(pool *pgxpool.Pool) *HistoryRepo { return &HistoryRepo{pool: pool} }

func (r *HistoryRepo) execTenant(ctx context.Context, tenantID string, fn func(context.Context, pgx.Tx) error) error {
	if r.pool == nil || tenantID == "" {
		return nil
	}
	return pgstore.Wrap(r.pool).ExecTenant(ctx, tenantID, fn)
}

func (r *HistoryRepo) NextBatch(ctx context.Context, tenantID string, minEntries, limit int) (*domain.HistoryBatch, error) {
	var batch *domain.HistoryBatch
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, historyBatchQuery, minEntries, limit)
		if err != nil {
			return fmt.Errorf("query history batch: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var conv, user, agent, scope, id, content string
			var created time.Time
			if err := rows.Scan(&conv, &user, &agent, &scope, &id, &content, &created); err != nil {
				return err
			}
			if batch == nil {
				batch = &domain.HistoryBatch{ConversationID: conv, UserID: user, AgentID: agent, Scope: domain.Scope(scope)}
			}
			batch.Entries = append(batch.Entries, domain.HistorySource{ID: id, Content: content, CreatedAt: created})
		}
		return rows.Err()
	})
	return batch, err
}

func (r *HistoryRepo) Upsert(ctx context.Context, tenantID string, h *domain.HistorySegment) error {
	if err := h.Validate(); err != nil {
		return err
	}
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, historyUpsertQuery, h.ConversationID, h.UserID, h.AgentID, string(h.Scope), h.Summary, h.PeriodEnd, h.Tier, h.PeriodStart, h.PeriodEnd, h.SourceStart, h.SourceEnd, h.AggregationKey, h.Importance, h.Confidence, h.Status)
		return err
	})
}

func (r *HistoryRepo) NextOverflow(ctx context.Context, tenantID string, recentMax, earlierMax, limit int) (*domain.HistoryOverflowGroup, error) {
	var group *domain.HistoryOverflowGroup
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, historyOverflowQuery, recentMax, earlierMax, limit)
		if err != nil {
			return fmt.Errorf("query history overflow: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var h domain.HistorySegment
			var scope string
			if err := rows.Scan(&h.ID, &h.ConversationID, &h.UserID, &h.AgentID, &scope, &h.Tier, &h.Summary,
				&h.PeriodStart, &h.PeriodEnd, &h.SourceStart, &h.SourceEnd, &h.Importance, &h.Confidence); err != nil {
				return err
			}
			h.Scope = domain.Scope(scope)
			if group == nil {
				group = &domain.HistoryOverflowGroup{UserID: h.UserID, AgentID: h.AgentID, Scope: h.Scope, Tier: h.Tier}
			}
			group.Sources = append(group.Sources, h)
		}
		return rows.Err()
	})
	return group, err
}

func (r *HistoryRepo) ReplaceOverflow(ctx context.Context, tenantID string, h *domain.HistorySegment, sourceIDs []string) error {
	if err := h.Validate(); err != nil {
		return err
	}
	if len(sourceIDs) == 0 {
		return domain.ErrInvalidHistorySegment
	}
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, historyReplacementQuery, h.ConversationID, h.UserID, h.AgentID, string(h.Scope), h.Summary,
			h.PeriodEnd, h.Tier, h.PeriodStart, h.PeriodEnd, h.SourceStart, h.SourceEnd, h.AggregationKey,
			h.Importance, h.Confidence, h.Status, sourceIDs)
		return err
	})
}

func (r *HistoryRepo) Maintain(ctx context.Context, tenantID string) error {
	now := time.Now().UTC()
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, historyPromotionQuery, domain.HistoryTierRecent, now.Add(-constants.HistoryRecentPromotionAge), domain.HistoryTierEarlier); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, historyPromotionQuery, domain.HistoryTierEarlier, now.Add(-constants.HistoryEarlierPromotionAge), domain.HistoryTierBackground); err != nil {
			return err
		}
		return nil
	})
}

func (r *HistoryRepo) ArchiveColdFacts(ctx context.Context, tenantID string) (int, error) {
	var n int64
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, archiveColdFactsQuery, time.Now().UTC().Add(-constants.HistoryArchiveInactiveAge), constants.HistoryProtectedImportance, constants.HistoryProtectedConfidence)
		n = tag.RowsAffected()
		return err
	})
	return int(n), err
}

func (r *HistoryRepo) SearchRelevant(ctx context.Context, tenantID, userID, agentID, query string, limit int) ([]domain.HistorySegment, error) {
	var out []domain.HistorySegment
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, historyRelevanceQuery, userID, agentID, query, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var h domain.HistorySegment
			if err := rows.Scan(&h.Summary, &h.Tier); err != nil {
				return err
			}
			out = append(out, h)
		}
		return rows.Err()
	})
	return out, err
}
