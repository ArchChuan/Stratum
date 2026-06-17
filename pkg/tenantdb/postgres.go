// Package tenantdb re-exports tenant exec helpers from pkg/storage/postgres.
//
// New code should import github.com/byteBuilderX/stratum/pkg/storage/postgres
// directly; this shim will be removed in phase 5 of the DDD refactor.
package tenantdb

import (
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

// ExecTenant re-exports postgres.ExecTenant.
var ExecTenant = postgres.ExecTenant
