package persistence

import (
	"context"
	"encoding/json"
	"fmt"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// insertChangeAudit 在业务事务内写一条变更审计；nil 事件跳过。与
// agent/skill/workflow 的 insertChangeAudit 同构（tenant 取自 tenant 上下文）。
func insertChangeAudit(ctx context.Context, tx pgx.Tx, ev *auditdomain.ResourceChangeAuditEvent) error {
	ev = ev.Normalized()
	if ev == nil {
		return nil
	}
	if ev.ResourceID == "" || ev.Operation == "" || ev.ResourceKind == "" {
		return fmt.Errorf("change audit: incomplete event (kind=%s id=%q op=%q)",
			ev.ResourceKind, ev.ResourceID, ev.Operation)
	}
	tc, ok := tenantdb.FromContext(ctx)
	if !ok || tc.TenantID == "" {
		return fmt.Errorf("change audit: missing tenant context")
	}
	_, err := tx.Exec(ctx, auditdomain.ChangeAuditInsertSQL,
		uuid.Must(uuid.NewV7()).String(), tc.TenantID,
		ev.ResourceKind, ev.ResourceID, ev.Operation, ev.ActorID, ev.ActorType, ev.Source,
		ev.ProposalID, ev.Before, ev.After)
	if err != nil {
		return fmt.Errorf("insert change audit %s %s: %w", ev.ResourceKind, ev.ResourceID, err)
	}
	return nil
}

// commandChangeAuditTx 在 applyCommand 事务内写命令类变更审计（activate/reject/
// pause/rollback；promote 走 promoteChangeAuditTx）。
func commandChangeAuditTx(
	ctx context.Context, tx pgx.Tx, current domain.Experiment, newStatus domain.ExperimentStatus, op, actorID string,
) error {
	before := experimentProjectionTx(current)
	after := experimentProjectionTx(current)
	after["status"] = string(newStatus)
	return insertProjectionAudit(ctx, tx, current.ID, op, actorID, before, after)
}

func experimentProjectionTx(e domain.Experiment) map[string]any {
	return map[string]any{
		"resource_kind": string(e.ResourceKind),
		"resource_id":   e.ResourceID,
		"status":        string(e.Status),
	}
}

func insertProjectionAudit(
	ctx context.Context, tx pgx.Tx, resourceID, op, actorID string, before, after map[string]any,
) error {
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return fmt.Errorf("change audit: marshal before: %w", err)
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("change audit: marshal after: %w", err)
	}
	return insertChangeAudit(ctx, tx, &auditdomain.ResourceChangeAuditEvent{
		ResourceKind: auditdomain.ResourceKindEvaluation,
		ResourceID:   resourceID,
		Operation:    op,
		ActorID:      actorID,
		ActorType:    auditdomain.ChangeActorUser,
		Source:       auditdomain.ChangeSourceAPI,
		Before:       beforeJSON,
		After:        afterJSON,
	})
}
