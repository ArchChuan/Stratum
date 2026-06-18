package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
)

// TenantRepo persists tenant-level data: members, invitations, settings.
type TenantRepo interface {
	CountMembers(ctx context.Context, tenantID string) (int, error)
	ListMembers(ctx context.Context, tenantID string, limit, offset int) ([]domain.Member, error)
	GetMemberRole(ctx context.Context, tenantID, userID string) (string, error)
	UpdateMemberRole(ctx context.Context, tenantID, userID, role string) error
	DeleteMember(ctx context.Context, tenantID, userID string) error
	CreateInvitation(ctx context.Context, inv domain.Invitation) error
	GetTenantSettings(ctx context.Context, tenantID string) (name string, settingsJSON []byte, err error)
	UpdateTenantName(ctx context.Context, tenantID, name string) error
	UpdateTenantSettings(ctx context.Context, tenantID string, settingsJSON []byte) error
	ListUserTenants(ctx context.Context, userID string) ([]domain.UserTenantInfo, error)
}
