package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PgOptimizationRepository struct {
	pool *pgxpool.Pool
}

func NewPgOptimizationRepository(pool *pgxpool.Pool) *PgOptimizationRepository {
	return &PgOptimizationRepository{pool: pool}
}

func (r *PgOptimizationRepository) WithinTransaction(
	ctx context.Context, tenantID string, fn func(context.Context) error,
) error {
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	return tenantdb.ExecTenant(ctx, r.pool, func(txCtx context.Context, _ pgx.Tx) error {
		return fn(txCtx)
	})
}

func (r *PgOptimizationRepository) GetByIdempotencyKey(
	ctx context.Context, tenantID, idempotencyKey string,
) (domain.OptimizationJob, []domain.OptimizationCandidate, string, bool, error) {
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	var job domain.OptimizationJob
	var candidates []domain.OptimizationCandidate
	var fingerprint, resourceKind, resourceID, searchJSON, rewriteJSON string
	found := false
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id, resource_kind, resource_id, baseline_revision_id,
			suite_revision_id, status, search_space, rewrite_config, request_fingerprint, created_at
			FROM optimization_jobs WHERE idempotency_key=$1`, idempotencyKey).Scan(
			&job.ID, &resourceKind, &resourceID, &job.Baseline.RevisionID, &job.SuiteRevisionID,
			&job.Status, &searchJSON, &rewriteJSON, &fingerprint, &job.CreatedAt,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("optimization repository: get idempotency key: %w", err)
		}
		found = true
		job.Baseline.Kind = domain.ResourceKind(resourceKind)
		job.Baseline.ResourceID = resourceID
		if err := json.Unmarshal([]byte(searchJSON), &job.SearchSpace); err != nil {
			return fmt.Errorf("optimization repository: decode search space: %w", err)
		}
		var rewrite struct {
			FailureSummaries []string `json:"failure_summaries"`
		}
		if err := json.Unmarshal([]byte(rewriteJSON), &rewrite); err != nil {
			return fmt.Errorf("optimization repository: decode rewrite config: %w", err)
		}
		job.FailureSummaries = rewrite.FailureSummaries
		rows, err := tx.Query(ctx, `SELECT id, revision_id, parent_revision_id, source, rationale,
			generation_metadata, COALESCE(eval_run_id,''), COALESCE(rank,0), created_at
			FROM optimization_candidates WHERE optimization_job_id=$1 ORDER BY created_at, id`, job.ID)
		if err != nil {
			return fmt.Errorf("optimization repository: list candidates: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			candidate := domain.OptimizationCandidate{OptimizationJobID: job.ID}
			var metadataJSON string
			if err := rows.Scan(&candidate.ID, &candidate.Revision.RevisionID, &candidate.ParentRevisionID,
				&candidate.Source, &candidate.Rationale, &metadataJSON, &candidate.EvalRunID,
				&candidate.Rank, &candidate.CreatedAt); err != nil {
				return err
			}
			candidate.Revision.Kind = job.Baseline.Kind
			candidate.Revision.ResourceID = job.Baseline.ResourceID
			if err := json.Unmarshal([]byte(metadataJSON), &candidate.GenerationMetadata); err != nil {
				return fmt.Errorf("optimization repository: decode candidate metadata: %w", err)
			}
			candidates = append(candidates, candidate)
		}
		return rows.Err()
	})
	return job, candidates, fingerprint, found, err
}

func (r *PgOptimizationRepository) SaveJobWithCandidates(
	ctx context.Context,
	tenantID string,
	job domain.OptimizationJob,
	candidates []domain.OptimizationCandidate,
	idempotencyKey, requestFingerprint string,
) (bool, error) {
	searchJSON, err := json.Marshal(job.SearchSpace)
	if err != nil {
		return false, fmt.Errorf("optimization repository: marshal search space: %w", err)
	}
	rewriteJSON, err := json.Marshal(map[string]any{"failure_summaries": job.FailureSummaries})
	if err != nil {
		return false, fmt.Errorf("optimization repository: marshal rewrite config: %w", err)
	}
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	created := false
	err = tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`INSERT INTO optimization_jobs
				 (id, resource_kind, resource_id, baseline_revision_id, suite_revision_id, status,
				  search_space, rewrite_config, created_at, completed_at, idempotency_key, request_fingerprint)
				 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NOW(),$10,$11)
				 ON CONFLICT (idempotency_key) WHERE idempotency_key <> '' DO NOTHING`,
			job.ID, string(job.Baseline.Kind), job.Baseline.ResourceID, job.Baseline.RevisionID,
			job.SuiteRevisionID, string(job.Status), string(searchJSON), string(rewriteJSON), job.CreatedAt,
			idempotencyKey, requestFingerprint,
		)
		if err != nil {
			return fmt.Errorf("optimization repository: insert job: %w", err)
		}
		if tag.RowsAffected() == 0 {
			var storedFingerprint string
			if err := tx.QueryRow(ctx,
				`SELECT request_fingerprint FROM optimization_jobs WHERE idempotency_key=$1`, idempotencyKey,
			).Scan(&storedFingerprint); err != nil {
				return fmt.Errorf("optimization repository: read idempotency conflict: %w", err)
			}
			if storedFingerprint != requestFingerprint {
				return domain.ErrOptimizationIdempotencyConflict
			}
			return nil
		}
		created = true
		for _, candidate := range candidates {
			metadataJSON, err := json.Marshal(candidate.GenerationMetadata)
			if err != nil {
				return fmt.Errorf("optimization repository: marshal candidate metadata: %w", err)
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO optimization_candidates
				 (id, optimization_job_id, revision_id, parent_revision_id, source, rationale,
				  generation_metadata, eval_run_id, rank, created_at)
				 VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,0),$10)`,
				candidate.ID, candidate.OptimizationJobID, candidate.Revision.RevisionID,
				candidate.ParentRevisionID, candidate.Source, candidate.Rationale, string(metadataJSON),
				candidate.EvalRunID, candidate.Rank, candidate.CreatedAt,
			); err != nil {
				return fmt.Errorf("optimization repository: insert candidate: %w", err)
			}
		}
		return nil
	})
	return created, err
}
