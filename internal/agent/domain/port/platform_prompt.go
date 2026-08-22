package port

import "context"

// PlatformPromptResolver resolves platform-scope prompt parameters at runtime
// (hot-update, no restart). Implemented at the wiring seam by the parameters
// application service; a nil resolver means the consuming path must fail
// closed — prompts carry no code fallback.
type PlatformPromptResolver interface {
	// ResolvePlatform returns the effective value for key. present=false means
	// the key resolved to unset (definition default applies); err signals a
	// transient resolution failure.
	ResolvePlatform(ctx context.Context, key string) (any, bool, error)
}
