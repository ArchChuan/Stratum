package infrastructure

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
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
// search_path）；nil 事件跳过。
func insertAuditTx(ctx context.Context, tx pgx.Tx, tenantID string, ev *auditdomain.ResourceChangeAuditEvent) error {
	if ev == nil {
		return nil
	}
	ev = ev.Normalized()
	_, err := tx.Exec(ctx, auditdomain.ChangeAuditInsertSQL,
		ev.ResourceID+"-"+ev.Operation+"-"+tenantID, tenantID, ev.ResourceKind, ev.ResourceID,
		ev.Operation, ev.ActorID, ev.ActorType, ev.Source, ev.ProposalID,
		ev.Before, ev.After)
	return err
}
