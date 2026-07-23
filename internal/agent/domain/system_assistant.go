package domain

const (
	SystemAssistantKey                   = "stratum.platform_assistant"
	CurrentSystemAssistantProfileVersion = "2026-07-23.v1"
)

// SystemAssistantProfile is an immutable, code-reviewed runtime definition.
// Old versions remain addressable so historical traces and rollback targets
// continue to resolve after a new version becomes active.
type SystemAssistantProfile struct {
	Key              string
	Version          string
	Name             string
	Description      string
	SystemPrompt     string
	MaxIterations    int
	MaxContextTokens int
}
