package persistence

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/audit/domain/port"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgFailureAuditRepo 把失败的资源操作写入租户 resource_change_audits。
// 与成功路径的 insertChangeAudit（同事务、fail closed）不同：失败记录是
// 旁路审计，写入失败只上抛错误供调用方记日志，绝不改变主流程结果。
type PgFailureAuditRepo struct {
	pool *pgxpool.Pool
}

func NewPgFailureAuditRepo(pool *pgxpool.Pool) *PgFailureAuditRepo {
	return &PgFailureAuditRepo{pool: pool}
}

func (r *PgFailureAuditRepo) Record(ctx context.Context, f port.ResourceFailure) error {
	if f.ResourceKind == "" || f.ResourceID == "" || f.Operation == "" {
		return fmt.Errorf("failure audit: incomplete event (kind=%s id=%q op=%q)",
			f.ResourceKind, f.ResourceID, f.Operation)
	}
	after, err := json.Marshal(map[string]string{
		"status":     "failed",
		"error_code": f.ErrorCode,
		"detail":     f.Detail,
	})
	if err != nil {
		return fmt.Errorf("failure audit: marshal after: %w", err)
	}
	return tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		tc, ok := tenantdb.FromContext(ctx)
		if !ok || tc.TenantID == "" {
			return fmt.Errorf("failure audit: missing tenant context")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO resource_change_audits
				(id, tenant_id, resource_kind, resource_id, operation, actor_id, after_projection)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			uuid.Must(uuid.NewV7()).String(), tc.TenantID,
			f.ResourceKind, f.ResourceID, f.Operation+"_failed", tc.UserID, string(after),
		); err != nil {
			return fmt.Errorf("insert failure audit %s %s: %w", f.ResourceKind, f.ResourceID, err)
		}
		return nil
	})
}
