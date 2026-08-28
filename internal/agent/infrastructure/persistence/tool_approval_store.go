package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/jackc/pgx/v5"
)

type PgToolApprovalStore struct{ pool chatPoolIface }

// 历史分页行为数字（m2：禁止内联）。
const (
	approvalHistoryPageMin         = 1
	approvalHistoryPageSizeDefault = 20
	approvalHistoryPageSizeMax     = 100
)

func NewPgToolApprovalStore(pool chatPoolIface) *PgToolApprovalStore {
	return &PgToolApprovalStore{pool: pool}
}

func (s *PgToolApprovalStore) Create(ctx context.Context, tenantID string, a domain.ToolApproval) (string, error) {
	var id string
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO agent_tool_approvals
		 (decision_id,execution_id,trace_id,agent_id,user_id,tool_call_id,server_id,tool_name,risk_level,
		  arguments_digest,skill_revisions_digest,mcp_revisions_digest,knowledge_revisions_digest,
		  policy_version,subject_kind,assigned_approver,conversation_id,encrypted_payload,status,expires_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,'pending',$19)
		 ON CONFLICT(execution_id,tool_call_id) DO UPDATE SET execution_id=EXCLUDED.execution_id
		 RETURNING id`, a.DecisionID, a.ExecutionID, a.TraceID, a.AgentID, a.UserID, a.ToolCallID, a.ServerID,
			a.ToolName, a.RiskLevel, a.ArgumentsDigest, a.SkillRevisionsDigest, a.MCPRevisionsDigest,
			a.KnowledgeRevisionsDigest, a.PolicyVersion,
			a.SubjectKind, a.AssignedApprover, a.ConversationID,
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
		 risk_level,arguments_digest,skill_revisions_digest,mcp_revisions_digest,knowledge_revisions_digest,
		 policy_version,subject_kind,assigned_approver,invalidation_reason,conversation_id,
		 encrypted_payload,status,decided_by,decision_reason,
		 created_at,decided_at,executed_at,expires_at FROM agent_tool_approvals WHERE id=$1`, id).Scan(
			&a.ID, &a.DecisionID, &a.ExecutionID, &a.TraceID, &a.AgentID, &a.UserID, &a.ToolCallID, &a.ServerID,
			&a.ToolName, &a.RiskLevel, &a.ArgumentsDigest, &a.SkillRevisionsDigest, &a.MCPRevisionsDigest,
			&a.KnowledgeRevisionsDigest, &a.PolicyVersion, &a.SubjectKind, &a.AssignedApprover,
			&a.InvalidationReason, &a.ConversationID,
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

// Cancel CAS：仅 pending 且未过期 → cancelled。0 行（非 pending 或已过期）→ ErrApprovalAlreadyDecided（与 Decide 同语义，保证取消/批准并发单胜者）。
func (s *PgToolApprovalStore) Cancel(ctx context.Context, tenantID, id, actor, reason string, now time.Time) error {
	return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE agent_tool_approvals SET status='cancelled',decided_by=$2,decision_reason=$3,decided_at=$4 WHERE id=$1 AND status='pending' AND expires_at>$4`, id, actor, reason, now)
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
		tag, err := tx.Exec(ctx, `UPDATE agent_tool_approvals SET status='approved' WHERE id=$1 AND status='executing'`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrApprovalAlreadyExecuted
		}
		return nil
	})
}

func (s *PgToolApprovalStore) MarkOutcomeUnknown(ctx context.Context, tenantID, id string) error {
	return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE agent_tool_approvals SET status='unknown_outcome' WHERE id=$1 AND status='executing'`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrApprovalAlreadyExecuted
		}
		return nil
	})
}

func (s *PgToolApprovalStore) ListPending(ctx context.Context, tenantID, userID string) ([]domain.ToolApproval, error) {
	out := []domain.ToolApproval{}
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		query := `SELECT id,execution_id,trace_id,agent_id,user_id,tool_call_id,server_id,tool_name,risk_level,
		 subject_kind,assigned_approver,conversation_id,status,created_at,expires_at FROM agent_tool_approvals
		 WHERE status='pending' AND expires_at>NOW()`
		args := []any{}
		if userID != "" {
			query += ` AND user_id=$1`
			args = append(args, userID)
			// 指定给当前用户的最前（软绑定优先级提示，D8）
			query += ` ORDER BY CASE WHEN assigned_approver=$1 THEN 0 ELSE 1 END, created_at`
		} else {
			query += ` ORDER BY created_at`
		}
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a domain.ToolApproval
			if err := rows.Scan(&a.ID, &a.ExecutionID, &a.TraceID, &a.AgentID, &a.UserID, &a.ToolCallID, &a.ServerID, &a.ToolName, &a.RiskLevel, &a.SubjectKind, &a.AssignedApprover, &a.ConversationID, &a.Status, &a.CreatedAt, &a.ExpiresAt); err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

func (s *PgToolApprovalStore) ListActionable(ctx context.Context, tenantID, userID string) ([]domain.ToolApproval, error) {
	out := []domain.ToolApproval{}
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		query := `SELECT id,execution_id,trace_id,agent_id,user_id,tool_call_id,server_id,tool_name,risk_level,
		 subject_kind,assigned_approver,conversation_id,status,created_at,expires_at FROM agent_tool_approvals
		 WHERE status IN ('pending','approved') AND expires_at>NOW()`
		args := []any{}
		if userID != "" {
			query += ` AND user_id=$1`
			args = append(args, userID)
			// 指定给当前用户的最前（软绑定优先级提示，D8）
			query += ` ORDER BY CASE WHEN assigned_approver=$1 THEN 0 ELSE 1 END, created_at`
		} else {
			query += ` ORDER BY created_at`
		}
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a domain.ToolApproval
			if err := rows.Scan(&a.ID, &a.ExecutionID, &a.TraceID, &a.AgentID, &a.UserID, &a.ToolCallID, &a.ServerID, &a.ToolName, &a.RiskLevel, &a.SubjectKind, &a.AssignedApprover, &a.ConversationID, &a.Status, &a.CreatedAt, &a.ExpiresAt); err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

// ListByExecution 返回指定 execution_id 的全部审批行（含终态，不依赖过期）。
// workflow 恢复判定用：workflow 不关心审批内容，只判断该 execution 是否存在
// 未过期 pending（存在即仍未决，等待下一轮 reconcile）。
func (s *PgToolApprovalStore) ListByExecution(ctx context.Context, tenantID, executionID string) ([]domain.ToolApproval, error) {
	out := []domain.ToolApproval{}
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id,execution_id,trace_id,agent_id,user_id,tool_call_id,server_id,tool_name,risk_level,
			 subject_kind,assigned_approver,conversation_id,status,created_at,expires_at FROM agent_tool_approvals
			 WHERE execution_id=$1 ORDER BY created_at`, executionID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a domain.ToolApproval
			if err := rows.Scan(&a.ID, &a.ExecutionID, &a.TraceID, &a.AgentID, &a.UserID, &a.ToolCallID,
				&a.ServerID, &a.ToolName, &a.RiskLevel, &a.SubjectKind, &a.AssignedApprover,
				&a.ConversationID, &a.Status, &a.CreatedAt, &a.ExpiresAt); err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

func (s *PgToolApprovalStore) InvalidateStaleForTool(ctx context.Context, tenantID, executionID, serverID, toolName string) (int64, error) {
	var n int64
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE agent_tool_approvals SET status='invalidated', invalidation_reason='superseded',
		 decided_at=NOW(), decided_by='system:superseded'
		 WHERE execution_id=$1 AND server_id=$2 AND tool_name=$3
		   AND subject_kind='mcp_tool' AND status='pending' AND expires_at>NOW()`, executionID, serverID, toolName)
		if err != nil {
			return err
		}
		n = tag.RowsAffected()
		return nil
	})
	return n, err
}

func (s *PgToolApprovalStore) ExpireStale(ctx context.Context, tenantID string) (int64, error) {
	var n int64
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE agent_tool_approvals SET status='expired', decided_by='system:expiry',
 decided_at=NOW()
 WHERE expires_at<NOW() AND status IN ('pending','approved')`)
		if err != nil {
			return err
		}
		n = tag.RowsAffected()
		return nil
	})
	return n, err
}

func (s *PgToolApprovalStore) ListHistory(ctx context.Context, tenantID, userID string, page, pageSize int) ([]domain.ToolApproval, int, error) {
	out := []domain.ToolApproval{}
	total := 0
	// userID 非空（member 语义）时 COUNT 与 SELECT 同步过滤，保证 total 与列表一致（H3）。
	where := ` WHERE status <> 'pending'`
	args := []any{}
	if userID != "" {
		where += ` AND user_id=$1`
		args = append(args, userID)
	}
	// LIMIT/OFFSET 占位符：user 过滤占用 $1 时 LIMIT 为 $2/$3，否则为 $1/$2（保持绑定顺序正确）。
	limitIdx, offsetIdx := "$1", "$2"
	if userID != "" {
		limitIdx, offsetIdx = "$2", "$3"
	}
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM agent_tool_approvals`+where, args...).Scan(&total); err != nil {
			return err
		}
		if page < approvalHistoryPageMin {
			page = approvalHistoryPageMin
		}
		if pageSize < approvalHistoryPageMin || pageSize > approvalHistoryPageSizeMax {
			pageSize = approvalHistoryPageSizeDefault
		}
		selectArgs := append(append([]any{}, args...), pageSize, (page-1)*pageSize)
		rows, err := tx.Query(ctx,
			`SELECT id,decision_id,execution_id,trace_id,agent_id,user_id,tool_call_id,server_id,tool_name,
			 risk_level,subject_kind,assigned_approver,invalidation_reason,conversation_id,policy_version,
			 encrypted_payload,status,decided_by,decision_reason,created_at,decided_at,executed_at,expires_at
			 FROM agent_tool_approvals`+where+` ORDER BY created_at DESC LIMIT `+limitIdx+` OFFSET `+offsetIdx,
			selectArgs...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a domain.ToolApproval
			if err := rows.Scan(&a.ID, &a.DecisionID, &a.ExecutionID, &a.TraceID, &a.AgentID, &a.UserID,
				&a.ToolCallID, &a.ServerID, &a.ToolName, &a.RiskLevel, &a.SubjectKind, &a.AssignedApprover,
				&a.InvalidationReason, &a.ConversationID, &a.PolicyVersion, &a.EncryptedPayload, &a.Status,
				&a.DecidedBy, &a.DecisionReason, &a.CreatedAt, &a.DecidedAt, &a.ExecutedAt, &a.ExpiresAt); err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, total, err
}

func (s *PgToolApprovalStore) Invalidate(ctx context.Context, tenantID, id, reason string) error {
	return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE agent_tool_approvals SET status='invalidated',invalidation_reason=$2
			 WHERE id=$1 AND status IN ('approved','executing')`, id, reason)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrApprovalAlreadyExecuted
		}
		return nil
	})
}

func (s *PgToolApprovalStore) Void(ctx context.Context, tenantID, id, reason string) error {
	return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE agent_tool_approvals SET status='voided',invalidation_reason=$2
			 WHERE id=$1 AND status='approved'`, id, reason)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrApprovalAlreadyExecuted
		}
		return nil
	})
}

func (s *PgToolApprovalStore) UpdateAssignee(ctx context.Context, tenantID, id, assignee string) error {
	return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE agent_tool_approvals SET assigned_approver=$2 WHERE id=$1 AND status='pending'`,
			id, assignee)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrApprovalAlreadyDecided
		}
		return nil
	})
}

func (s *PgToolApprovalStore) CascadeByConversation(ctx context.Context, tenantID, conversationID string) error {
	// 修复 review major：空 conversationID 必须拒绝（fail closed）——否则 WHERE conversation_id=''
	// 会批量命中所有未关联会话的审批，pending→cancelled、approved→voided 全租户误伤。
	if conversationID == "" {
		return domain.ErrApprovalConversationGone
	}
	return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE agent_tool_approvals SET status='cancelled',invalidation_reason='conversation_deleted'
			 WHERE conversation_id=$1 AND status='pending'`, conversationID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE agent_tool_approvals SET status='voided',invalidation_reason='conversation_deleted'
			 WHERE conversation_id=$1 AND status='approved'`, conversationID); err != nil {
			return err
		}
		// 级联幂等：无 pending 命中（0 行）不代表失败——approved/executed 仍可能被级联或已处理。
		return nil
	})
}
