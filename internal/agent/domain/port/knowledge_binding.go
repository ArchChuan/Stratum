package port

import "context"

// WorkspaceBindingValidator verifies that knowledge workspace names resolve
// in the tenant. Defined by the consuming context (agent) and implemented by
// the wiring composition root against knowledge/application — agent never
// imports knowledge.
//
// Fail closed: an un-wired validator rejects every binding, and an unknown
// workspace name rejects the operation with the offending name in the error.
type WorkspaceBindingValidator interface {
	ValidateWorkspaceBindings(ctx context.Context, tenantID string, workspaceNames []string) error
}
