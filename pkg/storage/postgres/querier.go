// Package postgres provides PostgreSQL pool wrappers and consumer-side
// query interfaces. Business code depends on these interfaces, not on
// pgxpool.Pool directly, so storage backends can be swapped or mocked.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier is the minimum surface for read/write SQL execution. It is
// satisfied by *pgxpool.Pool, pgx.Tx, and *Pool (via method promotion).
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// TxBeginner is a Querier that can also start a transaction.
type TxBeginner interface {
	Querier
	Begin(ctx context.Context) (pgx.Tx, error)
}

// TenantExecer runs work inside a tenant-scoped transaction. Implementations
// must set search_path to the tenant schema before calling fn.
type TenantExecer interface {
	ExecTenant(ctx context.Context, tenantID string, fn func(ctx context.Context, tx pgx.Tx) error) error
}

// Compile-time assertion: pgxpool.Pool satisfies TxBeginner directly.
var _ TxBeginner = (*pgxpool.Pool)(nil)
