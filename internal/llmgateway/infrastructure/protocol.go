package infrastructure

import "context"

// DiscoveredModel carries metadata discovered from a provider's model list API.
type DiscoveredModel struct {
	Name            string // e.g. "gpt-4o", "claude-sonnet-4-5"
	ContextWindow   int    // total context tokens; 0 = unknown
	MaxOutputTokens int    // max output tokens; 0 = unknown
}

// ChatProtocol defines the interface for chat-completion providers.
type ChatProtocol interface {
	Complete(ctx context.Context, cfg ProviderConfig, req *CompletionRequest) (*CompletionResponse, error)
	CompleteStream(ctx context.Context, cfg ProviderConfig, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error)
	Health(ctx context.Context, cfg ProviderConfig) error
	ListModels(ctx context.Context, cfg ProviderConfig) ([]DiscoveredModel, error)
}

// EmbedProtocol defines the interface for embedding providers.
type EmbedProtocol interface {
	CreateEmbeddings(ctx context.Context, cfg ProviderConfig, req *EmbeddingRequest) (*EmbeddingResponse, error)
	BatchSize() int
}
