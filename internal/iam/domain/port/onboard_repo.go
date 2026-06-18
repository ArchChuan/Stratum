package port

import (
	"context"

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
	// JoinTenant accepts an invitation token and adds the user to the tenant.
	JoinTenant(ctx context.Context, in domain.JoinTenantInput) error
}
