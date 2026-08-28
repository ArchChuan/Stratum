// Package application implements iam bounded context use-cases.
package application

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/internal/iam/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// AdminService orchestrates platform-admin tenant operations.
type AdminService struct {
	repo             port.AdminTenantRepo
	userRepo         port.AdminUserRepo
	schemaCleaner    port.TenantSchemaCleaner
	vectorCleaner    port.TenantVectorCleaner
	objectCleaner    port.TenantObjectCleaner
	cacheInvalidator port.TenantCacheInvalidator
	logger           *zap.Logger
	// tenantProvisionedHook runs after ProvisionSchema succeeds on CreateTenant
	// (e.g. wiring seeds the built-in knowledge workspace for admin-created
	// tenants). The callback runs synchronously; the wiring implementation
	// spawns the async work itself.
	tenantProvisionedHook func(ctx context.Context, tenantID string)
}

// AdminServiceOption is a functional option for AdminService.
type AdminServiceOption func(*AdminService)

// WithSchemaCleaner sets the PostgreSQL schema cleaner.
func WithSchemaCleaner(c port.TenantSchemaCleaner) AdminServiceOption {
	return func(s *AdminService) { s.schemaCleaner = c }
}

// WithVectorCleaner sets the Milvus collection cleaner.
func WithVectorCleaner(c port.TenantVectorCleaner) AdminServiceOption {
	return func(s *AdminService) { s.vectorCleaner = c }
}

// WithObjectCleaner sets the MinIO object cleaner.
func WithObjectCleaner(c port.TenantObjectCleaner) AdminServiceOption {
	return func(s *AdminService) { s.objectCleaner = c }
}

// WithCacheInvalidator sets the in-process cache invalidator.
func WithCacheInvalidator(c port.TenantCacheInvalidator) AdminServiceOption {
	return func(s *AdminService) { s.cacheInvalidator = c }
}

// WithAdminLogger sets the logger.
func WithAdminLogger(l *zap.Logger) AdminServiceOption {
	return func(s *AdminService) { s.logger = l }
}

// WithUserRepo sets the platform-admin user repository.
func WithUserRepo(r port.AdminUserRepo) AdminServiceOption {
	return func(s *AdminService) { s.userRepo = r }
}

// WithTenantProvisionedHook sets a callback invoked after ProvisionSchema
// succeeds on CreateTenant. Used by wiring to seed the built-in knowledge
// workspace for admin-created tenants (the auth path is decorated in wiring via
// Platform.SchemaProvisioner). The callback runs synchronously; the wiring
// implementation spawns the async work so admin responses are not blocked.
func WithTenantProvisionedHook(fn func(ctx context.Context, tenantID string)) AdminServiceOption {
	return func(s *AdminService) { s.tenantProvisionedHook = fn }
}

// NewAdminService wires the repository and optional cleaners.
func NewAdminService(repo port.AdminTenantRepo, opts ...AdminServiceOption) *AdminService {
	svc := &AdminService{repo: repo, logger: zap.NewNop()}
	for _, o := range opts {
		o(svc)
	}
	return svc
}

// AdminListResult bundles pagination metadata for tenant list responses.
type AdminListResult struct {
	Tenants  []domain.Tenant
	Total    int
	Page     int
	PageSize int
}

// ListTenants returns a page of tenants, optionally filtered by status.
func (s *AdminService) ListTenants(ctx context.Context, filter domain.TenantFilter) (AdminListResult, error) {
	filter = normaliseFilter(filter)
	total, err := s.repo.Count(ctx, filter)
	if err != nil {
		return AdminListResult{}, err
	}
	tenants, err := s.repo.List(ctx, filter)
	if err != nil {
		return AdminListResult{}, err
	}
	return AdminListResult{
		Tenants:  tenants,
		Total:    total,
		Page:     filter.Page,
		PageSize: filter.PageSize,
	}, nil
}

// GetTenant returns a single tenant by id, or domain.ErrTenantNotFound.
func (s *AdminService) GetTenant(ctx context.Context, id string) (*domain.Tenant, error) {
	return s.repo.Get(ctx, id)
}

// CreateTenant inserts a new tenant row and provisions its schema.
func (s *AdminService) CreateTenant(ctx context.Context, actorID, name, slug, plan, status string) (*domain.Tenant, error) {
	t := domain.Tenant{
		ID:        uuid.Must(uuid.NewV7()).String(),
		Name:      name,
		Slug:      slug,
		Plan:      plan,
		Status:    status,
		CreatedAt: time.Now().UTC(),
	}
	audit, err := newPlatformAuditEvent(ctx, auditdomain.ResourceKindTenant, t.ID, auditdomain.ChangeOpCreate, actorID, nil, tenantProjection(&t))
	if err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, t, "", audit); err != nil {
		return nil, err
	}
	if err := s.repo.ProvisionSchema(ctx, t.ID); err != nil {
		return nil, err
	}
	if s.tenantProvisionedHook != nil {
		s.tenantProvisionedHook(ctx, t.ID)
	}
	return &t, nil
}

// UpdateTenant patches plan/status fields.
func (s *AdminService) UpdateTenant(ctx context.Context, actorID, id string, patch domain.TenantPatch) error {
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	after := domain.Tenant{
		ID:     t.ID,
		Name:   t.Name,
		Slug:   t.Slug,
		Plan:   nonZeroOr(patch.Plan, t.Plan),
		Status: nonZeroOr(patch.Status, t.Status),
	}
	audit, err := newPlatformAuditEvent(ctx, auditdomain.ResourceKindTenant, id, auditdomain.ChangeOpUpdate, actorID, tenantProjection(t), tenantProjection(&after))
	if err != nil {
		return err
	}
	return s.repo.UpdatePatch(ctx, id, patch, "", audit)
}

// DeleteTenant hard-deletes the tenant row (cascades to all public-schema FK tables)
// then drops the tenant PG schema and Milvus collections.
// Storage cleanup failures are logged as warnings; the public row deletion is authoritative.
func (s *AdminService) DeleteTenant(ctx context.Context, actorID, id string) error {
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	audit, err := newPlatformAuditEvent(ctx, auditdomain.ResourceKindTenant, id, auditdomain.ChangeOpDelete, actorID, tenantProjection(t), nil)
	if err != nil {
		return err
	}
	if err := s.repo.HardDelete(ctx, id, "", audit); err != nil {
		return err
	}
	// Vector cleaner must run before schema drop — it queries tenant schema for RAG workspace names.
	if s.vectorCleaner != nil {
		if err := s.vectorCleaner.DropTenantCollections(ctx, id); err != nil {
			s.logger.Warn("failed to drop tenant vector collections", zap.String("tenant_id", id), zap.Error(err))
		}
	}
	if s.objectCleaner != nil {
		if err := s.objectCleaner.DropTenantObjects(ctx, id); err != nil {
			s.logger.Warn("failed to drop tenant objects", zap.String("tenant_id", id), zap.Error(err))
		}
	}
	if s.schemaCleaner != nil {
		if err := s.schemaCleaner.DropTenantSchema(ctx, id); err != nil {
			s.logger.Warn("failed to drop tenant schema", zap.String("tenant_id", id), zap.Error(err))
		}
	}
	if s.cacheInvalidator != nil {
		s.cacheInvalidator.Invalidate(id)
	}
	return nil
}

// tenantProjection 是租户的脱敏投影（仅公开字段，无凭据）。
func tenantProjection(t *domain.Tenant) map[string]any {
	if t == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id": t.ID, "name": t.Name, "slug": t.Slug, "plan": t.Plan, "status": t.Status,
	}
}

// marshalProjection 序列化投影；nil 返回 nil（审计事件 Before/After 为 nil 时
// Normalized() 填 {}）。
func marshalProjection(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("admin audit: marshal projection: %w", err)
	}
	return b, nil
}

// newPlatformAuditEvent 构造平台审计事件：系统 actor（guest-reaper 等）覆盖为
// system/optimization；否则 actor_type=user、source 从 ctx（缺省 api）。
func newPlatformAuditEvent(ctx context.Context, kind, resourceID, operation, actorID string, before, after any) (*auditdomain.ResourceChangeAuditEvent, error) {
	ev := &auditdomain.ResourceChangeAuditEvent{
		ResourceKind: kind, ResourceID: resourceID, Operation: operation,
		ActorID: actorID, ActorType: auditdomain.ChangeActorUser,
	}
	if sysActor := reqctx.SystemActorFromContext(ctx); sysActor != "" {
		ev.ActorID = sysActor
		ev.ActorType = auditdomain.ChangeActorSystem
		ev.Source = auditdomain.ChangeSourceOptimization
	} else {
		ev.Source, _ = reqctx.ChangeSourceFromContext(ctx)
		if ev.Source == "" {
			ev.Source = auditdomain.ChangeSourceAPI
		}
	}
	var err error
	if ev.Before, err = marshalProjection(before); err != nil {
		return nil, err
	}
	if ev.After, err = marshalProjection(after); err != nil {
		return nil, err
	}
	return ev, nil
}

func nonZeroOr(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

func normaliseFilter(f domain.TenantFilter) domain.TenantFilter {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 || f.PageSize > constants.MaxPageSize {
		f.PageSize = constants.DefaultPageSize
	}
	return f
}

// SearchUsers returns non-guest, non-admin users as promotion candidates.
func (s *AdminService) SearchUsers(ctx context.Context, query string, limit int) ([]port.AdminUser, error) {
	if s.userRepo == nil {
		return nil, domain.ErrUserRepoUnavailable
	}
	return s.userRepo.SearchUsers(ctx, query, limit)
}

// ListAdmins returns all platform admins with their role.
func (s *AdminService) ListAdmins(ctx context.Context) ([]port.AdminUser, error) {
	if s.userRepo == nil {
		return nil, domain.ErrUserRepoUnavailable
	}
	return s.userRepo.ListAdmins(ctx)
}

// SetAdminRole promotes a user to system_admin. Only a global_admin actor may
// promote, and only non-guest, non-global-admin targets.
func (s *AdminService) SetAdminRole(ctx context.Context, actorID, userID string) error {
	if s.userRepo == nil {
		return domain.ErrUserRepoUnavailable
	}
	actorRole, err := s.userRepo.GetGlobalRole(ctx, actorID)
	if err != nil {
		return fmt.Errorf("set admin role: actor check: %w", err)
	}
	if actorRole != domain.GlobalRoleGlobalAdmin {
		return domain.ErrForbidden
	}
	targetRole, err := s.userRepo.GetGlobalRole(ctx, userID)
	if err != nil {
		return err
	}
	if targetRole == domain.GlobalRoleGlobalAdmin {
		return domain.ErrForbidden // never touch a super admin
	}
	audit, err := newPlatformAuditEvent(ctx, auditdomain.ResourceKindAdmin, userID, auditdomain.ChangeOpCreate, actorID, nil,
		map[string]any{"userID": userID, "role": domain.GlobalRoleSystemAdmin})
	if err != nil {
		return err
	}
	return s.userRepo.SetAdminRole(ctx, userID, "", audit)
}

// RemoveAdminRole demotes a system_admin back to user. The actor must be a
// global_admin; the target must not be one (including the actor themself).
func (s *AdminService) RemoveAdminRole(ctx context.Context, actorID, userID string) error {
	if s.userRepo == nil {
		return domain.ErrUserRepoUnavailable
	}
	actorRole, err := s.userRepo.GetGlobalRole(ctx, actorID)
	if err != nil {
		return fmt.Errorf("remove admin role: actor check: %w", err)
	}
	if actorRole != domain.GlobalRoleGlobalAdmin {
		return domain.ErrForbidden
	}
	targetRole, err := s.userRepo.GetGlobalRole(ctx, userID)
	if err != nil {
		return err
	}
	if targetRole == domain.GlobalRoleGlobalAdmin {
		return domain.ErrForbidden // never touch a super admin (incl. self)
	}
	audit, err := newPlatformAuditEvent(ctx, auditdomain.ResourceKindAdmin, userID, auditdomain.ChangeOpDelete, actorID, nil,
		map[string]any{"userID": userID, "role": domain.GlobalRoleUser})
	if err != nil {
		return err
	}
	return s.userRepo.RemoveAdminRole(ctx, userID, "", audit)
}
