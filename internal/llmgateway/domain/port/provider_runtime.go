package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

// DiscoveredModel carries metadata discovered from a provider's model list API.
type DiscoveredModel struct {
	Name            string // e.g. "gpt-4o", "claude-sonnet-4-5"
	ContextWindow   int    // max input tokens; 0 = unknown
	MaxOutputTokens int    // max output tokens; 0 = unknown
}

// ProviderRuntime performs provider-facing operations without exposing
// infrastructure protocol types to the application layer.
type ProviderRuntime interface {
	ListModels(ctx context.Context, provider domain.Provider) ([]DiscoveredModel, error)
	Health(ctx context.Context, provider domain.Provider) error
}
