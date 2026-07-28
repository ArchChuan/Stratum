package domain

import "time"

// ProviderKind enumerates supported LLM provider categories.
type ProviderKind string

const (
	ProviderOpenAICompat ProviderKind = "openai_compat"
	ProviderAnthropic    ProviderKind = "anthropic"
	ProviderOllama       ProviderKind = "ollama"
)

// Provider represents a configured LLM provider instance.
type Provider struct {
	ID           string
	TenantID     string
	Name         string
	Kind         ProviderKind
	BaseURL      string
	APIKey       string
	DefaultModel string
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
