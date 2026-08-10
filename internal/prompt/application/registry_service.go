package application

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/prompt/domain"
	"github.com/byteBuilderX/stratum/internal/prompt/domain/port"
)

// RegistryService manages prompt template lifecycle: create, publish, rollback, resolve.
type RegistryService struct {
	prompts  port.PromptRepo
	bindings port.BindingRepo
}

// NewRegistryService creates a prompt registry service.
func NewRegistryService(prompts port.PromptRepo, bindings port.BindingRepo) *RegistryService {
	return &RegistryService{prompts: prompts, bindings: bindings}
}

// CreateTemplate inserts a new prompt as draft. The key+tenantID pair uniquely
// identifies the prompt family; each call increments the version counter.
func (s *RegistryService) CreateTemplate(ctx context.Context, key string, tenantID *string, content, createdBy string) (*domain.PromptTemplate, error) {
	versions, err := s.prompts.GetByKey(ctx, key, tenantID)
	if err != nil {
		return nil, fmt.Errorf("prompt: create: %w", err)
	}
	nextVersion := 1
	for _, v := range versions {
		if v.Version >= nextVersion {
			nextVersion = v.Version + 1
		}
	}
	tmpl := domain.PromptTemplate{
		Key:         key,
		TenantID:    tenantID,
		Version:     nextVersion,
		Content:     content,
		Status:      domain.PromptDraft,
		ContentHash: domain.ComputeHash(content),
		CreatedBy:   createdBy,
		CreatedAt:   time.Now(),
	}
	if err := s.prompts.Insert(ctx, tmpl); err != nil {
		return nil, fmt.Errorf("prompt: create: %w", err)
	}
	return &tmpl, nil
}

// PublishVersion promotes a draft to published (immutable). Only one version
// of a key+tenant pair is published at a time; previously published versions
// are archived.
func (s *RegistryService) PublishVersion(ctx context.Context, key string, version int, tenantID *string) error {
	tmpl, err := s.prompts.GetVersion(ctx, key, version, tenantID)
	if err != nil {
		return fmt.Errorf("prompt: publish: %w", err)
	}
	if tmpl == nil {
		return fmt.Errorf("prompt: version %d not found", version)
	}
	if tmpl.Status != domain.PromptDraft {
		return fmt.Errorf("prompt: only draft can be published, current status: %s", tmpl.Status)
	}
	// Archive previously published versions.
	existing, _ := s.prompts.GetByKey(ctx, key, tenantID)
	for _, v := range existing {
		if v.Status == domain.PromptPublished {
			_ = s.prompts.UpdateStatus(ctx, key, v.Version, tenantID, domain.PromptArchived)
		}
	}
	return s.prompts.UpdateStatus(ctx, key, version, tenantID, domain.PromptPublished)
}

// ListTemplates returns the latest version of every prompt key for a tenant
// (nil = global) with the total key count, for the admin list endpoint.
func (s *RegistryService) ListTemplates(ctx context.Context, tenantID *string, page, pageSize int) ([]domain.PromptTemplate, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	tmpls, total, err := s.prompts.ListByKey(ctx, tenantID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("prompt: list templates: %w", err)
	}
	return tmpls, total, nil
}

// GetVersions returns all versions for a key+tenant pair.
func (s *RegistryService) GetVersions(ctx context.Context, key string, tenantID *string) ([]domain.PromptTemplate, error) {
	versions, err := s.prompts.GetByKey(ctx, key, tenantID)
	if err != nil {
		return nil, fmt.Errorf("prompt: get versions: %w", err)
	}
	return versions, nil
}

// Rollback creates a new version with the content of an older published version
// and publishes it immediately. This preserves the audit trail.
func (s *RegistryService) Rollback(ctx context.Context, key string, targetVersion int, tenantID *string, createdBy string) (*domain.PromptTemplate, error) {
	src, err := s.prompts.GetVersion(ctx, key, targetVersion, tenantID)
	if err != nil || src == nil {
		return nil, fmt.Errorf("prompt: rollback: source version %d not found", targetVersion)
	}
	return s.CreateTemplate(ctx, key, tenantID, src.Content, createdBy)
}

// GetEffectivePrompt resolves the prompt text for a key given scope
// (agent > tenant > global priority).
func (s *RegistryService) GetEffectivePrompt(ctx context.Context, key, tenantID, agentID, requestID string) (string, error) {
	// 1. Try agent-scoped binding first.
	if agentID != "" {
		if text, found := s.resolveBinding(ctx, key, "agent:"+agentID, requestID); found {
			return text, nil
		}
	}
	// 2. Try tenant-scoped binding.
	if tenantID != "" {
		if text, found := s.resolveBinding(ctx, key, "tenant:"+tenantID, requestID); found {
			return text, nil
		}
	}
	// 3. Fall back to global published version.
	var globalTenant *string
	tmpl, err := s.prompts.GetLatestPublished(ctx, key, globalTenant)
	if err != nil || tmpl == nil {
		return "", fmt.Errorf("prompt: no published version for key %q", key)
	}
	return tmpl.Content, nil
}

// resolveBinding loads the binding for key+scope and applies A/B split.
func (s *RegistryService) resolveBinding(ctx context.Context, key, scope, requestID string) (string, bool) {
	binding, err := s.bindings.GetBinding(ctx, key, scope)
	if err != nil || binding == nil {
		return "", false
	}
	// Determine which version to serve based on A/B hash.
	versionID := binding.StableVersionID
	if binding.CanaryVersionID != "" && binding.TrafficPercent > 0 {
		if resolveAB(requestID, binding.TrafficPercent) {
			versionID = binding.CanaryVersionID
		}
	}
	// Look up the prompt by content hash (version IDs are content hashes).
	tmpl, err := s.prompts.GetByHash(ctx, versionID)
	if err != nil || tmpl == nil {
		return "", false
	}
	return tmpl.Content, true
}
