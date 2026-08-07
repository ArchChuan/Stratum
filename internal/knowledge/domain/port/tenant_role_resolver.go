package port

import "context"

// TenantRoleResolver resolves a user's tenant role. Identical shape across
// contexts so wiring can satisfy them all with one adapter.
type TenantRoleResolver interface {
	ResolveTenantRole(context.Context, string, string) (string, error)
}
