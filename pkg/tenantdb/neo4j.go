// Package tenantdb provides tenant database isolation and execution helpers.
//
// Neo4j naming helpers were moved to pkg/storage/tenantnaming. This file
// retains a re-export for backward compatibility and will be removed in a
// later DDD refactor phase.
package tenantdb

import "github.com/byteBuilderX/stratum/pkg/storage/tenantnaming"

// TenantLabel re-exports tenantnaming.TenantLabel.
var TenantLabel = tenantnaming.TenantLabel
