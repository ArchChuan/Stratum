package domain

import (
	"errors"
	"time"
)

// ErrUpstreamRequestFailed indicates the provider's API returned an unexpected
// HTTP status on an operational endpoint (model discovery, health check).
// The wrapping error carries diagnostic details: provider name, URL, status code.
var ErrUpstreamRequestFailed = errors.New("upstream provider request failed")

// ErrStreamTruncated indicates a streaming response ended before the
// provider's termination marker ([DONE]/finish_reason, message_stop,
// done:true) after content had already been emitted — the answer is
// incomplete and must not be treated as success.
var ErrStreamTruncated = errors.New("stream truncated before completion marker")

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
	Name         string       `json:"name"`
	Kind         ProviderKind `json:"kind"`
	BaseURL      string       `json:"baseUrl"`
	APIKey       string       `json:"-"`
	DefaultModel string       `json:"defaultModel"`
	Enabled      bool         `json:"enabled"`
	CreatedAt    time.Time    `json:"createdAt"`
	UpdatedAt    time.Time    `json:"updatedAt"`
}
