package persistence

import (
	"context"

	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5"
)

// poolIface 是 evaluation persistence 仓库所需的最小 pool 接口
// （允许 pgxmock 注入，与 internal/agent/infrastructure/persistence 同模式）。
type poolIface interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// execTenantTx 在 tenantID 的 search_path 事务内执行 fn。
// 替代 tenantdb.ExecTenant 的显式租户版本：校验 tenantID 而非依赖 ctx。
func execTenantTx(ctx context.Context, pool poolIface, tenantID string, fn func(context.Context, pgx.Tx) error) error {
	return pgstore.ExecTenantWith(ctx, pool, tenantID, fn)
}
