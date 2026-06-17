// Package tenantdb provides tenant database isolation and execution helpers.
//
// NATS naming helpers were moved to pkg/storage/tenantnaming. This file
// retains a re-export for backward compatibility and will be removed in a
// later DDD refactor phase.
package tenantdb

import "github.com/byteBuilderX/stratum/pkg/storage/tenantnaming"

// TenantSubject re-exports tenantnaming.TenantSubject.
var TenantSubject = tenantnaming.TenantSubject
