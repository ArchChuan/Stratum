package port

import (
	"context"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
)

// AdminTenantRepo handles platform-admin tenant CRUD against public.tenants.
// Distinct from TenantRepo (which is per-tenant member/settings work).
type AdminTenantRepo interface {
	Count(ctx context.Context, filter domain.TenantFilter) (int, error)
	List(ctx context.Context, filter domain.TenantFilter) ([]domain.Tenant, error)
	Get(ctx context.Context, id string) (*domain.Tenant, error)
	Create(ctx context.Context, t domain.Tenant, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error
	UpdatePatch(ctx context.Context, id string, patch domain.TenantPatch, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error
	HardDelete(ctx context.Context, id string, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error
	ProvisionSchema(ctx context.Context, tenantID string) error
}
