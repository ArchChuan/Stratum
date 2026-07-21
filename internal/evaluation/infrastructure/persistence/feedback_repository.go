package persistence

import (
	"context"
	"encoding/json"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PgFeedbackRepository struct {
	pool *pgxpool.Pool
}

func NewPgFeedbackRepository(pool *pgxpool.Pool) *PgFeedbackRepository {
	return &PgFeedbackRepository{pool: pool}
}

func (r *PgFeedbackRepository) Record(
	ctx context.Context,
	tenantID string,
	input domain.FeedbackRequest,
) (domain.EvaluationFeedback, error) {
	var feedback domain.EvaluationFeedback
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		outcome := make(map[string]any, len(input.Outcome)+1)
		for key, value := range input.Outcome {
			outcome[key] = value
		}
		if input.SecurityViolation {
			outcome["security_violation"] = true
		}
		outcomeJSON, err := json.Marshal(sanitizeValue(outcome))
		if err != nil {
			return err
		}
		var kind string
		var storedOutcome []byte
		feedback.ID = uuid.Must(uuid.NewV7()).String()
		err = tx.QueryRow(ctx,
			`INSERT INTO evaluation_feedback
			 (id, trace_id, resource_kind, resource_id, revision_id, score, outcome, idempotency_key)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			 ON CONFLICT (trace_id, resource_id) DO UPDATE SET
			 score=EXCLUDED.score, outcome=EXCLUDED.outcome, idempotency_key=EXCLUDED.idempotency_key
			 RETURNING id, trace_id, resource_kind, resource_id, revision_id, score, outcome, idempotency_key, created_at`,
			feedback.ID, input.TraceID, string(input.ResourceKind), input.ResourceID, input.RevisionID,
			input.Score, string(outcomeJSON), input.IdempotencyKey,
		).Scan(&feedback.ID, &feedback.TraceID, &kind, &feedback.ResourceID, &feedback.RevisionID,
			&feedback.Score, &storedOutcome, &feedback.IdempotencyKey, &feedback.CreatedAt)
		if err != nil {
			return err
		}
		feedback.ResourceKind = domain.ResourceKind(kind)
		return json.Unmarshal(storedOutcome, &feedback.Outcome)
	})
	return feedback, err
}

func (r *PgFeedbackRepository) ActiveExperiment(
	ctx context.Context,
	tenantID, resourceKind, resourceID string,
) (domain.Experiment, bool, error) {
	var experimentID string
	found := false
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT experiment_id FROM evaluation_deployments
			 WHERE resource_kind=$1 AND resource_id=$2 AND experiment_id IS NOT NULL`,
			resourceKind, resourceID,
		).Scan(&experimentID)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil || !found {
		return domain.Experiment{}, found, err
	}
	return NewPgExperimentRepository(r.pool).Get(ctx, tenantID, experimentID)
}

func (r *PgFeedbackRepository) StageFeedback(
	ctx context.Context,
	tenantID string,
	experiment domain.Experiment,
) ([]domain.EvaluationFeedback, int, error) {
	var feedback []domain.EvaluationFeedback
	observedMinutes := 0
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var stageStartedAt time.Time
		if err := tx.QueryRow(ctx, `SELECT updated_at FROM evaluation_experiments WHERE id=$1`, experiment.ID).Scan(&stageStartedAt); err != nil {
			return err
		}
		observedMinutes = int(time.Since(stageStartedAt).Minutes())
		var err error
		feedback, err = loadStageFeedback(ctx, tx, experiment.ResourceID, stageStartedAt)
		return err
	})
	return feedback, observedMinutes, err
}

func loadStageFeedback(
	ctx context.Context,
	tx pgx.Tx,
	resourceID string,
	stageStartedAt time.Time,
) ([]domain.EvaluationFeedback, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, trace_id, resource_kind, resource_id, revision_id, score, outcome, idempotency_key, created_at
		 FROM evaluation_feedback
		 WHERE resource_id=$1 AND created_at >= $2
		 ORDER BY created_at`, resourceID, stageStartedAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var feedback []domain.EvaluationFeedback
	for rows.Next() {
		var row domain.EvaluationFeedback
		var resourceKind string
		var outcome []byte
		if err := rows.Scan(&row.ID, &row.TraceID, &resourceKind, &row.ResourceID, &row.RevisionID,
			&row.Score, &outcome, &row.IdempotencyKey, &row.CreatedAt); err != nil {
			return nil, err
		}
		row.ResourceKind = domain.ResourceKind(resourceKind)
		if err := json.Unmarshal(outcome, &row.Outcome); err != nil {
			return nil, err
		}
		feedback = append(feedback, row)
	}
	return feedback, rows.Err()
}

func (r *PgFeedbackRepository) execTenant(
	ctx context.Context,
	tenantID string,
	fn func(context.Context, pgx.Tx) error,
) error {
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	return tenantdb.ExecTenant(ctx, r.pool, fn)
}
