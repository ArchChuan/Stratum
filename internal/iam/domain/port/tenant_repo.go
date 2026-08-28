package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
)

// TenantRepo persists tenant-level data: members and settings.
type TenantRepo interface {
	CountMembers(ctx context.Context, tenantID string) (int, error)
	ListMembers(ctx context.Context, tenantID string, limit, offset int) ([]domain.Member, error)
	// ListMembersByRole returns every member holding one of the given roles.
	// Used to enumerate candidate editors (admin/owner) for resource sharing.
	ListMembersByRole(ctx context.Context, tenantID string, roles []string) ([]domain.Member, error)
	GetMemberRole(ctx context.Context, tenantID, userID string) (string, error)
	UpdateMemberRole(ctx context.Context, tenantID, userID, role string) error
	DeleteMember(ctx context.Context, tenantID, userID string) error
	GetTenantSettings(ctx context.Context, tenantID string) (name string, isDefault bool, settingsJSON []byte, err error)
	UpdateTenantName(ctx context.Context, tenantID, name string) error
	UpdateTenantSettings(ctx context.Context, tenantID string, settingsJSON []byte) error
	ListUserTenants(ctx context.Context, userID string) ([]domain.UserTenantInfo, error)
	// ListAllTenants returns every active tenant (id/name/is_default), used by
	// platform admins to enumerate all tenants for cross-tenant access.
	ListAllTenants(ctx context.Context) ([]domain.UserTenantInfo, error)
}
