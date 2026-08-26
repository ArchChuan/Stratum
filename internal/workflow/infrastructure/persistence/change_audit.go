package persistence

import (
	"context"
	"fmt"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/pkg/resourceaccess"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
)

// insertChangeAudit 在业务事务内写一条变更审计；nil 事件跳过。租户取自
// tenant 上下文（execTenant 注入）；缺失为调用方 bug，fail transaction
// closed。薄包装 pkg/resourceaccess 共享实现。
func insertChangeAudit(ctx context.Context, tx pgx.Tx, ev *auditdomain.ResourceChangeAuditEvent) error {
	ev = ev.Normalized()
	if ev == nil {
		return nil
	}
	tc, ok := tenantdb.FromContext(ctx)
	if !ok || tc.TenantID == "" {
		return fmt.Errorf("change audit: missing tenant context")
	}
	return resourceaccess.InsertChangeAudit(ctx, tx, tc.TenantID, auditdomain.ChangeAuditInsertSQL, resourceaccess.ChangeAudit{
		ResourceKind: ev.ResourceKind,
		ResourceID:   ev.ResourceID,
		Operation:    ev.Operation,
		ActorID:      ev.ActorID,
		ActorType:    ev.ActorType,
		Source:       ev.Source,
		ProposalID:   ev.ProposalID,
		Before:       ev.Before,
		After:        ev.After,
	})
}
