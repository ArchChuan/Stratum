package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/internal/iam/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
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
	ErrEmbedModelAlreadySet  = errors.New("iam: embed_model already set and cannot be changed")
	ErrInvalidSettings       = errors.New("iam: invalid settings")
)

// TenantGatewayCache is the minimal cache invalidation interface needed by
// TenantService. Satisfied by *llmgateway.TenantGatewayCache without
// importing the infrastructure package directly.
type TenantGatewayCache interface {
	Invalidate(tenantID string)
}

// TenantService orchestrates tenant member, settings, and embed-model operations.
type TenantService struct {
	repo   port.TenantRepo
	logger *zap.Logger
	aesKey [32]byte
	cache  TenantGatewayCache
}

func NewTenantService(repo port.TenantRepo, logger *zap.Logger, aesKey [32]byte, cache TenantGatewayCache) *TenantService {
	return &TenantService{repo: repo, logger: logger, aesKey: aesKey, cache: cache}
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

// GetSettings reads tenant settings, decrypts and masks llm_api_keys.
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
	if apiKeys, ok := settings["llm_api_keys"].(map[string]interface{}); ok {
		masked := make(map[string]interface{}, len(apiKeys))
		for provider, val := range apiKeys {
			if str, ok := val.(string); ok && str != "" {
				decrypted, err := pkgcrypto.Decrypt(s.aesKey, str)
				if err == nil {
					masked[provider] = maskAPIKey(decrypted)
				} else {
					masked[provider] = ""
				}
			} else {
				masked[provider] = ""
			}
		}
		settings["llm_api_keys"] = masked
	}
	return name, isDefault, settings, nil
}

// UpdateSettings merges tenant settings, encrypting any llm_api_keys.
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

	_, _, existingJSON, _ := s.repo.GetTenantSettings(ctx, tenantID)
	merged := map[string]interface{}{}
	if len(existingJSON) > 0 {
		_ = json.Unmarshal(existingJSON, &merged)
	}

	if apiKeys, ok := req.Settings["llm_api_keys"].(map[string]interface{}); ok {
		encrypted := make(map[string]interface{}, len(apiKeys))
		for provider, val := range apiKeys {
			plaintext, ok := val.(string)
			if !ok || plaintext == "" {
				continue
			}
			// skip placeholder values sent back by the frontend (all bullet chars)
			if strings.Trim(plaintext, "•") == "" {
				continue
			}
			enc, err := pkgcrypto.Encrypt(s.aesKey, plaintext)
			if err != nil {
				s.logger.Error("encrypt api key failed", zap.String("provider", provider), zap.Error(err))
				return fmt.Errorf("tenant: encrypt api key: %w", err)
			}
			encrypted[provider] = enc
		}
		existing, _ := merged["llm_api_keys"].(map[string]interface{})
		if existing == nil {
			existing = map[string]interface{}{}
		}
		for k, v := range encrypted {
			existing[k] = v
		}
		merged["llm_api_keys"] = existing
	}

	for k, v := range req.Settings {
		if k == "llm_api_keys" {
			continue
		}
		merged[k] = v
	}

	settingsJSON, err := json.Marshal(merged)
	if err != nil {
		return ErrInvalidSettings
	}
	if err := s.repo.UpdateTenantSettings(ctx, tenantID, settingsJSON); err != nil {
		return err
	}
	if s.cache != nil {
		s.cache.Invalidate(tenantID)
	}
	return nil
}

// ListUserTenants returns all tenants the user belongs to.
func (s *TenantService) ListUserTenants(ctx context.Context, userID string) ([]domain.UserTenantInfo, error) {
	return s.repo.ListUserTenants(ctx, userID)
}

// GetMemberRole returns the role of a tenant member; ErrMemberNotFound if absent.
func (s *TenantService) GetMemberRole(ctx context.Context, tenantID, userID string) (string, error) {
	return s.repo.GetMemberRole(ctx, tenantID, userID)
}

// SetEmbedModel performs a set-once write of the embed_model setting.
func (s *TenantService) SetEmbedModel(ctx context.Context, tenantID, callerRole, embedModel string) error {
	if callerRole != "admin" && callerRole != "owner" {
		return ErrForbiddenAdminOrOwner
	}
	_, _, existingJSON, _ := s.repo.GetTenantSettings(ctx, tenantID)
	existing := map[string]interface{}{}
	if len(existingJSON) > 0 {
		_ = json.Unmarshal(existingJSON, &existing)
	}
	if v, ok := existing["embed_model"]; ok && v != "" {
		return ErrEmbedModelAlreadySet
	}
	existing["embed_model"] = embedModel
	merged, err := json.Marshal(existing)
	if err != nil {
		return fmt.Errorf("tenant: marshal embed model: %w", err)
	}
	if err := s.repo.UpdateTenantSettings(ctx, tenantID, merged); err != nil {
		return err
	}
	if s.cache != nil {
		s.cache.Invalidate(tenantID)
	}
	return nil
}

// maskAPIKey shows the first 6 chars then 8 bullets — enough to identify the key without exposing it.
func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	runes := []rune(key)
	show := 6
	if len(runes) <= show {
		show = len(runes) / 2
	}
	return string(runes[:show]) + strings.Repeat("•", 8)
}
