package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

type ActiveSnapshotRepo interface {
	Upsert(ctx context.Context, snapshot *domain.ActiveSnapshot) error
	Get(ctx context.Context, tenantID, userID, agentID string) (*domain.ActiveSnapshot, error)
	Delete(ctx context.Context, tenantID, userID, agentID string) error
	// ListUser lists every snapshot row for a user across agents (含过期/inactive，
	// 管理页需展示并允许清空；区别于注入路径 Get 的活跃过滤)。
	ListUser(ctx context.Context, tenantID, userID string) ([]*domain.ActiveSnapshot, error)
}
