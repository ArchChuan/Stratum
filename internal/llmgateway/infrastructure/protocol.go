package infrastructure

import "context"

// ChatProtocol defines the interface for chat-completion providers.
type ChatProtocol interface {
	Complete(ctx context.Context, cfg ProviderConfig, req *CompletionRequest) (*CompletionResponse, error)
	CompleteStream(ctx context.Context, cfg ProviderConfig, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error)
	Health(ctx context.Context, cfg ProviderConfig) error
	ListModels(ctx context.Context, cfg ProviderConfig) ([]string, error)
}

// EmbedProtocol defines the interface for embedding providers.
type EmbedProtocol interface {
	CreateEmbeddings(ctx context.Context, cfg ProviderConfig, req *EmbeddingRequest) (*EmbeddingResponse, error)
	BatchSize() int
}
