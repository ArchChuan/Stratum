package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PgFeedbackRepository struct {
	pool poolIface
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
		outcomeJSON, err := json.Marshal(domain.SanitizeValue(outcome))
		if err != nil {
			return err
		}
		var kind string
		var storedOutcome []byte
		feedback.ID = uuid.Must(uuid.NewV7()).String()
		row := tx.QueryRow(ctx,
			`INSERT INTO evaluation_feedback
			 (id, trace_id, resource_kind, resource_id, revision_id, experiment_id, variant,
			  score, outcome, idempotency_key, created_by)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			 ON CONFLICT DO NOTHING
			 RETURNING id, trace_id, resource_kind, resource_id, revision_id, experiment_id, variant,
			           score, outcome, idempotency_key, created_by, created_at`,
			feedback.ID, input.TraceID, string(input.ResourceKind), input.ResourceID, input.RevisionID,
			input.ExperimentID, input.Variant, input.Score, string(outcomeJSON), input.IdempotencyKey, input.ActorID,
		)
		err = row.Scan(&feedback.ID, &feedback.TraceID, &kind, &feedback.ResourceID, &feedback.RevisionID,
			&feedback.ExperimentID, &feedback.Variant,
			&feedback.Score, &storedOutcome, &feedback.IdempotencyKey, &feedback.CreatedBy, &feedback.CreatedAt)
		if err == pgx.ErrNoRows {
			var matches, exactMatches int
			err = tx.QueryRow(ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE
				trace_id=$1 AND resource_kind=$2 AND resource_id=$3 AND revision_id=$4
				AND COALESCE(experiment_id,'')=$5 AND COALESCE(variant,'')=$6
				AND score IS NOT DISTINCT FROM $7 AND outcome=$8::jsonb AND idempotency_key=$9)
				FROM evaluation_feedback
				WHERE idempotency_key=$9 OR (trace_id=$1 AND resource_id=$3)`,
				input.TraceID, string(input.ResourceKind), input.ResourceID, input.RevisionID,
				input.ExperimentID, input.Variant, input.Score, string(outcomeJSON), input.IdempotencyKey,
			).Scan(&matches, &exactMatches)
			if err != nil {
				return err
			}
			if matches != 1 || exactMatches != 1 {
				return domain.ErrFeedbackIdempotencyConflict
			}
			err = tx.QueryRow(ctx, `SELECT id, trace_id, resource_kind, resource_id, revision_id,
				COALESCE(experiment_id,''), COALESCE(variant,''), score, outcome, idempotency_key, created_by, created_at
				FROM evaluation_feedback WHERE idempotency_key=$1`, input.IdempotencyKey,
			).Scan(&feedback.ID, &feedback.TraceID, &kind, &feedback.ResourceID, &feedback.RevisionID,
				&feedback.ExperimentID, &feedback.Variant, &feedback.Score, &storedOutcome,
				&feedback.IdempotencyKey, &feedback.CreatedBy, &feedback.CreatedAt)
		}
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
	return (&PgExperimentRepository{pool: r.pool}).Get(ctx, tenantID, experimentID)
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
		if err := tx.QueryRow(ctx,
			`SELECT stage_started_at FROM evaluation_experiments WHERE id=$1`, experiment.ID,
		).Scan(&stageStartedAt); err != nil {
			return err
		}
		observedMinutes = int(time.Since(stageStartedAt).Minutes())
		var err error
		feedback, err = loadStageFeedback(ctx, tx, experiment, stageStartedAt)
		return err
	})
	return feedback, observedMinutes, err
}

func loadStageFeedback(
	ctx context.Context,
	tx pgx.Tx,
	experiment domain.Experiment,
	stageStartedAt time.Time,
) ([]domain.EvaluationFeedback, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, trace_id, resource_kind, resource_id, revision_id, experiment_id, variant,
		        score, outcome, idempotency_key, created_by, created_at
		 FROM evaluation_feedback
		 WHERE resource_kind=$1 AND resource_id=$2 AND experiment_id=$3 AND created_at >= $4
		   AND ((revision_id=$5 AND variant='stable') OR (revision_id=$6 AND variant='canary'))
		 ORDER BY created_at`, string(experiment.ResourceKind), experiment.ResourceID, experiment.ID, stageStartedAt,
		experiment.StableRevisionID, experiment.CanaryRevisionID)
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
			&row.ExperimentID, &row.Variant,
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
	return execTenantTx(ctx, r.pool, tenantID, fn)
}

// GetFeedbackCreatedBy 返回反馈创建者；未命中 found=false。
func (r *PgFeedbackRepository) GetFeedbackCreatedBy(ctx context.Context, tenantID, feedbackID string) (string, bool, error) {
	var createdBy string
	found := false
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT created_by FROM evaluation_feedback WHERE id=$1`, feedbackID).Scan(&createdBy)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("evaluation feedback repository: load created by: %w", err)
		}
		found = true
		return nil
	})
	return createdBy, found, err
}

// DeleteFeedback 删除反馈：无入站引用，直接删除并写审计。
func (r *PgFeedbackRepository) DeleteFeedback(
	ctx context.Context, tenantID, feedbackID string, audit *auditdomain.ResourceChangeAuditEvent,
) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM evaluation_feedback WHERE id=$1`, feedbackID)
		if err != nil {
			return translateEntityReferenced(fmt.Errorf("evaluation feedback repository: delete feedback: %w", err))
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("evaluation feedback repository: delete feedback %s: not found", feedbackID)
		}
		return insertChangeAudit(ctx, tx, audit)
	})
}
