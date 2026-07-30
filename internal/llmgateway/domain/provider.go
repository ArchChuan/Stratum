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
// apiKey is write-only: it is accepted on create/update but never returned.
type Provider struct {
	ID           string       `json:"id"`
	TenantID     string       `json:"-"`
	Name         string       `json:"name"`
	Kind         ProviderKind `json:"kind"`
	BaseURL      string       `json:"baseUrl"`
	APIKey       string       `json:"-"`
	DefaultModel string       `json:"defaultModel"`
	Enabled      bool         `json:"enabled"`
	CreatedAt    time.Time    `json:"createdAt"`
	UpdatedAt    time.Time    `json:"updatedAt"`
}
