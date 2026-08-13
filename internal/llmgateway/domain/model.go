package domain

import (
	"errors"
	"time"
)

// ModelCapability enumerates capabilities that a model may support.
type ModelCapability string

const (
	CapChat      ModelCapability = "chat"
	CapEmbedding ModelCapability = "embedding"
	CapRerank    ModelCapability = "rerank"
	CapVision    ModelCapability = "vision"
	CapToolUse   ModelCapability = "tool_use"
	CapReasoning ModelCapability = "reasoning"
)

// ErrModelNotEmbeddingEnabled indicates the target model is disabled or lacks
// the embedding capability, so it cannot be promoted to the tenant default
// embedding model. It is a client-input mistake and must map to 4xx, never 5xx.
var ErrModelNotEmbeddingEnabled = errors.New("model is not an enabled embedding model")

// ErrModelNotFound indicates the target model does not exist for the tenant
// (or belongs to another tenant). It is a client-input mistake and must map
// to 4xx (404), never 5xx.
var ErrModelNotFound = errors.New("model not found")

// Model represents an LLM model that can be used for completions or embeddings.
type Model struct {
	ID               string            `json:"id"`
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
