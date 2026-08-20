package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

// MigrationRepo 持久化记忆嵌入模型迁移记录（tenant-scoped memory_migrations 表）。
// 所有方法显式携带 tenantID 并走 execTenant 租户边界，禁止直接调用 pool。
type MigrationRepo struct {
	pool tenantPool
}

// NewMigrationRepo wires a MigrationRepo over a pgx pool. A nil pool leaves the
// repo permanently fail-closed; wiring must replace it with an explicit noop if
// persistence is disabled.
func NewMigrationRepo(pool *pgxpool.Pool) *MigrationRepo {
	if pool == nil {
		return &MigrationRepo{}
	}
	return &MigrationRepo{pool: pool}
}

func (r *MigrationRepo) execTenant(ctx context.Context, tenantID string, fn func(context.Context, pgx.Tx) error) error {
	if isNilPool(r.pool) {
		return fmt.Errorf("memory: migration persistence pool is nil")
	}
	if tenantID == "" {
		return fmt.Errorf("memory: migration tenant_id is empty")
	}
	return pgstore.ExecTenantWith(ctx, r.pool, tenantID, fn)
}

const migrationColumns = `id, tenant_id, from_model, to_model, status, progress, total_facts, created_at, updated_at`

func scanMigration(row pgx.Row) (*domain.MemoryMigration, error) {
	var m domain.MemoryMigration
	// status 以 TEXT 存储；先扫进 string 再转换，pgx 不会自动赋给命名 string 类型。
	var status string
	err := row.Scan(
		&m.ID, &m.TenantID, &m.FromModel, &m.ToModel, &status,
		&m.Progress, &m.TotalFacts, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	m.Status = domain.MigrationStatus(status)
	return &m, nil
}

// Create 插入一条 migrating 状态、零进度的迁移记录，返回 DB 生成的 id。
func (r *MigrationRepo) Create(ctx context.Context, tenantID string, m *domain.MemoryMigration) (int64, error) {
	const query = `
		INSERT INTO memory_migrations (tenant_id, from_model, to_model, status, progress, total_facts)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`

	var id int64
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, query, tenantID, m.FromModel, m.ToModel,
			string(domain.MigrationStatusMigrating), m.Progress, m.TotalFacts).Scan(&id)
	})
	if err != nil {
		return 0, translatePgError(err, "create migration")
	}
	return id, nil
}

// GetActive 返回租户最近一条仍在 migrating 的迁移；无则返回 (nil, nil)。
func (r *MigrationRepo) GetActive(ctx context.Context, tenantID string) (*domain.MemoryMigration, error) {
	const query = `
		SELECT ` + migrationColumns + `
		FROM memory_migrations
		WHERE tenant_id = $1 AND status = 'migrating'
		ORDER BY id DESC LIMIT 1`

	var m *domain.MemoryMigration
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx, query, tenantID)
		got, err := scanMigration(row)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get active migration: %w", err)
		}
		m = got
		return nil
	})
	return m, err
}

// GetLatest 返回租户最近一条迁移（任意状态）；无则返回 (nil, nil)。
func (r *MigrationRepo) GetLatest(ctx context.Context, tenantID string) (*domain.MemoryMigration, error) {
	const query = `
		SELECT ` + migrationColumns + `
		FROM memory_migrations
		WHERE tenant_id = $1
		ORDER BY id DESC LIMIT 1`

	var m *domain.MemoryMigration
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx, query, tenantID)
		got, err := scanMigration(row)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get latest migration: %w", err)
		}
		m = got
		return nil
	})
	return m, err
}

// GetByID 按 id 返回迁移；不存在返回 domain.ErrMigrationNotFound。
func (r *MigrationRepo) GetByID(ctx context.Context, tenantID string, id int64) (*domain.MemoryMigration, error) {
	const query = `
		SELECT ` + migrationColumns + `
		FROM memory_migrations
		WHERE tenant_id = $1 AND id = $2`

	var m *domain.MemoryMigration
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx, query, tenantID, id)
		got, err := scanMigration(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrMigrationNotFound
		}
		if err != nil {
			return fmt.Errorf("get migration by id: %w", err)
		}
		m = got
		return nil
	})
	return m, err
}

// Advance 仅在 status='migrating' 时原子更新 progress 与 updated_at。
// 返回 false 表示迁移已被取消/失败，调用方应停止回填。
func (r *MigrationRepo) Advance(ctx context.Context, tenantID string, id int64, progress int) (bool, error) {
	const query = `
		UPDATE memory_migrations SET progress = $3, updated_at = NOW()
		WHERE tenant_id = $1 AND id = $2 AND status = 'migrating'`

	var hit bool
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, query, tenantID, id, progress)
		if err != nil {
			return translatePgError(err, "advance migration")
		}
		hit = tag.RowsAffected() == 1
		return nil
	})
	return hit, err
}

// Complete 把 migrating 迁移原子置为终态（done|failed|canceled）。
// 返回 false 表示迁移已不在 migrating（例如被并发取消），不做任何改动。
func (r *MigrationRepo) Complete(ctx context.Context, tenantID string, id int64, status domain.MigrationStatus) (bool, error) {
	const query = `
		UPDATE memory_migrations SET status = $3, updated_at = NOW()
		WHERE tenant_id = $1 AND id = $2 AND status = 'migrating'`

	var hit bool
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, query, tenantID, id, string(status))
		if err != nil {
			return translatePgError(err, "complete migration")
		}
		hit = tag.RowsAffected() == 1
		return nil
	})
	return hit, err
}

// Restart 把 failed/canceled 迁移原子置回 migrating（重试续传）。
// 返回 false 表示迁移不在可重试状态，不做任何改动。
func (r *MigrationRepo) Restart(ctx context.Context, tenantID string, id int64) (bool, error) {
	const query = `
		UPDATE memory_migrations SET status = 'migrating', updated_at = NOW()
		WHERE tenant_id = $1 AND id = $2 AND status IN ('failed', 'canceled')`

	var hit bool
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, query, tenantID, id)
		if err != nil {
			return translatePgError(err, "restart migration")
		}
		hit = tag.RowsAffected() == 1
		return nil
	})
	return hit, err
}
