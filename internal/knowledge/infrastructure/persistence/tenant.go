package persistence

import (
	"context"

	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func execTenant(ctx context.Context, pool *pgxpool.Pool, tenantID string, fn func(context.Context, pgx.Tx) error) error {
	return pgstore.Wrap(pool).ExecTenant(ctx, tenantID, fn)
}
