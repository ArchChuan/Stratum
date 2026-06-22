// Package application implements iam bounded context use-cases.
package application

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/internal/iam/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// AdminService orchestrates platform-admin tenant operations.
type AdminService struct {
	repo             port.AdminTenantRepo
	schemaCleaner    port.TenantSchemaCleaner
	vectorCleaner    port.TenantVectorCleaner
	cacheInvalidator port.TenantCacheInvalidator
	logger           *zap.Logger
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

// WithCacheInvalidator sets the in-process cache invalidator.
func WithCacheInvalidator(c port.TenantCacheInvalidator) AdminServiceOption {
	return func(s *AdminService) { s.cacheInvalidator = c }
}

// WithAdminLogger sets the logger.
func WithAdminLogger(l *zap.Logger) AdminServiceOption {
	return func(s *AdminService) { s.logger = l }
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
func (s *AdminService) CreateTenant(ctx context.Context, name, slug, plan, status string) (*domain.Tenant, error) {
	t := domain.Tenant{
		ID:        uuid.Must(uuid.NewV7()).String(),
		Name:      name,
		Slug:      slug,
		Plan:      plan,
		Status:    status,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.repo.Create(ctx, t); err != nil {
		return nil, err
	}
	if err := s.repo.ProvisionSchema(ctx, t.ID); err != nil {
		return nil, err
	}
	return &t, nil
}

// UpdateTenant patches plan/status fields.
func (s *AdminService) UpdateTenant(ctx context.Context, id string, patch domain.TenantPatch) error {
	return s.repo.UpdatePatch(ctx, id, patch)
}

// DeleteTenant soft-deletes the tenant row and best-effort cleans all associated storage.
// PG schema and Milvus collection failures are logged as warnings and do not abort the operation;
// the soft-delete is the authoritative gate against new writes.
func (s *AdminService) DeleteTenant(ctx context.Context, id string) error {
	if err := s.repo.SoftDelete(ctx, id); err != nil {
		return err
	}
	if s.schemaCleaner != nil {
		if err := s.schemaCleaner.DropTenantSchema(ctx, id); err != nil {
			s.logger.Warn("failed to drop tenant schema", zap.String("tenant_id", id), zap.Error(err))
		}
	}
	if s.vectorCleaner != nil {
		if err := s.vectorCleaner.DropTenantCollections(ctx, id); err != nil {
			s.logger.Warn("failed to drop tenant vector collections", zap.String("tenant_id", id), zap.Error(err))
		}
	}
	if s.cacheInvalidator != nil {
		s.cacheInvalidator.Invalidate(id)
	}
	return nil
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
