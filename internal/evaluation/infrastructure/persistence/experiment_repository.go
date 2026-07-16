package persistence

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
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
			  suite_revision_id, status, stage_percent, policy)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			experiment.ID, string(experiment.ResourceKind), experiment.ResourceID,
			experiment.StableRevisionID, experiment.CanaryRevisionID, experiment.SuiteRevisionID,
			string(experiment.Status), experiment.Stage, string(policyJSON),
		); err != nil {
			return fmt.Errorf("experiment repository: insert experiment: %w", err)
		}
		_, err := tx.Exec(ctx,
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
			 updated_at=NOW()`,
			string(deployment.ResourceKind), deployment.ResourceID, deployment.StableRevisionID,
			deployment.CanaryRevisionID, deployment.CanaryPercent, deployment.ExperimentID, deployment.PolicyVersion,
		)
		return err
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
			        suite_revision_id, status, stage_percent, policy
			 FROM evaluation_experiments WHERE id=$1`, experimentID,
		).Scan(&experiment.ID, &kind, &experiment.ResourceID, &experiment.StableRevisionID,
			&experiment.CanaryRevisionID, &experiment.SuiteRevisionID, &status, &experiment.Stage, &policyJSON)
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
) error {
	snapshotJSON, err := json.Marshal(map[string]any{"decision": decision, "metrics": metrics})
	if err != nil {
		return err
	}
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE evaluation_experiments
			 SET status=$2, stage_percent=$3, decision_snapshot=$4, updated_at=NOW(),
			     completed_at=CASE WHEN $2 IN ('completed','rolled_back') THEN NOW() ELSE NULL END
			 WHERE id=$1`, experiment.ID, string(experiment.Status), experiment.Stage, string(snapshotJSON)); err != nil {
			return err
		}
		switch experiment.Status {
		case domain.ExperimentRolledBack:
			_, err = tx.Exec(ctx,
				`UPDATE evaluation_deployments
				 SET canary_revision_id=NULL, canary_percent=0, experiment_id=NULL, updated_at=NOW()
				 WHERE resource_kind=$1 AND resource_id=$2`, string(experiment.ResourceKind), experiment.ResourceID)
		case domain.ExperimentCompleted:
			_, err = tx.Exec(ctx,
				`UPDATE evaluation_deployments
				 SET stable_revision_id=$3, canary_revision_id=NULL, canary_percent=0,
				     experiment_id=NULL, policy_version=policy_version+1, updated_at=NOW()
				 WHERE resource_kind=$1 AND resource_id=$2`,
				string(experiment.ResourceKind), experiment.ResourceID, experiment.CanaryRevisionID)
		case domain.ExperimentRunning:
			_, err = tx.Exec(ctx,
				`UPDATE evaluation_deployments SET canary_percent=$3, updated_at=NOW()
				 WHERE resource_kind=$1 AND resource_id=$2 AND experiment_id=$4`,
				string(experiment.ResourceKind), experiment.ResourceID, experiment.Stage, experiment.ID)
		}
		return err
	})
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
