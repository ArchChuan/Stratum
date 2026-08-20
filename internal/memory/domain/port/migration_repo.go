package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

// MigrationRepo 管理租户记忆嵌入模型迁移记录（tenant-scoped memory_migrations 表）。
// 所有方法显式携带 tenantID，实现必须通过 execTenant 走租户边界。
type MigrationRepo interface {
	// Create 插入一条迁移记录（status=migrating），返回 DB 生成的 id。
	Create(ctx context.Context, tenantID string, m *domain.MemoryMigration) (int64, error)

	// GetActive 返回租户最近一条仍在 migrating 的迁移；无则返回 (nil, nil)。
	GetActive(ctx context.Context, tenantID string) (*domain.MemoryMigration, error)

	// GetLatest 返回租户最近一条迁移（任意状态）；无则返回 (nil, nil)。
	GetLatest(ctx context.Context, tenantID string) (*domain.MemoryMigration, error)

	// GetByID 按 id 返回迁移；不存在返回 domain.ErrMigrationNotFound。
	GetByID(ctx context.Context, tenantID string, id int64) (*domain.MemoryMigration, error)

	// Advance 仅在 status='migrating' 时原子更新 progress 与 updated_at，
	// 返回是否命中（false = 迁移已被取消/失败，调用方应停止回填）。
	Advance(ctx context.Context, tenantID string, id int64, progress int) (bool, error)

	// Complete 把 migrating 迁移原子置为终态（done|failed|canceled）并更新
	// updated_at，返回是否命中（false = 迁移已不在 migrating）。
	Complete(ctx context.Context, tenantID string, id int64, status domain.MigrationStatus) (bool, error)

	// Restart 把 failed/canceled 迁移原子置回 migrating（重试续传），
	// 返回是否命中（false = 迁移不在可重试状态）。
	Restart(ctx context.Context, tenantID string, id int64) (bool, error)
}
