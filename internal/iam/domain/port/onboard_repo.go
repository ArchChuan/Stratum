package port

import (
	"context"
	"time"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
)

// OnboardRepo persists tenant creation/joining and user-onboarding flows.
// All methods operate on the public schema.
type OnboardRepo interface {
	// CreateTenant runs upsert-user + insert-tenant + insert-member + create-schema in one tx.
	CreateTenant(ctx context.Context, in domain.CreateTenantInput) (*domain.CreateTenantResult, error)
	// CreateTenantForUser creates a new tenant for an existing user (no upsert).
	CreateTenantForUser(ctx context.Context, userID, name string) (tenantID string, err error)
	// GetUserTenant returns the user's first active tenant by GitHub ID.
	GetUserTenant(ctx context.Context, githubID string) (userID, tenantID string, found bool, err error)
	// GetUserTenants returns user UUID, global_role, and all their tenants.
	GetUserTenants(ctx context.Context, githubID string) (userID, globalRole string, tenants []domain.TenantInfo, found bool, err error)
	// SetGlobalRole updates users.global_role.
	SetGlobalRole(ctx context.Context, userID, role string) error
	// GetGlobalRole returns users.global_role.
	GetGlobalRole(ctx context.Context, userID string) (string, error)
	// AutoJoinDefaultTenant upserts the GitHub user and joins the default tenant.
	AutoJoinDefaultTenant(ctx context.Context, in domain.AutoJoinInput) (userID, tenantID, globalRole string, err error)
	// GetTenantRole returns the role for (userID, tenantID).
	GetTenantRole(ctx context.Context, userID, tenantID string) (string, error)
	// IsMember reports whether userID is an active member of tenantID.
	IsMember(ctx context.Context, userID, tenantID string) (bool, error)

	// CreateGuestInDefaultTenant inserts a synthetic guest user with the given
	// github_id/login/expiry and joins them to the default tenant as a member,
	// all in one tx. Returns the new user UUID and the default tenant ID.
	CreateGuestInDefaultTenant(ctx context.Context, githubID, githubLogin, avatarURL string, expiresAt time.Time) (userID, tenantID string, err error)

	// ListExpiredGuests returns UUIDs of guest users whose expires_at is in the past.
	ListExpiredGuests(ctx context.Context, now time.Time) ([]string, error)

	// ListOwnedNonDefaultTenants returns tenant IDs the user owns that are not the default tenant.
	ListOwnedNonDefaultTenants(ctx context.Context, userID string) ([]string, error)

	// DeleteUser hard-deletes the user row; FK cascades remove tenant_members and refresh_tokens.
	DeleteUser(ctx context.Context, userID string) error
}
