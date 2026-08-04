package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PgOperationProposalRepo struct {
	pool poolIface
}

func NewPgOperationProposalRepo(pool *pgxpool.Pool) *PgOperationProposalRepo {
	return &PgOperationProposalRepo{pool: pool}
}

func (r *PgOperationProposalRepo) execTenant(
	ctx context.Context,
	fn func(context.Context, pgx.Tx) error,
) error {
	tenant, ok := tenantdb.FromContext(ctx)
	if !ok || tenant.TenantID == "" {
		return fmt.Errorf("operation_proposal_repo: missing tenant context")
	}
	return pgstore.ExecTenantWith(ctx, r.pool, tenant.TenantID, fn)
}

func (r *PgOperationProposalRepo) Insert(ctx context.Context, p domain.OperationProposal) error {
	summary, err := json.Marshal(p.PayloadSummary)
	if err != nil {
		return fmt.Errorf("marshal operation proposal payload summary: %w", err)
	}
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO operation_proposals
            (id, agent_id, target_agent_id, op_type, delegation, max_daily_cost_usd, max_daily_executions,
             fingerprint, payload_summary, status, proposer_id, reviewed_by, review_note,
             created_at, updated_at, resolved_at, expires_at)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10, $11, $12, $13, $14, $15, $16, $17)`,
			p.ID, p.AgentID, p.TargetAgentID, p.OpType, p.Delegation, p.MaxDailyCostUSD, p.MaxDailyExecutions,
			p.Fingerprint, string(summary), p.Status, p.ProposerID, p.ReviewedBy, p.ReviewNote,
			p.CreatedAt, p.UpdatedAt, p.ResolvedAt, p.ExpiresAt)
		if err != nil {
			var pgErr *pgconn.PgError
			// Concurrent duplicate submission hits the partial unique index on
			// open fingerprints; surface it as a duplicate, not a 500.
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return domain.ErrOperationProposalPending
			}
			return fmt.Errorf("insert operation proposal: %w", err)
		}
		return nil
	})
}

func (r *PgOperationProposalRepo) GetByID(ctx context.Context, tenantID, id string) (*domain.OperationProposal, error) {
	var proposal domain.OperationProposal
	var summary []byte
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, agent_id, target_agent_id, op_type, delegation,
            max_daily_cost_usd, max_daily_executions, fingerprint, payload_summary, status,
            proposer_id, reviewed_by, review_note, created_at, updated_at, resolved_at, expires_at
            FROM operation_proposals WHERE id = $1`, id).Scan(
			&proposal.ID, &proposal.AgentID, &proposal.TargetAgentID, &proposal.OpType, &proposal.Delegation,
			&proposal.MaxDailyCostUSD, &proposal.MaxDailyExecutions, &proposal.Fingerprint, &summary,
			&proposal.Status, &proposal.ProposerID, &proposal.ReviewedBy, &proposal.ReviewNote,
			&proposal.CreatedAt, &proposal.UpdatedAt, &proposal.ResolvedAt, &proposal.ExpiresAt)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrOperationProposalNotFound
		}
		return nil, fmt.Errorf("get operation proposal %s: %w", id, err)
	}
	proposal.PayloadSummary = append(json.RawMessage(nil), summary...)
	return &proposal, nil
}

func (r *PgOperationProposalRepo) ListPending(ctx context.Context, tenantID string) ([]domain.OperationProposal, error) {
	var proposals []domain.OperationProposal
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, agent_id, target_agent_id, op_type, delegation,
            max_daily_cost_usd, max_daily_executions, fingerprint, payload_summary, status,
            proposer_id, reviewed_by, review_note, created_at, updated_at, resolved_at, expires_at
            FROM operation_proposals
            WHERE status IN ('proposed','reviewing')
            ORDER BY created_at DESC`)
		if err != nil {
			return fmt.Errorf("list pending operation proposals: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var p domain.OperationProposal
			var summary []byte
			if err := rows.Scan(
				&p.ID, &p.AgentID, &p.TargetAgentID, &p.OpType, &p.Delegation,
				&p.MaxDailyCostUSD, &p.MaxDailyExecutions, &p.Fingerprint, &summary,
				&p.Status, &p.ProposerID, &p.ReviewedBy, &p.ReviewNote,
				&p.CreatedAt, &p.UpdatedAt, &p.ResolvedAt, &p.ExpiresAt); err != nil {
				return fmt.Errorf("scan pending operation proposal: %w", err)
			}
			p.PayloadSummary = append(json.RawMessage(nil), summary...)
			proposals = append(proposals, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return proposals, nil
}

func (r *PgOperationProposalRepo) UpdateStatus(
	ctx context.Context,
	tenantID, id string,
	status domain.OpProposalStatus,
	reviewerID, note string,
) error {
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE operation_proposals SET
            status = $3,
            reviewed_by = $4,
            review_note = $5,
            updated_at = NOW(),
            resolved_at = CASE WHEN $3 = 'rejected' THEN NOW() ELSE resolved_at END,
            expires_at = CASE WHEN $3 = 'approved' THEN NOW() + $6::interval ELSE expires_at END
            WHERE id = $1 AND status IN ('proposed','reviewing')`,
			id, tenantID, status, reviewerID, note, constants.OperationApprovalTTL.String())
		if err != nil {
			return fmt.Errorf("update operation proposal status: %w", err)
		}
		if tag.RowsAffected() > 0 {
			return nil
		}
		var current domain.OpProposalStatus
		err = tx.QueryRow(ctx, `SELECT status FROM operation_proposals WHERE id = $1`, id).Scan(&current)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrOperationProposalNotFound
		}
		if err != nil {
			return fmt.Errorf("re-read operation proposal %s: %w", id, err)
		}
		// A concurrent review already moved the proposal to a terminal state;
		// the conditional UPDATE makes the transition single-winner.
		return domain.ErrOperationProposalResolved
	})
}

func (r *PgOperationProposalRepo) HasPending(ctx context.Context, tenantID, fingerprint string) (bool, error) {
	var exists bool
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// An expired approval is not pending: it can never be consumed, so it
		// must not block a fresh proposal for the same content.
		return tx.QueryRow(ctx, `SELECT EXISTS(
            SELECT 1 FROM operation_proposals
            WHERE fingerprint = $1
              AND (status IN ('proposed','reviewing')
                   OR (status = 'approved' AND expires_at > NOW())))`, fingerprint).Scan(&exists)
	})
	if err != nil {
		return false, fmt.Errorf("check pending operation proposal: %w", err)
	}
	return exists, nil
}

func (r *PgOperationProposalRepo) ConsumeApproved(
	ctx context.Context,
	tenantID, fingerprint, proposerID string,
) (bool, error) {
	var consumed bool
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE operation_proposals SET
            status = 'executed',
            resolved_at = NOW(),
            updated_at = NOW()
            WHERE fingerprint = $1 AND proposer_id = $2
              AND status = 'approved' AND expires_at > NOW()`,
			fingerprint, proposerID)
		if err != nil {
			return fmt.Errorf("consume approved operation proposal: %w", err)
		}
		consumed = tag.RowsAffected() > 0
		return nil
	})
	if err != nil {
		return false, err
	}
	return consumed, nil
}
