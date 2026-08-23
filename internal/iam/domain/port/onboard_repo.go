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

	// CreateGuestSandboxTenant inserts a synthetic guest user with the given
	// github_id/login/expiry and creates a dedicated per-guest sandbox tenant
	// with the guest as owner (status 'provisioning'), all in one tx. The guest
	// is never a member of the default tenant; caller must provision and
	// activate the sandbox schema afterwards. Returns the new user UUID and
	// sandbox tenant ID.
	CreateGuestSandboxTenant(ctx context.Context, githubID, githubLogin, avatarURL string, expiresAt time.Time) (userID, tenantID string, err error)

	// ListExpiredGuests returns UUIDs of guest users whose expires_at is in the past.
	ListExpiredGuests(ctx context.Context, now time.Time) ([]string, error)

	// ListOwnedNonDefaultTenants returns tenant IDs the user owns that are not the default tenant.
	ListOwnedNonDefaultTenants(ctx context.Context, userID string) ([]string, error)

	// DeleteUser hard-deletes the user row; FK cascades remove tenant_members and refresh_tokens.
	DeleteUser(ctx context.Context, userID string) error

	// RegisterByUsername creates a local user (github_id='local:<username>') and joins the default tenant.
	// Returns user UUID and tenant ID.
	RegisterByUsername(ctx context.Context, username, passwordHash string) (userID, tenantID string, err error)
	// FindByUsername looks up a local user by username. Returns zero values with found=false when absent.
	FindByUsername(ctx context.Context, username string) (userID, passwordHash, globalRole string, found bool, err error)
	// FindByUsernameWithLogin is like FindByUsername but also returns github_login (display name).
	FindByUsernameWithLogin(ctx context.Context, username string) (userID, passwordHash, githubLogin, globalRole string, found bool, err error)
	// FindUsernameByUserID returns the user's username (empty if not a password user).
	FindUsernameByUserID(ctx context.Context, userID string) (string, error)
	// GetUserTenantByUserID returns the tenant a user should land in at login:
	// first the non-default tenant they created (owner), else the first
	// non-default tenant they joined, else the default tenant.
	GetUserTenantByUserID(ctx context.Context, userID string) (tenantID, role string, err error)
	// UpdateProfile updates the user's display name and/or avatar URL.
	UpdateProfile(ctx context.Context, userID, displayName, avatarURL string) error
}
