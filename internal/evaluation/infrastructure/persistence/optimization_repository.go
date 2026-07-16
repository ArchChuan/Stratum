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

type PgOptimizationRepository struct {
	pool *pgxpool.Pool
}

func NewPgOptimizationRepository(pool *pgxpool.Pool) *PgOptimizationRepository {
	return &PgOptimizationRepository{pool: pool}
}

func (r *PgOptimizationRepository) SaveJobWithCandidates(
	ctx context.Context,
	tenantID string,
	job domain.OptimizationJob,
	candidates []domain.OptimizationCandidate,
) error {
	searchJSON, err := json.Marshal(job.SearchSpace)
	if err != nil {
		return fmt.Errorf("optimization repository: marshal search space: %w", err)
	}
	rewriteJSON, err := json.Marshal(map[string]any{"failure_summaries": job.FailureSummaries})
	if err != nil {
		return fmt.Errorf("optimization repository: marshal rewrite config: %w", err)
	}
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	return tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO optimization_jobs
			 (id, resource_kind, resource_id, baseline_revision_id, suite_revision_id, status,
			  search_space, rewrite_config, created_at, completed_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NOW())`,
			job.ID, string(job.Baseline.Kind), job.Baseline.ResourceID, job.Baseline.RevisionID,
			job.SuiteRevisionID, string(job.Status), string(searchJSON), string(rewriteJSON), job.CreatedAt,
		); err != nil {
			return fmt.Errorf("optimization repository: insert job: %w", err)
		}
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
}
