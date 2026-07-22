package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/jackc/pgx/v5"
)

type PgToolApprovalStore struct{ pool chatPoolIface }

func NewPgToolApprovalStore(pool chatPoolIface) *PgToolApprovalStore {
	return &PgToolApprovalStore{pool: pool}
}

func (s *PgToolApprovalStore) Create(ctx context.Context, tenantID string, a domain.ToolApproval) (string, error) {
	var id string
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO agent_tool_approvals
		 (decision_id,execution_id,trace_id,agent_id,user_id,tool_call_id,server_id,tool_name,risk_level,
		  arguments_digest,skill_revisions_digest,policy_version,encrypted_payload,status,expires_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'pending',$14)
		 ON CONFLICT(execution_id,tool_call_id) DO UPDATE SET execution_id=EXCLUDED.execution_id
		 RETURNING id`, a.DecisionID, a.ExecutionID, a.TraceID, a.AgentID, a.UserID, a.ToolCallID, a.ServerID,
			a.ToolName, a.RiskLevel, a.ArgumentsDigest, a.SkillRevisionsDigest, a.PolicyVersion,
			a.EncryptedPayload, a.ExpiresAt).Scan(&id)
	})
	if err != nil {
		return "", fmt.Errorf("tool approval create: %w", err)
	}
	return id, nil
}

func (s *PgToolApprovalStore) Get(ctx context.Context, tenantID, id string) (domain.ToolApproval, error) {
	var a domain.ToolApproval
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id,decision_id,execution_id,trace_id,agent_id,user_id,tool_call_id,server_id,tool_name,
		 risk_level,arguments_digest,skill_revisions_digest,policy_version,encrypted_payload,status,decided_by,decision_reason,
		 created_at,decided_at,executed_at,expires_at FROM agent_tool_approvals WHERE id=$1`, id).Scan(
			&a.ID, &a.DecisionID, &a.ExecutionID, &a.TraceID, &a.AgentID, &a.UserID, &a.ToolCallID, &a.ServerID,
			&a.ToolName, &a.RiskLevel, &a.ArgumentsDigest, &a.SkillRevisionsDigest, &a.PolicyVersion,
			&a.EncryptedPayload, &a.Status, &a.DecidedBy, &a.DecisionReason, &a.CreatedAt, &a.DecidedAt, &a.ExecutedAt,
			&a.ExpiresAt)
	})
	if err == pgx.ErrNoRows {
		return domain.ToolApproval{}, domain.ErrApprovalNotFound
	}
	return a, err
}

func (s *PgToolApprovalStore) Decide(ctx context.Context, tenantID, id, decision, actor, reason string, now time.Time) error {
	return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE agent_tool_approvals SET status=$2,decided_by=$3,decision_reason=$4,decided_at=$5 WHERE id=$1 AND status='pending' AND expires_at>$5`, id, decision, actor, reason, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrApprovalAlreadyDecided
		}
		return nil
	})
}

func (s *PgToolApprovalStore) MarkExecuted(ctx context.Context, tenantID, id string) error {
	return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE agent_tool_approvals SET status='executed',executed_at=NOW() WHERE id=$1 AND status='executing'`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrApprovalAlreadyExecuted
		}
		return nil
	})
}

func (s *PgToolApprovalStore) ClaimExecution(ctx context.Context, tenantID, id string) error {
	return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE agent_tool_approvals SET status='executing' WHERE id=$1 AND status='approved' AND expires_at>NOW()`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrApprovalAlreadyExecuted
		}
		return nil
	})
}
func (s *PgToolApprovalStore) ReleaseExecution(ctx context.Context, tenantID, id string) error {
	return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE agent_tool_approvals SET status='approved' WHERE id=$1 AND status='executing'`, id)
		return err
	})
}

func (s *PgToolApprovalStore) ListPending(ctx context.Context, tenantID string) ([]domain.ToolApproval, error) {
	out := []domain.ToolApproval{}
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id,execution_id,trace_id,agent_id,user_id,tool_call_id,server_id,tool_name,risk_level,status,created_at,expires_at FROM agent_tool_approvals WHERE status='pending' AND expires_at>NOW() ORDER BY created_at`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a domain.ToolApproval
			if err := rows.Scan(&a.ID, &a.ExecutionID, &a.TraceID, &a.AgentID, &a.UserID, &a.ToolCallID, &a.ServerID, &a.ToolName, &a.RiskLevel, &a.Status, &a.CreatedAt, &a.ExpiresAt); err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}
