package domain

import "time"

// ModelCapability enumerates capabilities that a model may support.
type ModelCapability string

const (
	CapChat      ModelCapability = "chat"
	CapEmbedding ModelCapability = "embedding"
	CapVision    ModelCapability = "vision"
	CapToolUse   ModelCapability = "tool_use"
	CapReasoning ModelCapability = "reasoning"
)

// Model represents an LLM model that can be used for completions or embeddings.
type Model struct {
	ID              string
	TenantID        string
	ProviderID      string
	Name            string
	DisplayName     string
	Capabilities    []ModelCapability
	ContextWindow   int
	MaxTokens       int
	InputPrice      float64
	OutputPrice     float64
	Recommended     bool
	Enabled         bool
	ProviderManaged bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
