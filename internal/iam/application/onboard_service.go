// Package application provides IAM application services (JWT, onboarding).
package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/internal/iam/domain/port"
)

// Type aliases preserve the historical application.X surface for handlers/tests
// while moving the canonical definitions into iam/domain.
type (
	TenantInfo         = domain.TenantInfo
	CreateTenantInput  = domain.CreateTenantInput
	CreateTenantResult = domain.CreateTenantResult
	JoinTenantInput    = domain.JoinTenantInput
)

// OnboardService coordinates tenant creation and joining via OnboardRepo.
type OnboardService struct {
	repo port.OnboardRepo
}

// NewOnboardService creates an OnboardService over the given repo.
func NewOnboardService(repo port.OnboardRepo) *OnboardService {
	return &OnboardService{repo: repo}
}

// CreateTenant runs upsert-user + insert-tenant + insert-member + create-schema.
func (s *OnboardService) CreateTenant(ctx context.Context, in CreateTenantInput) (*CreateTenantResult, error) {
	return s.repo.CreateTenant(ctx, in)
}

// CreateTenantForUser creates a new tenant for an existing user (no upsert).
func (s *OnboardService) CreateTenantForUser(ctx context.Context, userID, name string) (string, error) {
	return s.repo.CreateTenantForUser(ctx, userID, name)
}

// GetUserTenant returns the user's first active tenant by GitHub ID.
func (s *OnboardService) GetUserTenant(ctx context.Context, githubID string) (string, string, bool, error) {
	return s.repo.GetUserTenant(ctx, githubID)
}

// GetUserTenants returns user UUID, global_role, and all their tenants.
func (s *OnboardService) GetUserTenants(ctx context.Context, githubID string) (string, string, []TenantInfo, bool, error) {
	return s.repo.GetUserTenants(ctx, githubID)
}

// SetGlobalRole updates users.global_role.
func (s *OnboardService) SetGlobalRole(ctx context.Context, userID, role string) error {
	return s.repo.SetGlobalRole(ctx, userID, role)
}

// GetGlobalRole returns users.global_role.
func (s *OnboardService) GetGlobalRole(ctx context.Context, userID string) (string, error) {
	return s.repo.GetGlobalRole(ctx, userID)
}

// AutoJoinDefaultTenant upserts the GitHub user into `users` and adds them to the default tenant.
// Returns userID, tenantID, and global_role.
func (s *OnboardService) AutoJoinDefaultTenant(ctx context.Context, githubID int64, githubLogin, avatarURL, globalAdminLogin string) (string, string, string, error) {
	return s.repo.AutoJoinDefaultTenant(ctx, domain.AutoJoinInput{
		GitHubID:         githubID,
		GitHubLogin:      githubLogin,
		AvatarURL:        avatarURL,
		GlobalAdminLogin: globalAdminLogin,
	})
}

// GetTenantRole returns the user's role in a specific tenant, or "member" as fallback.
func (s *OnboardService) GetTenantRole(ctx context.Context, userID, tenantID string) (string, error) {
	return s.repo.GetTenantRole(ctx, userID, tenantID)
}

// IsMember reports whether userID is an active member of tenantID.
func (s *OnboardService) IsMember(ctx context.Context, userID, tenantID string) (bool, error) {
	return s.repo.IsMember(ctx, userID, tenantID)
}

// JoinTenant accepts an invitation token and adds the user to the tenant.
func (s *OnboardService) JoinTenant(ctx context.Context, in JoinTenantInput) error {
	return s.repo.JoinTenant(ctx, in)
}
