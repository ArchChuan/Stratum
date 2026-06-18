// Package application implements iam bounded context use-cases.
package application

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/internal/iam/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// AdminService orchestrates platform-admin tenant operations.
type AdminService struct {
	repo port.AdminTenantRepo
}

// NewAdminService wires the repository.
func NewAdminService(repo port.AdminTenantRepo) *AdminService {
	return &AdminService{repo: repo}
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
		ID:        uuid.New().String(),
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

// DeleteTenant soft-deletes the tenant row.
func (s *AdminService) DeleteTenant(ctx context.Context, id string) error {
	return s.repo.SoftDelete(ctx, id)
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
