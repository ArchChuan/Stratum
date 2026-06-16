// Package tenantdb re-exports tenant context primitives from
// pkg/storage/postgres for backwards compatibility.
//
// New code should import github.com/byteBuilderX/stratum/pkg/storage/postgres
// directly; this shim will be removed in phase 5 of the DDD refactor.
package tenantdb

import (
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

// Role is an alias for postgres.Role.
type Role = postgres.Role

// TenantContext is an alias for postgres.TenantContext.
type TenantContext = postgres.TenantContext

// Role constants re-exported from pkg/storage/postgres.
const (
	RoleTenantAdmin = postgres.RoleTenantAdmin
	RoleTenantUser  = postgres.RoleTenantUser
	RoleGlobalAdmin = postgres.RoleGlobalAdmin
)

// WithTenant re-exports postgres.WithTenant.
var WithTenant = postgres.WithTenant

// FromContext re-exports postgres.FromContext.
var FromContext = postgres.FromContext
