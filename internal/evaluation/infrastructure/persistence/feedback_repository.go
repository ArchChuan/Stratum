package persistence

import (
	"context"
	"encoding/json"
	"fmt"
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
		var revisionID string
		if err := tx.QueryRow(ctx,
			`SELECT metadata_json->>'version_id'
			 FROM agent_tool_traces
			 WHERE trace_id=$1 AND provider_type='skill' AND provider_id=$2
			       AND COALESCE(metadata_json->>'version_id','') <> ''
			 ORDER BY created_at DESC LIMIT 1`, input.TraceID, input.ResourceID,
		).Scan(&revisionID); err != nil {
			return fmt.Errorf("feedback repository: trace revision not found: %w", err)
		}
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
			feedback.ID, input.TraceID, string(input.ResourceKind), input.ResourceID, revisionID,
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

func (r *PgFeedbackRepository) Observations(
	ctx context.Context,
	tenantID string,
	experiment domain.Experiment,
) ([]domain.OnlineObservation, []domain.OnlineObservation, int, error) {
	var stable, canary []domain.OnlineObservation
	observedMinutes := 0
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var stageStartedAt time.Time
		if err := tx.QueryRow(ctx, `SELECT updated_at FROM evaluation_experiments WHERE id=$1`, experiment.ID).Scan(&stageStartedAt); err != nil {
			return err
		}
		observedMinutes = int(time.Since(stageStartedAt).Minutes())
		var err error
		stable, err = loadObservations(ctx, tx, experiment.ResourceID, experiment.StableRevisionID, stageStartedAt)
		if err != nil {
			return err
		}
		canary, err = loadObservations(ctx, tx, experiment.ResourceID, experiment.CanaryRevisionID, stageStartedAt)
		return err
	})
	return stable, canary, observedMinutes, err
}

func loadObservations(
	ctx context.Context,
	tx pgx.Tx,
	resourceID, revisionID string,
	stageStartedAt time.Time,
) ([]domain.OnlineObservation, error) {
	rows, err := tx.Query(ctx,
		`SELECT f.score,
		        COALESCE((SELECT SUM(e.cost_usd) FROM agent_trace_events e WHERE e.trace_id=f.trace_id), 0),
		        t.latency_ms, t.status,
		        COALESCE((f.outcome->>'security_violation')::boolean, false)
		 FROM evaluation_feedback f
		 JOIN LATERAL (
		   SELECT latency_ms, status
		   FROM agent_tool_traces
		   WHERE trace_id=f.trace_id AND provider_type='skill' AND provider_id=f.resource_id
		         AND metadata_json->>'version_id'=f.revision_id
		   ORDER BY created_at DESC LIMIT 1
		 ) t ON true
		 WHERE f.resource_id=$1 AND f.revision_id=$2 AND f.created_at >= $3
		 ORDER BY f.created_at`, resourceID, revisionID, stageStartedAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var observations []domain.OnlineObservation
	for rows.Next() {
		var observation domain.OnlineObservation
		var status string
		if err := rows.Scan(
			&observation.Score, &observation.CostUSD, &observation.LatencyMs, &status,
			&observation.SecurityViolation,
		); err != nil {
			return nil, err
		}
		observation.Success = status == "success"
		observations = append(observations, observation)
	}
	return observations, rows.Err()
}

func (r *PgFeedbackRepository) execTenant(
	ctx context.Context,
	tenantID string,
	fn func(context.Context, pgx.Tx) error,
) error {
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	return tenantdb.ExecTenant(ctx, r.pool, fn)
}
