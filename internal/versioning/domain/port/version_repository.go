package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/versioning/domain"
)

// VersionRepo 是通用版本历史基座的只读 port。所有方法显式携带 tenantID
// （租户仓储规则）；kind 标识资源类型，product 表的生效版本指针由实现负责推导。
type VersionRepo interface {
	ListVersions(ctx context.Context, tenantID string, kind domain.ResourceKind, resourceID string) ([]domain.Version, error)
	GetVersion(ctx context.Context, tenantID string, kind domain.ResourceKind, resourceID, versionID string) (domain.Version, bool, error)
}
