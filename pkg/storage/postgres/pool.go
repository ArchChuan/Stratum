package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

const (
	defaultMaxConns = 20
	defaultMinConns = 2
)

// Pool wraps a pgxpool.Pool with a structured logger. The embedded
// *pgxpool.Pool means Pool satisfies Querier and TxBeginner via method
// promotion.
type Pool struct {
	*pgxpool.Pool
	logger *zap.Logger
}

// New connects to PostgreSQL at url and returns a ready *Pool.
func New(ctx context.Context, url string, logger *zap.Logger) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse config: %w", err)
	}
	cfg.MaxConns = defaultMaxConns
	cfg.MinConns = defaultMinConns

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}

	logger.Info("postgres connected", zap.String("url", maskPassword(url)))
	return &Pool{Pool: pool, logger: logger}, nil
}

// DB returns the underlying *pgxpool.Pool. Retained for callers written
// against the legacy pkg/postgres.Pool API.
func (p *Pool) DB() *pgxpool.Pool { return p.Pool }

// Close shuts down the pool.
func (p *Pool) Close() {
	p.Pool.Close()
	p.logger.Info("postgres connection closed")
}

func maskPassword(url string) string {
	return "postgres://***@" + extractHost(url)
}

func extractHost(url string) string {
	for i := len(url) - 1; i >= 0; i-- {
		if url[i] == '@' {
			return url[i+1:]
		}
	}
	return url
}

// Compile-time assertion: *Pool satisfies the consumer-side interfaces.
var (
	_ TxBeginner   = (*Pool)(nil)
	_ TenantExecer = (*Pool)(nil)
)

// Wrap returns a Pool wrapping an externally-owned *pgxpool.Pool.
// The caller retains ownership; calling Close on the returned Pool
// closes the underlying pool. Used by api/wiring.NewFromExisting to
// adopt the pool created by cmd/server/main.go without reconnecting.
func Wrap(p *pgxpool.Pool) *Pool {
	return &Pool{Pool: p}
}
