package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

// tenantPool 是 pgxmock 可替换的最小池接口，满足 postgres.ExecTenantWith。
type tenantPool interface {
	Begin(context.Context) (pgx.Tx, error)
}

type MCPTokenReplayRepo struct {
	pool tenantPool
}

func NewMCPTokenReplayRepo(pool *pgxpool.Pool) *MCPTokenReplayRepo {
	return &MCPTokenReplayRepo{pool: pool}
}

func (r *MCPTokenReplayRepo) ConsumeInvocationJTI(
	ctx context.Context,
	tenantID, jti string,
	expiresAt time.Time,
) (bool, error) {
	var consumed bool
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO mcp_invocation_jtis (jti, expires_at)
			SELECT $1, $2 WHERE $2 > NOW()
			ON CONFLICT (jti) DO NOTHING`, jti, expiresAt)
		if err != nil {
			return fmt.Errorf("insert invocation JTI: %w", err)
		}
		consumed = tag.RowsAffected() == 1
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("consume invocation JTI: %w", err)
	}
	return consumed, nil
}

func (r *MCPTokenReplayRepo) execTenant(
	ctx context.Context,
	tenantID string,
	fn func(context.Context, pgx.Tx) error,
) error {
	return postgres.ExecTenantWith(ctx, r.pool, tenantID, fn)
}
