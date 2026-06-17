// Package postgres re-exports pkg/storage/postgres for backwards compatibility.
//
// New code should import github.com/byteBuilderX/stratum/pkg/storage/postgres
// directly; this shim will be removed in phase 5 of the DDD refactor.
package postgres

import (
	"context"

	storagepg "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"go.uber.org/zap"
)

// Pool is an alias for storage/postgres.Pool.
type Pool = storagepg.Pool

// New is a thin wrapper around storage/postgres.New.
func New(ctx context.Context, dsn string, logger *zap.Logger) (*storagepg.Pool, error) {
	return storagepg.New(ctx, dsn, logger)
}
