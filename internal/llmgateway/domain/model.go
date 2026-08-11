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
	ID               string            `json:"id"`
	TenantID         string            `json:"tenantId"`
	ProviderID       string            `json:"providerId"`
	Name             string            `json:"name"`
	DisplayName      string            `json:"displayName"`
	Capabilities     []ModelCapability `json:"capabilities"`
	ContextWindow    int               `json:"contextWindow"`
	MaxTokens        int               `json:"maxTokens"`
	InputPrice       float64           `json:"inputPrice"`
	OutputPrice      float64           `json:"outputPrice"`
	Recommended      bool              `json:"recommended"`
	DefaultEmbedding bool              `json:"defaultEmbedding"`
	Enabled          bool              `json:"enabled"`
	ProviderManaged  bool              `json:"providerManaged"`
	CreatedAt        time.Time         `json:"createdAt"`
	UpdatedAt        time.Time         `json:"updatedAt"`
}
