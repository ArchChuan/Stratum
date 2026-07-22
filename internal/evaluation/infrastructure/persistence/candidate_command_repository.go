package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PgCandidateCommandRepository struct{ pool *pgxpool.Pool }

func NewPgCandidateCommandRepository(pool *pgxpool.Pool) *PgCandidateCommandRepository {
	return &PgCandidateCommandRepository{pool: pool}
}

func (r *PgCandidateCommandRepository) Reject(
	ctx context.Context, tenantID, candidateID string, command domain.CandidateCommand,
) (domain.CandidateSummary, error) {
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	var result domain.CandidateSummary
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		var key, fingerprint string
		var version int64
		var parent, candidate []byte
		var parentExists bool
		err := tx.QueryRow(ctx, `SELECT c.id,j.resource_kind,j.resource_id,c.revision_id,c.parent_revision_id,
			c.source,c.status,c.rank,c.created_at,COALESCE(c.rejection_key,''),
			COALESCE(c.rejection_fingerprint,''),c.state_version,COALESCE(parent.safe_summary,'{}'::jsonb),
			parent.id IS NOT NULL,COALESCE(candidate.safe_summary,'{}'::jsonb)
			FROM optimization_candidates c JOIN optimization_jobs j ON j.id=c.optimization_job_id
			LEFT JOIN resource_revisions parent ON parent.resource_kind=j.resource_kind AND
				parent.resource_id=j.resource_id AND parent.id=c.parent_revision_id
			LEFT JOIN resource_revisions candidate ON candidate.resource_kind=j.resource_kind AND
				candidate.resource_id=j.resource_id AND candidate.id=c.revision_id
			WHERE c.id=$1 FOR UPDATE`, candidateID).Scan(
			&result.ID, &result.ResourceKind, &result.ResourceID, &result.RevisionID, &result.ParentRevisionID,
			&result.Source, &result.Status, &result.Rank, &result.CreatedAt, &key, &fingerprint, &version,
			&parent, &parentExists, &candidate,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrCandidateNotFound
		}
		if err != nil {
			return fmt.Errorf("candidate command repository: get candidate: %w", err)
		}
		result.SafeDiff = buildCandidateSafeDiff(parseSanitizedSafeSummary(parent),
			parseSanitizedSafeSummary(candidate), parentExists)
		if result.Status == "rejected" {
			result.StateVersion = version
			if key == command.IdempotencyKey && fingerprint == command.Fingerprint() {
				return nil
			}
			return domain.ErrCandidateCommandConflict
		}
		if result.Status != "proposed" {
			return domain.ErrCandidateCommandNotAllowed
		}
		if version != command.ExpectedStateVersion {
			return domain.ErrCandidateStateConflict
		}
		_, err = tx.Exec(ctx, `UPDATE optimization_candidates SET status='rejected',state_version=state_version+1,
			rejection_reason=$2,rejected_by=$3,rejection_key=$4,rejection_fingerprint=$5 WHERE id=$1`,
			candidateID, command.Reason, command.ActorID, command.IdempotencyKey, command.Fingerprint())
		if err == nil {
			result.Status = "rejected"
			result.StateVersion = version + 1
		}
		return err
	})
	return result, err
}
