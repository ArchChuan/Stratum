package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PgExperimentRepository struct {
	pool *pgxpool.Pool
}

func NewPgExperimentRepository(pool *pgxpool.Pool) *PgExperimentRepository {
	return &PgExperimentRepository{pool: pool}
}

func (r *PgExperimentRepository) Create(
	ctx context.Context,
	tenantID string,
	experiment domain.Experiment,
	deployment domain.Deployment,
) error {
	policyJSON, err := json.Marshal(experiment.Policy)
	if err != nil {
		return err
	}
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO evaluation_experiments
			 (id, resource_kind, resource_id, stable_revision_id, canary_revision_id,
			  suite_revision_id, status, stage_percent, policy, state_version, recommendation, safety_stopped)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			experiment.ID, string(experiment.ResourceKind), experiment.ResourceID,
			experiment.StableRevisionID, experiment.CanaryRevisionID, experiment.SuiteRevisionID,
			string(experiment.Status), experiment.Stage, string(policyJSON), experiment.StateVersion,
			string(experiment.Recommendation), experiment.SafetyStopped,
		); err != nil {
			return fmt.Errorf("experiment repository: insert experiment: %w", err)
		}
		result, err := tx.Exec(ctx,
			`INSERT INTO evaluation_deployments
			 (resource_kind, resource_id, stable_revision_id, canary_revision_id,
			  canary_percent, experiment_id, policy_version)
			 VALUES ($1,$2,$3,$4,$5,$6,$7)
			 ON CONFLICT (resource_kind, resource_id) DO UPDATE SET
			 stable_revision_id=EXCLUDED.stable_revision_id,
			 canary_revision_id=EXCLUDED.canary_revision_id,
			 canary_percent=EXCLUDED.canary_percent,
			 experiment_id=EXCLUDED.experiment_id,
			 policy_version=evaluation_deployments.policy_version+1,
			 updated_at=NOW()
			 WHERE evaluation_deployments.experiment_id IS NULL`,
			string(deployment.ResourceKind), deployment.ResourceID, deployment.StableRevisionID,
			deployment.CanaryRevisionID, deployment.CanaryPercent, deployment.ExperimentID, deployment.PolicyVersion,
		)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return domain.ErrExperimentDeploymentConflict
		}
		return nil
	})
}

func (r *PgExperimentRepository) Get(
	ctx context.Context,
	tenantID, experimentID string,
) (domain.Experiment, bool, error) {
	var experiment domain.Experiment
	found := false
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var kind, status string
		var policyJSON []byte
		err := tx.QueryRow(ctx,
			`SELECT id, resource_kind, resource_id, stable_revision_id, canary_revision_id,
			        suite_revision_id, status, stage_percent, policy, state_version, recommendation, safety_stopped
			 FROM evaluation_experiments WHERE id=$1`, experimentID,
		).Scan(&experiment.ID, &kind, &experiment.ResourceID, &experiment.StableRevisionID,
			&experiment.CanaryRevisionID, &experiment.SuiteRevisionID, &status, &experiment.Stage, &policyJSON,
			&experiment.StateVersion, &experiment.Recommendation, &experiment.SafetyStopped)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		experiment.ResourceKind = domain.ResourceKind(kind)
		experiment.Status = domain.ExperimentStatus(status)
		return json.Unmarshal(policyJSON, &experiment.Policy)
	})
	return experiment, found, err
}

func (r *PgExperimentRepository) SaveDecision(
	ctx context.Context,
	tenantID string,
	experiment domain.Experiment,
	decision domain.Decision,
	metrics domain.StageMetrics,
	idempotencyKey, fingerprint string,
) (domain.Experiment, domain.Decision, error) {
	snapshotJSON, err := json.Marshal(map[string]any{
		"decision": decision, "metrics": metrics, "fingerprint": fingerprint, "result": experiment,
	})
	if err != nil {
		return domain.Experiment{}, domain.DecisionHold, err
	}
	var stored domain.Experiment
	storedDecision := domain.DecisionHold
	err = r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		current, found, err := getExperimentTx(ctx, tx, experiment.ID, true)
		if err != nil {
			return err
		}
		if !found {
			return pgx.ErrNoRows
		}
		var priorSnapshot []byte
		err = tx.QueryRow(ctx, `SELECT metrics FROM experiment_decisions
			WHERE experiment_id=$1 AND idempotency_key=$2`, experiment.ID, idempotencyKey).Scan(&priorSnapshot)
		if err == nil {
			var prior struct {
				Fingerprint string            `json:"fingerprint"`
				Decision    domain.Decision   `json:"decision"`
				Result      domain.Experiment `json:"result"`
			}
			if err := json.Unmarshal(priorSnapshot, &prior); err != nil {
				return err
			}
			if prior.Fingerprint != fingerprint {
				return domain.ErrExperimentCommandConflict
			}
			stored, storedDecision = prior.Result, prior.Decision
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if current.Status != domain.ExperimentRunning || experiment.Status != domain.ExperimentRunning {
			return domain.ErrExperimentCommandNotAllowed
		}
		result, err := tx.Exec(ctx,
			`UPDATE evaluation_experiments
			 SET status=$2, stage_percent=$3, decision_snapshot=$4, recommendation=$5,
			     safety_stopped=$6, state_version=$7, updated_at=NOW()
			 WHERE id=$1 AND state_version=$8`, experiment.ID, string(experiment.Status), experiment.Stage,
			string(snapshotJSON), string(decision), experiment.SafetyStopped, experiment.StateVersion,
			experiment.StateVersion-1)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return domain.ErrExperimentStateConflict
		}
		deploymentResult, err := tx.Exec(ctx,
			`UPDATE evaluation_deployments SET canary_percent=$3, updated_at=NOW()
				 WHERE resource_kind=$1 AND resource_id=$2 AND experiment_id=$4`,
			string(experiment.ResourceKind), experiment.ResourceID, experiment.Stage, experiment.ID)
		if err != nil {
			return err
		}
		if deploymentResult.RowsAffected() != 1 {
			return domain.ErrExperimentStateConflict
		}
		action := "recommend"
		if experiment.SafetyStopped {
			action = "safety_stop"
		}
		_, err = tx.Exec(ctx, `INSERT INTO experiment_decisions
			(id, experiment_id, action, actor_type, actor_id, prior_status, new_status,
			 recommendation, metrics, reason, idempotency_key)
			VALUES ($1,$2,$3,'system','evaluation-service',$4,$5,$6,$7,$8,$9)`,
			uuid.Must(uuid.NewV7()).String(), experiment.ID, action, string(current.Status), string(experiment.Status),
			string(decision), string(snapshotJSON), "automated stage evaluation", idempotencyKey)
		if err == nil {
			stored, storedDecision = experiment, decision
		}
		return err
	})
	return stored, storedDecision, err
}

func (r *PgExperimentRepository) ApplyCommand(
	ctx context.Context,
	tenantID, experimentID string,
	action domain.ExperimentCommandAction,
	command domain.ExperimentCommand,
) (domain.Experiment, error) {
	var updated domain.Experiment
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		fingerprint := command.Fingerprint(action)
		current, found, err := getExperimentTx(ctx, tx, experimentID, true)
		if err != nil {
			return err
		}
		if !found {
			return pgx.ErrNoRows
		}
		var priorMetadata []byte
		err = tx.QueryRow(ctx, `SELECT metrics FROM experiment_decisions
			WHERE experiment_id=$1 AND idempotency_key=$2`, experimentID, command.IdempotencyKey).Scan(&priorMetadata)
		if err == nil {
			var prior struct {
				Fingerprint string            `json:"fingerprint"`
				Result      domain.Experiment `json:"result"`
			}
			if err := json.Unmarshal(priorMetadata, &prior); err != nil {
				return err
			}
			if prior.Fingerprint != fingerprint {
				return domain.ErrExperimentCommandConflict
			}
			updated = prior.Result
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if current.StateVersion != command.ExpectedStateVersion {
			return domain.ErrExperimentStateConflict
		}
		if !domain.CanApplyExperimentCommand(current.Status, action) ||
			(action == domain.CommandPromote && (current.Recommendation != domain.DecisionPromote || current.SafetyStopped)) {
			return domain.ErrExperimentCommandNotAllowed
		}

		newStatus := domain.ExperimentPaused
		switch action {
		case domain.CommandPromote:
			newStatus = domain.ExperimentCompleted
		case domain.CommandRollback:
			newStatus = domain.ExperimentRolledBack
		case domain.CommandPause:
		default:
			return domain.ErrExperimentCommandNotAllowed
		}
		newVersion := current.StateVersion + 1
		_, err = tx.Exec(ctx, `UPDATE evaluation_experiments
			SET status=$2, state_version=$3, recommendation='hold', updated_at=NOW(),
			completed_at=CASE WHEN $2 IN ('completed','rolled_back') THEN NOW() ELSE NULL END WHERE id=$1`,
			experimentID, string(newStatus), newVersion)
		if err != nil {
			return err
		}
		var deploymentResult pgconn.CommandTag
		switch action {
		case domain.CommandPause:
			deploymentResult, err = tx.Exec(ctx, `UPDATE evaluation_deployments SET canary_percent=0, updated_at=NOW()
				WHERE experiment_id=$1`, experimentID)
		case domain.CommandPromote:
			deploymentResult, err = tx.Exec(ctx, `UPDATE evaluation_deployments
				SET stable_revision_id=$2, canary_revision_id=NULL, canary_percent=0, experiment_id=NULL,
				policy_version=policy_version+1, updated_at=NOW() WHERE experiment_id=$1`,
				experimentID, current.CanaryRevisionID)
		case domain.CommandRollback:
			deploymentResult, err = tx.Exec(ctx, `UPDATE evaluation_deployments
				SET canary_revision_id=NULL, canary_percent=0, experiment_id=NULL, updated_at=NOW()
				WHERE experiment_id=$1`, experimentID)
		}
		if err != nil {
			return err
		}
		if deploymentResult.RowsAffected() != 1 {
			return domain.ErrExperimentStateConflict
		}
		resultSnapshot := current
		resultSnapshot.Status = newStatus
		resultSnapshot.StateVersion = newVersion
		resultSnapshot.Recommendation = domain.DecisionHold
		metadata, err := json.Marshal(map[string]any{"fingerprint": fingerprint, "result": resultSnapshot})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO experiment_decisions
			(id, experiment_id, action, actor_type, actor_id, prior_status, new_status,
			 recommendation, metrics, reason, idempotency_key)
			VALUES ($1,$2,$3,$4,$5,$6,$7,'hold',$8,$9,$10)`, uuid.Must(uuid.NewV7()).String(), experimentID,
			string(action), string(command.ActorType), command.ActorID, string(current.Status), string(newStatus),
			string(metadata), command.Reason, command.IdempotencyKey)
		if err != nil {
			return err
		}
		updated = resultSnapshot
		return nil
	})
	return updated, err
}

func getExperimentTx(ctx context.Context, tx pgx.Tx, experimentID string, lock bool) (domain.Experiment, bool, error) {
	query := `SELECT id, resource_kind, resource_id, stable_revision_id, canary_revision_id,
		suite_revision_id, status, stage_percent, policy, state_version, recommendation, safety_stopped
		FROM evaluation_experiments WHERE id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var experiment domain.Experiment
	var kind, status string
	var policyJSON []byte
	err := tx.QueryRow(ctx, query, experimentID).Scan(&experiment.ID, &kind, &experiment.ResourceID,
		&experiment.StableRevisionID, &experiment.CanaryRevisionID, &experiment.SuiteRevisionID, &status,
		&experiment.Stage, &policyJSON, &experiment.StateVersion, &experiment.Recommendation, &experiment.SafetyStopped)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Experiment{}, false, nil
	}
	if err != nil {
		return domain.Experiment{}, false, err
	}
	experiment.ResourceKind = domain.ResourceKind(kind)
	experiment.Status = domain.ExperimentStatus(status)
	if err := json.Unmarshal(policyJSON, &experiment.Policy); err != nil {
		return domain.Experiment{}, false, err
	}
	return experiment, true, nil
}

func (r *PgExperimentRepository) ResolveDeployment(
	ctx context.Context,
	tenantID, resourceKind, resourceID string,
) (domain.Deployment, bool, error) {
	var deployment domain.Deployment
	found := false
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var kind string
		err := tx.QueryRow(ctx,
			`SELECT resource_kind, resource_id, stable_revision_id, COALESCE(canary_revision_id, ''),
			        canary_percent, COALESCE(experiment_id, ''), policy_version
			 FROM evaluation_deployments WHERE resource_kind=$1 AND resource_id=$2`, resourceKind, resourceID,
		).Scan(&kind, &deployment.ResourceID, &deployment.StableRevisionID, &deployment.CanaryRevisionID,
			&deployment.CanaryPercent, &deployment.ExperimentID, &deployment.PolicyVersion)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		deployment.ResourceKind = domain.ResourceKind(kind)
		return nil
	})
	return deployment, found, err
}

func (r *PgExperimentRepository) execTenant(
	ctx context.Context,
	tenantID string,
	fn func(context.Context, pgx.Tx) error,
) error {
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	return tenantdb.ExecTenant(ctx, r.pool, fn)
}
