package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/internal/iam/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// UpdateSettingsInput carries the application-level shape of PATCH /tenant/settings.
type UpdateSettingsInput struct {
	Name     string
	Settings map[string]interface{}
}

// Application-level sentinel errors returned by TenantService.
var (
	ErrForbiddenAdminOrOwner = errors.New("iam: admin or owner role required")
	ErrForbiddenOwner        = errors.New("iam: owner role required")
	ErrForbiddenSelfModify   = errors.New("iam: cannot modify your own role/membership")
	ErrForbiddenOwnerRole    = errors.New("iam: cannot change owner's role")
	ErrForbiddenRemoveOwner  = errors.New("iam: cannot remove owner")
	ErrForbiddenAdminRemove  = errors.New("iam: admin cannot remove another admin")
	ErrInvalidSettings       = errors.New("iam: invalid settings")
	ErrInvalidRoleFilter     = errors.New("iam: invalid role filter")
)

// TenantService orchestrates tenant member and settings operations.
type TenantService struct {
	repo port.TenantRepo
}

func NewTenantService(repo port.TenantRepo, _ *zap.Logger) *TenantService {
	return &TenantService{repo: repo}
}

// ListMembers returns a paginated list of members; page/pageSize are normalized.
func (s *TenantService) ListMembers(ctx context.Context, tenantID string, page, pageSize int) ([]domain.Member, int, int, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > constants.MaxPageSize {
		pageSize = constants.DefaultPageSize
	}
	offset := (page - 1) * pageSize

	total, err := s.repo.CountMembers(ctx, tenantID)
	if err != nil {
		return nil, 0, page, pageSize, fmt.Errorf("tenant: list members count: %w", err)
	}
	members, err := s.repo.ListMembers(ctx, tenantID, pageSize, offset)
	if err != nil {
		return nil, 0, page, pageSize, fmt.Errorf("tenant: list members: %w", err)
	}
	return members, total, page, pageSize, nil
}

// ListMembersByRole returns every member holding one of the given roles.
// Only admin/owner are valid filters; the returned list is the candidate
// set for "resource editors" (no pagination — the caller wants all).
func (s *TenantService) ListMembersByRole(ctx context.Context, tenantID string, roles []string) ([]domain.Member, error) {
	if len(roles) == 0 {
		return nil, ErrInvalidRoleFilter
	}
	seen := make(map[string]bool, len(roles))
	for _, r := range roles {
		if r != "admin" && r != "owner" {
			return nil, ErrInvalidRoleFilter
		}
		seen[r] = true
	}
	allowed := make([]string, 0, len(seen))
	for r := range seen {
		allowed = append(allowed, r)
	}
	members, err := s.repo.ListMembersByRole(ctx, tenantID, allowed)
	if err != nil {
		return nil, fmt.Errorf("tenant: list members by role: %w", err)
	}
	return members, nil
}

// UpdateMemberRole changes a member's role with full permission rules.
func (s *TenantService) UpdateMemberRole(ctx context.Context, tenantID, callerID, callerRole, targetUserID, newRole string) error {
	if callerRole != "owner" {
		return ErrForbiddenOwner
	}
	if callerID == targetUserID {
		return ErrForbiddenSelfModify
	}
	targetRole, err := s.repo.GetMemberRole(ctx, tenantID, targetUserID)
	if err != nil {
		// Preserve original handler behavior: any lookup error → 404 not found.
		return domain.ErrMemberNotFound
	}
	if targetRole == "owner" {
		return ErrForbiddenOwnerRole
	}
	return s.repo.UpdateMemberRole(ctx, tenantID, targetUserID, newRole)
}

// RemoveMember deletes a member with full permission rules.
func (s *TenantService) RemoveMember(ctx context.Context, tenantID, callerID, callerRole, targetUserID string) error {
	if callerRole != "owner" && callerRole != "admin" {
		return ErrForbiddenAdminOrOwner
	}
	if callerID == targetUserID {
		return ErrForbiddenSelfModify
	}
	targetRole, err := s.repo.GetMemberRole(ctx, tenantID, targetUserID)
	if err != nil {
		// Preserve original handler behavior: any lookup error → 404 not found.
		return domain.ErrMemberNotFound
	}
	if targetRole == "owner" {
		return ErrForbiddenRemoveOwner
	}
	if callerRole == "admin" && targetRole == "admin" {
		return ErrForbiddenAdminRemove
	}
	return s.repo.DeleteMember(ctx, tenantID, targetUserID)
}

// GetSettings reads tenant settings. Legacy model credentials are never exposed.
func (s *TenantService) GetSettings(ctx context.Context, tenantID string) (string, bool, map[string]interface{}, error) {
	name, isDefault, raw, err := s.repo.GetTenantSettings(ctx, tenantID)
	if err != nil {
		return "", false, nil, err
	}
	settings := map[string]interface{}{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return "", false, nil, fmt.Errorf("tenant: settings unmarshal: %w", err)
		}
	}
	delete(settings, "llm_api_keys")
	return name, isDefault, settings, nil
}

// UpdateSettings merges ordinary tenant settings. Model credentials are managed by llmgateway providers.
// Caller-side role enforcement is required (callerRole must be admin or owner).
func (s *TenantService) UpdateSettings(ctx context.Context, tenantID, callerRole string, req UpdateSettingsInput) error {
	if callerRole != "admin" && callerRole != "owner" {
		return ErrForbiddenAdminOrOwner
	}

	if req.Name != "" {
		if err := s.repo.UpdateTenantName(ctx, tenantID, req.Name); err != nil {
			return err
		}
	}

	if req.Settings == nil {
		return nil
	}
	if _, legacyModelConfig := req.Settings["llm_api_keys"]; legacyModelConfig {
		return ErrInvalidSettings
	}

	merged, err := s.readSettingsBaseline(ctx, tenantID)
	if err != nil {
		return err
	}

	for k, v := range req.Settings {
		merged[k] = v
	}
	delete(merged, "llm_api_keys")

	settingsJSON, err := json.Marshal(merged)
	if err != nil {
		return ErrInvalidSettings
	}
	if err := s.repo.UpdateTenantSettings(ctx, tenantID, settingsJSON); err != nil {
		return err
	}
	return nil
}

// readSettingsBaseline loads the stored tenant settings and parses them into
// a merge baseline. Fail closed: a read failure or corrupt stored JSON must
// never be replaced by an empty baseline.
func (s *TenantService) readSettingsBaseline(ctx context.Context, tenantID string) (map[string]interface{}, error) {
	_, _, existingJSON, err := s.repo.GetTenantSettings(ctx, tenantID)
	if err != nil {
		// fail closed: 读取失败不得以空基线覆盖现有设置
		return nil, fmt.Errorf("tenant: read settings: %w", err)
	}
	merged := map[string]interface{}{}
	if len(existingJSON) > 0 {
		if err := json.Unmarshal(existingJSON, &merged); err != nil {
			// fail closed: 损坏的存量 JSON 不得被空基线替换
			return nil, fmt.Errorf("tenant: settings unmarshal: %w", err)
		}
	}
	return merged, nil
}

// ListUserTenants returns all tenants the user belongs to.
func (s *TenantService) ListUserTenants(ctx context.Context, userID string) ([]domain.UserTenantInfo, error) {
	return s.repo.ListUserTenants(ctx, userID)
}

// GetMemberRole returns the role of a tenant member; ErrMemberNotFound if absent.
func (s *TenantService) GetMemberRole(ctx context.Context, tenantID, userID string) (string, error) {
	return s.repo.GetMemberRole(ctx, tenantID, userID)
}
