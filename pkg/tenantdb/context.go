package tenantdb

import "context"

// Role represents the access role encoded in a JWT claim.
type Role string

const (
	RoleTenantAdmin Role = "tenant_admin"
	RoleTenantUser  Role = "tenant_user"
	RoleGlobalAdmin Role = "global_admin"
)

// TenantContext carries tenant identity through the request lifecycle.
// TenantID is empty for global_admin requests.
type TenantContext struct {
	TenantID string
	UserID   string
	Role     Role
}

type ctxKey struct{}

// WithTenant returns a new context with tc embedded.
func WithTenant(ctx context.Context, tc *TenantContext) context.Context {
	return context.WithValue(ctx, ctxKey{}, tc)
}

// FromContext extracts the TenantContext from ctx.
// Returns (nil, false) if not present.
func FromContext(ctx context.Context) (*TenantContext, bool) {
	tc, ok := ctx.Value(ctxKey{}).(*TenantContext)
	return tc, ok && tc != nil
}
