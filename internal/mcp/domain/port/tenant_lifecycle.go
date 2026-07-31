// Package port defines consumer-side interfaces for MCP server management.
package port

import "context"

// TenantLifecycleHook allows the MCP infrastructure to react to tenant-level
// lifecycle events such as deletion. Implementations must tear down all
// connections belonging to the tenant.
type TenantLifecycleHook interface {
	// RemoveTenant disconnects every MCP client owned by tenantID (all
	// transports), clears per-tenant connection state, and cleans up any
	// OS-level resource limits such as cgroups. It must never return an
	// error for a tenant that has no active connections.
	RemoveTenant(ctx context.Context, tenantID string) error
}
