package port

import "context"

// ResourceParamResolver resolves a resource-scope parameter value for a
// tenant's agent (declared agent value → platform default → definition
// default). memory.* resource params are bound to the agent resource and read
// per execution, so tenant and agent identity are explicit in the call.
// Implemented at the wiring seam; a nil resolver keeps the pkg/constants
// defaults so unit tests and degraded startups behave as before.
type ResourceParamResolver interface {
	// Resolve returns the effective value for key. present=false means the
	// key resolved to unset (definition default applies); err signals a
	// transient resolution failure that the caller should fall back from
	// rather than propagate into the pipeline.
	Resolve(ctx context.Context, tenantID, agentID, key string) (any, bool, error)
}
