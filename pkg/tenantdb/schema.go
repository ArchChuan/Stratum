// Package tenantdb re-exports schema provisioning helpers from
// pkg/storage/postgres.
//
// New code should import github.com/byteBuilderX/stratum/pkg/storage/postgres
// directly; this shim will be removed in phase 5 of the DDD refactor.
package tenantdb

import (
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

// ProvisionTenantSchema re-exports postgres.ProvisionTenantSchema.
var ProvisionTenantSchema = postgres.ProvisionTenantSchema

// ProvisionAllTenantSchemas re-exports postgres.ProvisionAllTenantSchemas.
var ProvisionAllTenantSchemas = postgres.ProvisionAllTenantSchemas

// EnsureDefaultTenant re-exports postgres.EnsureDefaultTenant.
var EnsureDefaultTenant = postgres.EnsureDefaultTenant

// ListTenantSchemas re-exports postgres.ListTenantSchemas.
var ListTenantSchemas = postgres.ListTenantSchemas

// ProvisionPublicSchema re-exports postgres.ProvisionPublicSchema.
var ProvisionPublicSchema = postgres.ProvisionPublicSchema
