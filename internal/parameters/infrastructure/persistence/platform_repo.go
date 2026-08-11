package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/internal/parameters/domain/port"
)

// platformPool keeps the repository decoupled from *pgxpool.Pool for tests.
type platformPool interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// PlatformRepository stores platform-scope parameter values in the public
// platform_settings table. Public-scope by nature: direct pool access with
// schema-qualified names (startup-path rule in migration-tenant.md), no
// execTenant routing.
type PlatformRepository struct {
	pool platformPool
}

func NewPlatformRepository(pool *pgxpool.Pool) *PlatformRepository {
	return &PlatformRepository{pool: pool}
}

func (r *PlatformRepository) GetValue(
	ctx context.Context,
	key string,
) (json.RawMessage, bool, error) {
	var raw []byte
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM public.platform_settings WHERE key = $1`, key,
	).Scan(&raw)
	switch err {
	case nil:
		return json.RawMessage(raw), true, nil
	case pgx.ErrNoRows:
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("platform repository: get %s: %w", key, err)
	}
}

func (r *PlatformRepository) SetValue(
	ctx context.Context,
	key string,
	value json.RawMessage,
	updatedBy string,
) error {
	if err := r.pool.QueryRow(ctx,
		`INSERT INTO public.platform_settings (key, value, updated_by)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = NOW()
		 RETURNING key`, key, string(value), updatedBy,
	).Scan(new(string)); err != nil {
		return fmt.Errorf("platform repository: set %s: %w", key, err)
	}
	return nil
}

func (r *PlatformRepository) GetAll(ctx context.Context) ([]port.PlatformValue, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT key, value, updated_by, updated_at FROM public.platform_settings`)
	if err != nil {
		return nil, fmt.Errorf("platform repository: list: %w", err)
	}
	defer rows.Close()

	var out []port.PlatformValue
	for rows.Next() {
		var (
			v    port.PlatformValue
			raw  []byte
			upAt time.Time
		)
		if err := rows.Scan(&v.Key, &raw, &v.UpdatedBy, &upAt); err != nil {
			return nil, fmt.Errorf("platform repository: scan: %w", err)
		}
		v.Value = json.RawMessage(raw)
		v.UpdatedAt = upAt
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("platform repository: rows iteration: %w", err)
	}
	return out, nil
}
