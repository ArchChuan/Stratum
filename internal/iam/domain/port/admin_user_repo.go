package port

import (
	"context"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
)

// AdminUser is a platform-scoped user row, used by the platform-admin UI.
type AdminUser struct {
	UserID      string
	Username    string
	GitHubLogin string
	AvatarURL   string
	GlobalRole  domain.GlobalRole
}

// AdminUserRepo manages users.global_role for the platform-admin UI. It is a
// separate write channel from the programmatic OnboardRepo.SetGlobalRole
// (which seeds global_admin from env); its role writes are fixed to
// system_admin — a signature-level guarantee that the UI cannot produce a
// super admin.
type AdminUserRepo interface {
	// SearchUsers returns non-guest, non-admin users matching query, newest first.
	SearchUsers(ctx context.Context, query string, limit int) ([]AdminUser, error)
	// ListAdmins returns all platform admins (system_admin + global_admin).
	ListAdmins(ctx context.Context) ([]AdminUser, error)
	// SetAdminRole promotes userID to system_admin. ErrUserNotFound if absent.
	SetAdminRole(ctx context.Context, userID string, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error
	// RemoveAdminRole demotes userID back to user. ErrUserNotFound if absent.
	RemoveAdminRole(ctx context.Context, userID string, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error
	// GetGlobalRole returns userID's current global_role.
	GetGlobalRole(ctx context.Context, userID string) (domain.GlobalRole, error)
}
