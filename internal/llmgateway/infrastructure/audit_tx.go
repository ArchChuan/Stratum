package infrastructure

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/pkg/resourceaccess"
)

// pgxBeginner 抽象 pool 与 tx 的 Begin（测试可 mock）。
type pgxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// beginTenantTx 开启事务并切换租户 schema（resource_change_audits 位于
// tenant_<id>）；SET LOCAL 随事务结束自动失效，无连接残留。调用方必须
// defer Rollback。
func beginTenantTx(ctx context.Context, pool pgxBeginner, tenantID string) (pgx.Tx, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path = %q, public", "tenant_"+tenantID)); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("set search_path: %w", err)
	}
	return tx, nil
}

// insertAuditTx 在业务事务内写资源变更审计（表在租户 schema，依赖当前事务
// search_path）；nil 事件跳过。薄包装 pkg/resourceaccess 共享实现；审计行
// id 由此前确定的 `resourceID-op-tenantID` 收敛为 uuid v7，与其他资源上下文
// 一致（无外部消费者依赖旧格式）。
func insertAuditTx(ctx context.Context, tx pgx.Tx, tenantID string, ev *auditdomain.ResourceChangeAuditEvent) error {
	if ev == nil {
		return nil
	}
	ev = ev.Normalized()
	if ev == nil {
		return nil
	}
	return resourceaccess.InsertChangeAudit(ctx, tx, tenantID, auditdomain.ChangeAuditInsertSQL, resourceaccess.ChangeAudit{
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
