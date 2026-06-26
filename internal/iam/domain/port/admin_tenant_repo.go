package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
)

// AdminTenantRepo handles platform-admin tenant CRUD against public.tenants.
// Distinct from TenantRepo (which is per-tenant member/settings work).
type AdminTenantRepo interface {
	Count(ctx context.Context, filter domain.TenantFilter) (int, error)
	List(ctx context.Context, filter domain.TenantFilter) ([]domain.Tenant, error)
	Get(ctx context.Context, id string) (*domain.Tenant, error)
	Create(ctx context.Context, t domain.Tenant) error
	UpdatePatch(ctx context.Context, id string, patch domain.TenantPatch) error
	HardDelete(ctx context.Context, id string) error
	ProvisionSchema(ctx context.Context, tenantID string) error
}
