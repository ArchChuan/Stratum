package persistence

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgCaseSampleSource pairs evaluation_feedback rows with the production
// (query, response) conversation they were traced from. The join runs on
// chat_messages.trace_id, which is populated only for rows written after
// Phase 3c; historical conversations are unreachable by design.
type PgCaseSampleSource struct {
	pool poolIface
}

func NewPgCaseSampleSource(pool *pgxpool.Pool) *PgCaseSampleSource {
	return &PgCaseSampleSource{pool: pool}
}

// sampleQuery picks, per feedback trace, the latest user message and the
// first assistant message after it (the final answer, before the tool
// summary). role IN ('assistant','agent') tolerates the historical CHECK
// constraint drift between tenant_schema.sql and live databases.
const sampleQuery = `
WITH fb AS (
	SELECT id AS feedback_id, trace_id, score, outcome, created_at
	FROM evaluation_feedback
	WHERE resource_kind = $1 AND trace_id <> ''
),
last_user AS (
	SELECT DISTINCT ON (trace_id) trace_id, content, created_at
	FROM chat_messages
	WHERE role = 'user' AND content <> '' AND trace_id <> ''
	ORDER BY trace_id, created_at DESC
),
first_answer AS (
	SELECT DISTINCT ON (u.trace_id) u.trace_id, m.content
	FROM last_user u
	JOIN chat_messages m ON m.trace_id = u.trace_id
		AND m.role IN ('assistant', 'agent')
		AND m.content <> ''
		AND m.created_at > u.created_at
	ORDER BY u.trace_id, m.created_at ASC
)
SELECT fb.feedback_id, fb.trace_id, fb.score, fb.outcome, u.content, a.content
FROM fb
JOIN last_user u ON u.trace_id = fb.trace_id
JOIN first_answer a ON a.trace_id = fb.trace_id
ORDER BY (COALESCE(fb.score, 0.5) < 0.5) DESC, fb.created_at DESC
LIMIT $2`

// ListSamples returns up to limit sampled interactions for the suite's
// resource kind, negative feedback first. Balanced policy alternates
// negative and non-negative samples in Go after the SQL ordering.
func (r *PgCaseSampleSource) ListSamples(
	ctx context.Context,
	tenantID string,
	kind domain.ResourceKind,
	policy domain.SamplePolicy,
	limit int,
) ([]domain.CaseSample, error) {
	samples := make([]domain.CaseSample, 0, limit)
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, sampleQuery, string(kind), limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var sample domain.CaseSample
			var outcomeJSON []byte
			if err := rows.Scan(&sample.FeedbackRef, &sample.TraceID, &sample.Score, &outcomeJSON, &sample.Query, &sample.Response); err != nil {
				return err
			}
			_ = json.Unmarshal(outcomeJSON, &sample.Outcome)
			samples = append(samples, sample)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("case sample source: %w", err)
	}
	if policy == domain.SamplePolicyBalanced {
		return interleaveBalanced(samples), nil
	}
	return samples, nil
}

// interleaveBalanced alternates negative (score < 0.5 or missing) and
// non-negative samples so a balanced generation set is not dominated by one
// side when the source order groups them.
func interleaveBalanced(in []domain.CaseSample) []domain.CaseSample {
	var negative, positive []domain.CaseSample
	for _, s := range in {
		if isNegative(s.Score) {
			negative = append(negative, s)
		} else {
			positive = append(positive, s)
		}
	}
	out := make([]domain.CaseSample, 0, len(in))
	for i := 0; i < len(in); i++ {
		fromNeg := i%2 == 0
		if fromNeg && len(negative) > 0 {
			out = append(out, negative[0])
			negative = negative[1:]
			continue
		}
		if !fromNeg && len(positive) > 0 {
			out = append(out, positive[0])
			positive = positive[1:]
			continue
		}
		if len(negative) > 0 {
			out = append(out, negative[0])
			negative = negative[1:]
			continue
		}
		if len(positive) > 0 {
			out = append(out, positive[0])
			positive = positive[1:]
		}
	}
	return out
}

// isNegative mirrors the SQL ordering predicate: a missing score counts as
// non-negative so the default feedback (no score) still lands in the
// balanced mix instead of dominating negative-first orderings.
func isNegative(score *float64) bool {
	return score != nil && *score < 0.5
}

func (r *PgCaseSampleSource) execTenant(
	ctx context.Context,
	tenantID string,
	fn func(context.Context, pgx.Tx) error,
) error {
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	return execTenantTx(ctx, r.pool, tenantID, fn)
}
