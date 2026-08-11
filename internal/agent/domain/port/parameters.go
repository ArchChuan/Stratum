package port

import "context"

// ParametersProvider resolves effective execution parameters for an agent
// resource following the two-level fallback (declared resource value →
// platform default → definition default). Declared values carry the 0=unset
// semantics of AgentConfig sampling fields. Implemented at the wiring seam
// by the parameters application service; a nil provider means no platform
// defaults participate in execution.
type ParametersProvider interface {
	ResolveForResource(ctx context.Context, declared map[string]any) (map[string]any, error)
	// Resolve returns the effective value for a single registry key
	// (declared → platform default → definition default). present=false
	// means the value resolved to unset. Used for platform-scope execution
	// toggles (e.g. trace.capture_parameters) that are not resource keys.
	Resolve(ctx context.Context, key string, declared map[string]any) (any, bool, error)
	// ValidateResource validates resource-scope declared sampling values
	// (bare JSONB keys) against registry bounds/options. Unknown keys and
	// out-of-bounds values return an error; callers skip 0=unset values
	// before invoking.
	ValidateResource(ctx context.Context, declared map[string]any) error
}
