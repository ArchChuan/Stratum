package constants

const (
	// MaxPlatformConfigVersionsPerGroup caps total versions (draft +
	// published + archived) for a single platform config group. Reaching
	// the cap auto-archives the oldest published version (explicitly
	// auditable) without blocking new drafts. Platform-specific cap —
	// do not reuse MaxPromptVersionsPerKey across domains.
	MaxPlatformConfigVersionsPerGroup = 100
)
