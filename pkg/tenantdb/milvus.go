// Package tenantdb provides tenant database isolation and execution helpers.
//
// Milvus naming helpers were moved to pkg/storage/tenantnaming. This file
// retains re-exports for backward compatibility and will be removed in a
// later DDD refactor phase.
package tenantdb

import "github.com/byteBuilderX/stratum/pkg/storage/tenantnaming"

// TenantCollection re-exports tenantnaming.TenantCollection.
var TenantCollection = tenantnaming.TenantCollection

// WorkspaceCollection re-exports tenantnaming.WorkspaceCollection.
var WorkspaceCollection = tenantnaming.WorkspaceCollection
