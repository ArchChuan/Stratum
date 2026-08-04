package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PgExperimentRepository struct {
	pool poolIface
}

func NewPgExperimentRepository(pool *pgxpool.Pool) *PgExperimentRepository {
	return &PgExperimentRepository{pool: pool}
}

func (r *PgExperimentRepository) ValidatePrerequisites(
	ctx context.Context,
	tenantID string,
	stable, canary domain.ResourceRef,
	suiteRevisionID string,
) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT
			CASE WHEN $2 = 'skill' THEN EXISTS (
				SELECT 1 FROM skill_revisions WHERE id=$1 AND skill_id=$3 AND status='published'
			) ELSE EXISTS (
				SELECT 1 FROM resource_revisions
				WHERE id=$1 AND resource_kind=$2 AND resource_id=$3 AND status='published'
			) END`, stable.RevisionID, string(stable.Kind), stable.ResourceID).Scan(&exists); err != nil {
			return fmt.Errorf("experiment repository: validate stable revision: %w", err)
		}
		if !exists {
			return domain.ErrExperimentStableNotPublished
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM optimization_candidates c
			WHERE c.revision_id=$1 AND c.status='proposed' AND (
				($2 = 'skill' AND EXISTS (
					SELECT 1 FROM skill_revisions s
					WHERE s.id=c.revision_id AND s.skill_id=$3 AND s.status='candidate'
				)) OR ($2 <> 'skill' AND EXISTS (
					SELECT 1 FROM resource_revisions r
					WHERE r.id=c.revision_id AND r.resource_kind=$2 AND r.resource_id=$3
					  AND r.source='optimization' AND r.status='draft'
				))
			)
		)`, canary.RevisionID, string(canary.Kind), canary.ResourceID).Scan(&exists); err != nil {
			return fmt.Errorf("experiment repository: validate canary revision: %w", err)
		}
		if !exists {
			return domain.ErrExperimentInvalidCandidate
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM eval_suite_revisions
			WHERE id=$1 AND status='published' AND resource_kind=$2
		)`, suiteRevisionID, string(canary.Kind)).Scan(&exists); err != nil {
			return fmt.Errorf("experiment repository: validate suite revision: %w", err)
		}
		if !exists {
			return domain.ErrExperimentSuiteNotPublished
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM eval_runs
			WHERE resource_kind=$1 AND resource_id=$2 AND revision_id=$3
			  AND suite_revision_id=$4 AND status='succeeded' AND passed=true
		)`, string(canary.Kind), canary.ResourceID, canary.RevisionID, suiteRevisionID).Scan(&exists); err != nil {
			return fmt.Errorf("experiment repository: validate offline run: %w", err)
		}
		if !exists {
			return domain.ErrExperimentOfflineRunRequired
		}
		return nil
	})
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
			     safety_stopped=$6, state_version=$7,
			     stage_started_at=CASE WHEN stage_percent<>$3 THEN NOW() ELSE stage_started_at END,
			     updated_at=NOW()
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
		current, fingerprint, cached, err := r.loadAndCheckIdempotency(ctx, tx, experimentID, action, command)
		if err != nil {
			return err
		}
		if cached != nil {
			updated = *cached
			return nil
		}

		if current.StateVersion != command.ExpectedStateVersion {
			return domain.ErrExperimentStateConflict
		}
		if !domain.CanApplyExperimentCommand(current.Status, action) ||
			(action == domain.CommandPromote && (current.Recommendation != domain.DecisionPromote || current.SafetyStopped)) {
			return domain.ErrExperimentCommandNotAllowed
		}

		newStatus, err := experimentCommandTargetStatus(action)
		if err != nil {
			return err
		}
		newVersion := current.StateVersion + 1
		if err := r.updateExperimentAndDeployment(ctx, tx, experimentID, action, *current, newStatus, newVersion); err != nil {
			return err
		}

		resultSnapshot := *current
		resultSnapshot.Status = newStatus
		resultSnapshot.StateVersion = newVersion
		resultSnapshot.Recommendation = domain.DecisionHold
		if err := recordExperimentDecisionTx(ctx, tx, experimentID, action, command,
			current.Status, newStatus, fingerprint, resultSnapshot); err != nil {
			return err
		}
		updated = resultSnapshot
		return nil
	})
	return updated, err
}

// loadAndCheckIdempotency fetches the experiment and checks idempotency.
// Returns (current, fingerprint, cached, error). When cached is non-nil the
// caller must use it directly (idempotent replay); current will be nil.
func (r *PgExperimentRepository) loadAndCheckIdempotency(
	ctx context.Context, tx pgx.Tx,
	experimentID string,
	action domain.ExperimentCommandAction,
	command domain.ExperimentCommand,
) (*domain.Experiment, string, *domain.Experiment, error) {
	fingerprint := command.Fingerprint(action)
	current, found, err := getExperimentTx(ctx, tx, experimentID, true)
	if err != nil {
		return nil, fingerprint, nil, err
	}
	if !found {
		return nil, fingerprint, nil, pgx.ErrNoRows
	}

	var prior []byte
	err = tx.QueryRow(ctx, `SELECT metrics FROM experiment_decisions
		WHERE experiment_id=$1 AND idempotency_key=$2`, experimentID, command.IdempotencyKey).Scan(&prior)
	if errors.Is(err, pgx.ErrNoRows) {
		return &current, fingerprint, nil, nil
	}
	if err != nil {
		return nil, fingerprint, nil, err
	}
	var cached struct {
		Fingerprint string            `json:"fingerprint"`
		Result      domain.Experiment `json:"result"`
	}
	if err := json.Unmarshal(prior, &cached); err != nil {
		return nil, fingerprint, nil, err
	}
	if cached.Fingerprint != fingerprint {
		return nil, fingerprint, nil, domain.ErrExperimentCommandConflict
	}
	return nil, fingerprint, &cached.Result, nil
}

// experimentCommandTargetStatus maps an action to its target experiment status.
func experimentCommandTargetStatus(action domain.ExperimentCommandAction) (domain.ExperimentStatus, error) {
	switch action {
	case domain.CommandActivate:
		return domain.ExperimentRunning, nil
	case domain.CommandReject:
		return domain.ExperimentRejected, nil
	case domain.CommandPause:
		return domain.ExperimentPaused, nil
	case domain.CommandPromote:
		return domain.ExperimentCompleted, nil
	case domain.CommandRollback:
		return domain.ExperimentRolledBack, nil
	default:
		return "", domain.ErrExperimentCommandNotAllowed
	}
}

// updateExperimentAndDeployment updates the experiment row and (when applicable)
// the linked deployment row inside the same transaction.
func (r *PgExperimentRepository) updateExperimentAndDeployment(
	ctx context.Context, tx pgx.Tx,
	experimentID string,
	action domain.ExperimentCommandAction,
	current domain.Experiment,
	newStatus domain.ExperimentStatus,
	newVersion int64,
) error {
	_, err := tx.Exec(ctx, `UPDATE evaluation_experiments
		SET status=$2, state_version=$3, recommendation='hold', updated_at=NOW(),
		stage_started_at=CASE WHEN $2='running' THEN NOW() ELSE stage_started_at END,
		completed_at=CASE WHEN $2 IN ('completed','rolled_back','rejected') THEN NOW() ELSE NULL END
		WHERE id=$1`, experimentID, string(newStatus), newVersion)
	if err != nil {
		return err
	}

	switch action {
	case domain.CommandPause:
		return r.applyDeploymentUpdate(ctx, tx, experimentID, `UPDATE evaluation_deployments
			SET canary_percent=0, updated_at=NOW() WHERE experiment_id=$1`)
	case domain.CommandPromote:
		if err := promoteCandidateTx(ctx, tx, current); err != nil {
			return err
		}
		return r.applyDeploymentUpdate(ctx, tx, experimentID,
			`UPDATE evaluation_deployments
			 SET stable_revision_id=$2, canary_revision_id=NULL, canary_percent=0,
			 experiment_id=NULL, policy_version=policy_version+1, updated_at=NOW()
			 WHERE experiment_id=$1`, current.CanaryRevisionID)
	case domain.CommandRollback:
		return r.applyDeploymentUpdate(ctx, tx, experimentID, `UPDATE evaluation_deployments
			SET canary_revision_id=NULL, canary_percent=0, experiment_id=NULL, updated_at=NOW()
			WHERE experiment_id=$1`)
	default:
		return nil // Activate / Reject: no deployment change.
	}
}

func (r *PgExperimentRepository) applyDeploymentUpdate(
	ctx context.Context, tx pgx.Tx, experimentID, query string, args ...any,
) error {
	args = append([]any{experimentID}, args...)
	result, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return domain.ErrExperimentStateConflict
	}
	return nil
}

func recordExperimentDecisionTx(
	ctx context.Context, tx pgx.Tx,
	experimentID string,
	action domain.ExperimentCommandAction,
	command domain.ExperimentCommand,
	priorStatus domain.ExperimentStatus,
	newStatus domain.ExperimentStatus,
	fingerprint string,
	resultSnapshot domain.Experiment,
) error {
	metadata, err := json.Marshal(map[string]any{"fingerprint": fingerprint, "result": resultSnapshot})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO experiment_decisions
		(id, experiment_id, action, actor_type, actor_id, prior_status, new_status,
		 recommendation, metrics, reason, idempotency_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'hold',$8,$9,$10)`,
		uuid.Must(uuid.NewV7()).String(), experimentID,
		string(action), string(command.ActorType), command.ActorID,
		string(priorStatus), string(newStatus),
		string(metadata), command.Reason, command.IdempotencyKey)
	return err
}

func promoteCandidateTx(ctx context.Context, tx pgx.Tx, experiment domain.Experiment) error {
	var revisionResult pgconn.CommandTag
	var err error
	if experiment.ResourceKind == domain.ResourceKindSkill {
		if _, err = tx.Exec(ctx, `UPDATE skill_revisions SET status='deprecated',updated_at=NOW()
			WHERE skill_id=$1 AND status='published'`, experiment.ResourceID); err != nil {
			return fmt.Errorf("experiment repository: deprecate stable Skill revision: %w", err)
		}
		revisionResult, err = tx.Exec(ctx, `UPDATE skill_revisions
			SET status='published',revision_no=(SELECT COALESCE(MAX(revision_no),0)+1 FROM skill_revisions WHERE skill_id=$1),
			    published_at=NOW(),updated_at=NOW()
			WHERE id=$2 AND skill_id=$1 AND status='candidate'`, experiment.ResourceID, experiment.CanaryRevisionID)
		if err == nil {
			var skillResult pgconn.CommandTag
			skillResult, err = tx.Exec(ctx, `UPDATE skills SET status='published',active_revision_id=$2,draft_revision_id=NULL,
				updated_at=NOW() WHERE id=$1`, experiment.ResourceID, experiment.CanaryRevisionID)
			if err == nil && skillResult.RowsAffected() != 1 {
				return domain.ErrExperimentInvalidCandidate
			}
		}
	} else {
		revisionResult, err = tx.Exec(ctx, `UPDATE resource_revisions SET status='published',published_at=NOW(),updated_at=NOW()
			WHERE id=$1 AND resource_kind=$2 AND resource_id=$3 AND source='optimization' AND status='draft'`,
			experiment.CanaryRevisionID, string(experiment.ResourceKind), experiment.ResourceID)
	}
	if err != nil {
		return fmt.Errorf("experiment repository: publish canary revision: %w", err)
	}
	if revisionResult.RowsAffected() != 1 {
		return domain.ErrExperimentInvalidCandidate
	}
	candidateResult, err := tx.Exec(ctx, `UPDATE optimization_candidates c SET status='promoted',state_version=state_version+1
		FROM optimization_jobs j WHERE c.optimization_job_id=j.id AND c.revision_id=$1 AND c.status='proposed'
		AND j.resource_kind=$2 AND j.resource_id=$3`, experiment.CanaryRevisionID, string(experiment.ResourceKind),
		experiment.ResourceID)
	if err != nil {
		return fmt.Errorf("experiment repository: consume promoted candidate: %w", err)
	}
	if candidateResult.RowsAffected() != 1 {
		return domain.ErrExperimentInvalidCandidate
	}
	return nil
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

func (r *PgExperimentRepository) HasRunningExperiment(
	ctx context.Context,
	tenantID, resourceKind, resourceID string,
) (bool, error) {
	var active bool
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM evaluation_experiments
			WHERE resource_kind=$1 AND resource_id=$2 AND status IN ('running','paused')
		)`, resourceKind, resourceID).Scan(&active)
	})
	return active, err
}

func (r *PgExperimentRepository) ListPendingExperiments(
	ctx context.Context,
	tenantID, resourceKind, resourceID string,
) ([]domain.Experiment, error) {
	var experiments []domain.Experiment
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		query := `SELECT id, resource_kind, resource_id, stable_revision_id, canary_revision_id,
			suite_revision_id, status, stage_percent, policy, state_version, recommendation, safety_stopped
			FROM evaluation_experiments WHERE status='pending'`
		args := []any{}
		if resourceKind != "" {
			args = append(args, resourceKind)
			query += fmt.Sprintf(" AND resource_kind=$%d", len(args))
		}
		if resourceID != "" {
			args = append(args, resourceID)
			query += fmt.Sprintf(" AND resource_id=$%d", len(args))
		}
		query += ` ORDER BY created_at ASC`
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			exp, err := scanExperiment(rows)
			if err != nil {
				return err
			}
			experiments = append(experiments, exp)
		}
		return rows.Err()
	})
	return experiments, err
}

func (r *PgExperimentRepository) ListRunningExperiments(
	ctx context.Context,
	tenantID string,
) ([]domain.Experiment, error) {
	var experiments []domain.Experiment
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, resource_kind, resource_id, stable_revision_id, canary_revision_id,
			suite_revision_id, status, stage_percent, policy, state_version, recommendation, safety_stopped
			FROM evaluation_experiments WHERE status='running' ORDER BY created_at ASC`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			exp, err := scanExperiment(rows)
			if err != nil {
				return err
			}
			experiments = append(experiments, exp)
		}
		return rows.Err()
	})
	return experiments, err
}

func scanExperiment(row pgx.Row) (domain.Experiment, error) {
	var exp domain.Experiment
	var kind, status string
	var policyJSON []byte
	err := row.Scan(&exp.ID, &kind, &exp.ResourceID, &exp.StableRevisionID, &exp.CanaryRevisionID,
		&exp.SuiteRevisionID, &status, &exp.Stage, &policyJSON, &exp.StateVersion,
		&exp.Recommendation, &exp.SafetyStopped)
	if err != nil {
		return domain.Experiment{}, err
	}
	exp.ResourceKind = domain.ResourceKind(kind)
	exp.Status = domain.ExperimentStatus(status)
	if err := json.Unmarshal(policyJSON, &exp.Policy); err != nil {
		return domain.Experiment{}, err
	}
	return exp, nil
}

func (r *PgExperimentRepository) execTenant(
	ctx context.Context,
	tenantID string,
	fn func(context.Context, pgx.Tx) error,
) error {
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	return execTenantTx(ctx, r.pool, tenantID, fn)
}
