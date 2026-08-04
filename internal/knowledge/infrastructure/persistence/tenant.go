package persistence

import (
	"context"

	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// poolIface allows pgxmock injection in tests.
type poolIface interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

var _ poolIface = (*pgxpool.Pool)(nil)

func execTenant(ctx context.Context, pool poolIface, tenantID string, fn func(context.Context, pgx.Tx) error) error {
	return pgstore.ExecTenantWith(ctx, pool, tenantID, fn)
}
