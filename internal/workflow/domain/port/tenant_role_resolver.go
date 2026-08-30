package port

import "context"

// TenantRoleResolver resolves a tenant member's role (owner/admin/member).
// Single source of truth injected by wiring; resolution failure fails closed
// in the ownership matrix.
type TenantRoleResolver interface {
	ResolveTenantRole(context.Context, string, string) (string, error)
}
