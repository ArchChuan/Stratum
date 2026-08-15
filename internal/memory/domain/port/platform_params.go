package port

import "context"

// PlatformParamResolver resolves a platform-scope parameter value for
// cross-agent background workers (enrich / session summary / history
// summary / supersede). Workers are tenant-agnostic batch consumers, so the
// resolution carries no tenant/agent identity — declared=nil means the pure
// platform value or the definition default. Implemented at the wiring seam; a
// nil resolver keeps the pkg/constants defaults so unit tests and degraded
// startups behave as before.
type PlatformParamResolver interface {
	// ResolvePlatform returns the effective value for key. present=false means
	// the key resolved to unset (definition default applies); err signals a
	// transient resolution failure that the caller should fall back from
	// rather than propagate into the pipeline.
	ResolvePlatform(ctx context.Context, key string) (any, bool, error)
}
