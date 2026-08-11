package port

import "context"

// WorkspaceBindingValidator verifies that knowledge workspace IDs resolve
// in the tenant. Defined by the consuming context (skill) and implemented by
// the wiring composition root against knowledge/application — skill never
// imports knowledge.
//
// Fail closed: an un-wired validator rejects every binding, and an unknown
// workspace ID rejects the operation with the offending ID in the error.
type WorkspaceBindingValidator interface {
	ValidateWorkspaceBindings(ctx context.Context, tenantID string, workspaceIDs []string) error
}
