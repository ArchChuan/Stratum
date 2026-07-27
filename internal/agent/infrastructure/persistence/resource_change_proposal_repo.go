package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PgResourceChangeProposalRepo struct {
	pool poolIface
}

func NewPgResourceChangeProposalRepo(pool *pgxpool.Pool) *PgResourceChangeProposalRepo {
	return &PgResourceChangeProposalRepo{pool: pool}
}

func (r *PgResourceChangeProposalRepo) execTenant(
	ctx context.Context,
	fn func(context.Context, pgx.Tx) error,
) error {
	tenant, ok := tenantdb.FromContext(ctx)
	if !ok || tenant.TenantID == "" {
		return fmt.Errorf("resource_change_proposal_repo: missing tenant context")
	}
	return pgstore.ExecTenantWith(ctx, r.pool, tenant.TenantID, fn)
}

func (r *PgResourceChangeProposalRepo) Create(
	ctx context.Context,
	proposal domain.ResourceChangeProposal,
	event domain.ProposalEvent,
) error {
	tenant, ok := tenantdb.FromContext(ctx)
	if !ok || proposal.TenantID != tenant.TenantID {
		return domain.ErrProposalForbidden
	}
	payload, err := json.Marshal(proposal.Payload)
	if err != nil {
		return fmt.Errorf("marshal proposal payload: %w", err)
	}
	summary, err := json.Marshal(map[string]string{"text": proposal.Summary})
	if err != nil {
		return fmt.Errorf("marshal proposal summary: %w", err)
	}
	result, err := json.Marshal(proposal.ApplyResult)
	if err != nil {
		return fmt.Errorf("marshal proposal result: %w", err)
	}
	baselineProjection := proposalBaselineProjectionJSON(proposal.BaselineProjection)
	detail, err := marshalProposalEventDetail(event)
	if err != nil {
		return err
	}
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO resource_change_proposals
            (id, conversation_id, proposer_id, confirmer_id, resource_kind, resource_id, operation,
             baseline_fingerprint, baseline_projection, payload, safe_summary, status, result, error_code, created_at, updated_at,
             confirmed_at, applied_at, expires_at, edit_count)
            VALUES ($1, NULLIF($2, '')::uuid, $3, $4, $5, $6, $7, $8, $9::jsonb, $10::jsonb, $11::jsonb, $12,
                    $13::jsonb, $14, $15, $16, $17, $18, $19, $20)`,
			proposal.ID, proposal.ConversationID, proposal.ProposerID, proposal.ConfirmerID,
			proposal.ResourceKind, proposal.ResourceID, proposal.Operation, proposal.BaselineFingerprint,
			string(baselineProjection), string(payload), string(summary), proposal.Status, string(result), proposal.ErrorCode,
			proposal.CreatedAt, proposal.UpdatedAt, proposal.ConfirmedAt, proposal.AppliedAt, proposal.ExpiresAt,
			proposal.EditCount)
		if err != nil {
			return fmt.Errorf("insert resource change proposal: %w", err)
		}
		return insertProposalEvent(ctx, tx, proposal.ID, event, detail)
	})
}

func (r *PgResourceChangeProposalRepo) Get(ctx context.Context, id string) (domain.ResourceChangeProposal, error) {
	var proposal domain.ResourceChangeProposal
	tenant, _ := tenantdb.FromContext(ctx)
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var baselineProjection, payload, summary, result []byte
		err := scanProposal(tx.QueryRow(ctx, proposalSelect+` WHERE id=$1`, id), &proposal, &baselineProjection, &payload, &summary, &result)
		if err != nil {
			return err
		}
		return decodeProposalJSON(&proposal, baselineProjection, payload, summary, result)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ResourceChangeProposal{}, domain.ErrProposalNotFound
	}
	if err != nil {
		return domain.ResourceChangeProposal{}, fmt.Errorf("get resource change proposal: %w", err)
	}
	proposal.TenantID = tenant.TenantID
	return proposal, nil
}

func (r *PgResourceChangeProposalRepo) UpdateDraft(
	ctx context.Context,
	proposal domain.ResourceChangeProposal,
	event domain.ProposalEvent,
) error {
	payload, err := json.Marshal(proposal.Payload)
	if err != nil {
		return fmt.Errorf("marshal proposal payload: %w", err)
	}
	summary, err := json.Marshal(map[string]string{"text": proposal.Summary})
	if err != nil {
		return fmt.Errorf("marshal proposal summary: %w", err)
	}
	detail, err := marshalProposalEventDetail(event)
	if err != nil {
		return err
	}
	baselineProjection := proposalBaselineProjectionJSON(proposal.BaselineProjection)
	var transitionErr error
	err = r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		command, err := tx.Exec(ctx, `UPDATE resource_change_proposals
			SET resource_id=$2, baseline_fingerprint=$3, baseline_projection=$4::jsonb, payload=$5::jsonb,
				safe_summary=$6::jsonb, status=$7, updated_at=$8, edit_count=edit_count+1
			WHERE id=$1 AND status IN ('draft','ready_for_review') AND expires_at>$8`, proposal.ID, proposal.ResourceID,
			proposal.BaselineFingerprint, string(baselineProjection), string(payload), string(summary), proposal.Status,
			proposal.UpdatedAt)
		if err != nil {
			return fmt.Errorf("update proposal draft: %w", err)
		}
		if command.RowsAffected() != 1 {
			transitionErr = classifyTransitionFailure(ctx, tx, proposal.ID, proposal.UpdatedAt)
			if errors.Is(transitionErr, domain.ErrProposalExpired) {
				return nil
			}
			return transitionErr
		}
		return insertProposalEvent(ctx, tx, proposal.ID, event, detail)
	})
	if err != nil {
		return err
	}
	return transitionErr
}

func (r *PgResourceChangeProposalRepo) Cancel(ctx context.Context, id, actor string, at time.Time) error {
	return r.transition(ctx, id, actor, at, domain.StatusCancelled,
		"status IN ('draft','ready_for_review') AND expires_at>$3")
}

func (r *PgResourceChangeProposalRepo) Confirm(ctx context.Context, id, actor string, at time.Time) error {
	var transitionErr error
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		command, err := tx.Exec(ctx, `UPDATE resource_change_proposals
            SET status='confirmed', confirmer_id=$2, confirmed_at=$3, updated_at=$3
            WHERE id=$1 AND status='ready_for_review' AND expires_at>$3`, id, actor, at)
		if err != nil {
			return fmt.Errorf("confirm proposal: %w", err)
		}
		if command.RowsAffected() != 1 {
			transitionErr = classifyTransitionFailure(ctx, tx, id, at)
			if errors.Is(transitionErr, domain.ErrProposalExpired) {
				return nil
			}
			return transitionErr
		}
		return insertProposalEvent(ctx, tx, id, domain.ProposalEvent{
			ActorID: actor, FromStatus: domain.StatusReadyForReview, ToStatus: domain.StatusConfirmed, CreatedAt: at,
		}, []byte(`{}`))
	})
	if err != nil {
		return err
	}
	return transitionErr
}

func (r *PgResourceChangeProposalRepo) ClaimApplying(
	ctx context.Context,
	id, actor string,
	at time.Time,
) (domain.ResourceChangeProposal, error) {
	var proposal domain.ResourceChangeProposal
	var transitionErr error
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var baselineProjection, payload, summary, result []byte
		err := scanProposal(tx.QueryRow(ctx, `UPDATE resource_change_proposals
            SET status='applying', updated_at=$2
            WHERE id=$1 AND status='confirmed' AND expires_at>$2
			RETURNING `+proposalColumns, id, at), &proposal, &baselineProjection, &payload, &summary, &result)
		if errors.Is(err, pgx.ErrNoRows) {
			transitionErr = classifyTransitionFailure(ctx, tx, id, at)
			if errors.Is(transitionErr, domain.ErrProposalExpired) {
				return nil
			}
			return transitionErr
		}
		if err != nil {
			return fmt.Errorf("claim proposal: %w", err)
		}
		if err := decodeProposalJSON(&proposal, baselineProjection, payload, summary, result); err != nil {
			return err
		}
		return insertProposalEvent(ctx, tx, id, domain.ProposalEvent{
			ActorID: actor, FromStatus: domain.StatusConfirmed, ToStatus: domain.StatusApplying, CreatedAt: at,
		}, []byte(`{}`))
	})
	if err != nil {
		return domain.ResourceChangeProposal{}, err
	}
	if transitionErr != nil {
		return domain.ResourceChangeProposal{}, transitionErr
	}
	tenant, _ := tenantdb.FromContext(ctx)
	proposal.TenantID = tenant.TenantID
	return proposal, nil
}

func (r *PgResourceChangeProposalRepo) Finish(
	ctx context.Context,
	id string,
	status domain.ProposalStatus,
	result domain.ApplyResult,
	event domain.ProposalEvent,
) error {
	if !domain.CanTransition(domain.StatusApplying, status) {
		return domain.ErrProposalInvalid
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal proposal result: %w", err)
	}
	detail, err := marshalProposalEventDetail(event)
	if err != nil {
		return err
	}
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		command, err := tx.Exec(ctx, `UPDATE resource_change_proposals
            SET status=$2, result=$3::jsonb, error_code=$4, applied_at=CASE WHEN $2='applied' THEN $5 ELSE applied_at END,
                updated_at=$5 WHERE id=$1 AND status='applying'`, id, status, string(resultJSON), event.Code, event.CreatedAt)
		if err != nil {
			return fmt.Errorf("finish proposal: %w", err)
		}
		if command.RowsAffected() != 1 {
			return domain.ErrProposalAlreadyClaimed
		}
		return insertProposalEvent(ctx, tx, id, event, detail)
	})
}

func (r *PgResourceChangeProposalRepo) ListEvents(ctx context.Context, id string) ([]domain.ProposalEvent, error) {
	events := []domain.ProposalEvent{}
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id::text, proposal_id, actor_id, from_status, to_status, detail, created_at
            FROM resource_change_proposal_events WHERE proposal_id=$1 ORDER BY created_at, id`, id)
		if err != nil {
			return fmt.Errorf("list proposal events: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var event domain.ProposalEvent
			var detail []byte
			if err := rows.Scan(&event.ID, &event.ProposalID, &event.ActorID, &event.FromStatus,
				&event.ToStatus, &detail, &event.CreatedAt); err != nil {
				return fmt.Errorf("scan proposal event: %w", err)
			}
			if err := json.Unmarshal(detail, &event); err != nil {
				return fmt.Errorf("decode proposal event detail: %w", err)
			}
			events = append(events, event)
		}
		return rows.Err()
	})
	return events, err
}

func (r *PgResourceChangeProposalRepo) transition(
	ctx context.Context,
	id, actor string,
	at time.Time,
	to domain.ProposalStatus,
	condition string,
) error {
	var transitionErr error
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		query := `WITH candidate AS (
            SELECT status FROM resource_change_proposals WHERE id=$1 AND ` + condition + ` FOR UPDATE
        ), updated AS (
            UPDATE resource_change_proposals SET status=$2, updated_at=$3
            WHERE id=$1 AND EXISTS (SELECT 1 FROM candidate)
        ) SELECT status FROM candidate`
		var from domain.ProposalStatus
		if err := tx.QueryRow(ctx, query, id, to, at).Scan(&from); errors.Is(err, pgx.ErrNoRows) {
			transitionErr = classifyTransitionFailure(ctx, tx, id, at)
			if errors.Is(transitionErr, domain.ErrProposalExpired) {
				return nil
			}
			return transitionErr
		} else if err != nil {
			return fmt.Errorf("transition proposal: %w", err)
		}
		return insertProposalEvent(ctx, tx, id, domain.ProposalEvent{
			ActorID: actor, FromStatus: from, ToStatus: to, CreatedAt: at,
		}, []byte(`{}`))
	})
	if err != nil {
		return err
	}
	return transitionErr
}

const proposalColumns = `id, COALESCE(conversation_id::text,''), proposer_id, confirmer_id, resource_kind,
    resource_id, operation, baseline_fingerprint, baseline_projection, payload, safe_summary, status, result, error_code,
    created_at, updated_at, confirmed_at, applied_at, expires_at, edit_count`

const proposalSelect = `SELECT ` + proposalColumns + ` FROM resource_change_proposals`

func scanProposal(row pgx.Row, proposal *domain.ResourceChangeProposal, baselineProjection, payload, summary, result *[]byte) error {
	return row.Scan(&proposal.ID, &proposal.ConversationID, &proposal.ProposerID, &proposal.ConfirmerID,
		&proposal.ResourceKind, &proposal.ResourceID, &proposal.Operation, &proposal.BaselineFingerprint,
		baselineProjection, payload, summary, &proposal.Status, result, &proposal.ErrorCode, &proposal.CreatedAt, &proposal.UpdatedAt,
		&proposal.ConfirmedAt, &proposal.AppliedAt, &proposal.ExpiresAt, &proposal.EditCount)
}

func decodeProposalJSON(proposal *domain.ResourceChangeProposal, baselineProjection, payload, summary, result []byte) error {
	proposal.BaselineProjection = append(proposal.BaselineProjection[:0], baselineProjection...)
	proposal.Payload = append(proposal.Payload[:0], payload...)
	var safeSummary map[string]string
	if err := json.Unmarshal(summary, &safeSummary); err != nil {
		return fmt.Errorf("decode proposal summary: %w", err)
	}
	proposal.Summary = safeSummary["text"]
	if len(result) > 0 {
		if err := json.Unmarshal(result, &proposal.ApplyResult); err != nil {
			return fmt.Errorf("decode proposal result: %w", err)
		}
	}
	return nil
}

func proposalBaselineProjectionJSON(value json.RawMessage) []byte {
	if len(value) == 0 || string(value) == "null" {
		return []byte(`{}`)
	}
	return value
}

func classifyTransitionFailure(ctx context.Context, tx pgx.Tx, id string, now time.Time) error {
	var status domain.ProposalStatus
	var expiresAt time.Time
	if err := tx.QueryRow(ctx, `SELECT status, expires_at FROM resource_change_proposals WHERE id=$1`, id).
		Scan(&status, &expiresAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrProposalNotFound
		}
		return fmt.Errorf("classify proposal transition: %w", err)
	}
	if !expiresAt.After(now) {
		if !domain.CanTransition(status, domain.StatusExpired) {
			return fmt.Errorf("%w: status=%s", domain.ErrProposalAlreadyClaimed, status)
		}
		command, err := tx.Exec(ctx, `UPDATE resource_change_proposals
            SET status='expired', updated_at=$2 WHERE id=$1 AND status=$3 AND expires_at<=$2`, id, now, status)
		if err != nil {
			return fmt.Errorf("persist proposal expiration: %w", err)
		}
		if command.RowsAffected() != 1 {
			return domain.ErrProposalAlreadyClaimed
		}
		if err := insertProposalEvent(ctx, tx, id, domain.ProposalEvent{
			FromStatus: status, ToStatus: domain.StatusExpired, CreatedAt: now,
		}, []byte(`{}`)); err != nil {
			return err
		}
		return domain.ErrProposalExpired
	}
	return fmt.Errorf("%w: status=%s", domain.ErrProposalAlreadyClaimed, status)
}

func marshalProposalEventDetail(event domain.ProposalEvent) ([]byte, error) {
	detail, err := json.Marshal(struct {
		Code    string `json:"code,omitempty"`
		Summary string `json:"summary,omitempty"`
	}{Code: event.Code, Summary: event.Summary})
	if err != nil {
		return nil, fmt.Errorf("marshal proposal event detail: %w", err)
	}
	return detail, nil
}

func insertProposalEvent(
	ctx context.Context,
	tx pgx.Tx,
	proposalID string,
	event domain.ProposalEvent,
	detail []byte,
) error {
	_, err := tx.Exec(ctx, `INSERT INTO resource_change_proposal_events
        (id, proposal_id, actor_id, from_status, to_status, detail, created_at)
        VALUES (COALESCE(NULLIF($1,'')::uuid, public.gen_uuid_v7()), $2, $3, $4, $5, $6::jsonb, $7)`,
		event.ID, proposalID, event.ActorID, event.FromStatus, event.ToStatus, string(detail), event.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert proposal event: %w", err)
	}
	return nil
}
