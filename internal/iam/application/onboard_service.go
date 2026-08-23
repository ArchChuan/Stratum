// Package application provides IAM application services (JWT, onboarding).
package application

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/internal/iam/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// Type aliases preserve the historical application.X surface for handlers/tests
// while moving the canonical definitions into iam/domain.
type (
	TenantInfo         = domain.TenantInfo
	CreateTenantInput  = domain.CreateTenantInput
	CreateTenantResult = domain.CreateTenantResult
)

// GuestAccount is the provisioned identity returned to the guest-login handler.
type GuestAccount struct {
	UserID      string
	TenantID    string
	GitHubLogin string
	AvatarURL   string
}

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
func (s *OnboardService) AutoJoinDefaultTenant(
	ctx context.Context,
	githubID int64,
	githubLogin, avatarURL, email, globalAdminLogin string,
) (string, string, string, error) {
	return s.repo.AutoJoinDefaultTenant(ctx, domain.AutoJoinInput{
		GitHubID:         githubID,
		GitHubLogin:      githubLogin,
		AvatarURL:        avatarURL,
		Email:            email,
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

// CreateGuest provisions a temporary guest identity: a synthetic namespaced
// github_id, a dedicated per-guest sandbox tenant (owner seat), and an expiry
// GuestAccountTTL from now. The guest never joins the default tenant; its
// sandbox tenant and user row are removed by the reaper after expiry.
func (s *OnboardService) CreateGuest(ctx context.Context) (*GuestAccount, error) {
	id := uuid.Must(uuid.NewV7()).String()
	githubID := constants.GuestGitHubIDPrefix + id
	// uuid v7 leads with a millisecond timestamp, so its prefix collides across
	// guests minted in the same window. Take the trailing random segment so the
	// display login stays distinct even under rapid concurrent onboarding.
	githubLogin := "guest-" + id[len(id)-12:]
	expiresAt := time.Now().Add(constants.GuestAccountTTL)

	userID, tenantID, err := s.repo.CreateGuestSandboxTenant(ctx, githubID, githubLogin, "", expiresAt)
	if err != nil {
		return nil, err
	}
	return &GuestAccount{
		UserID:      userID,
		TenantID:    tenantID,
		GitHubLogin: githubLogin,
	}, nil
}

// ListExpiredGuests returns UUIDs of guest users past their expiry.
func (s *OnboardService) ListExpiredGuests(ctx context.Context, now time.Time) ([]string, error) {
	return s.repo.ListExpiredGuests(ctx, now)
}

// ListOwnedNonDefaultTenants returns tenant IDs the user owns outside the default tenant.
func (s *OnboardService) ListOwnedNonDefaultTenants(ctx context.Context, userID string) ([]string, error) {
	return s.repo.ListOwnedNonDefaultTenants(ctx, userID)
}

// DeleteUser hard-deletes a user; FK cascades clear tenant_members and refresh_tokens.
func (s *OnboardService) DeleteUser(ctx context.Context, userID string) error {
	return s.repo.DeleteUser(ctx, userID)
}

// RegisterByUsername creates a local user and joins the default tenant.
func (s *OnboardService) RegisterByUsername(ctx context.Context, username, passwordHash string) (string, string, error) {
	return s.repo.RegisterByUsername(ctx, username, passwordHash)
}

// FindByUsername looks up a local user by username.
func (s *OnboardService) FindByUsername(ctx context.Context, username string) (string, string, string, bool, error) {
	return s.repo.FindByUsername(ctx, username)
}

// FindByUsernameWithLogin looks up a local user including their display login.
func (s *OnboardService) FindByUsernameWithLogin(ctx context.Context, username string) (string, string, string, string, bool, error) {
	return s.repo.FindByUsernameWithLogin(ctx, username)
}

// UpdateProfile updates the user's display name and/or avatar URL.
func (s *OnboardService) UpdateProfile(ctx context.Context, userID, displayName, avatarURL string) error {
	return s.repo.UpdateProfile(ctx, userID, displayName, avatarURL)
}

// FindUsernameByUserID returns the user's username (empty if not a password user).
func (s *OnboardService) FindUsernameByUserID(ctx context.Context, userID string) (string, error) {
	return s.repo.FindUsernameByUserID(ctx, userID)
}

// GetUserTenantByUserID returns the tenant a user should land in at login:
// first the non-default tenant they created (owner), else the first
// non-default tenant they joined, else the default tenant.
func (s *OnboardService) GetUserTenantByUserID(ctx context.Context, userID string) (string, string, error) {
	return s.repo.GetUserTenantByUserID(ctx, userID)
}
