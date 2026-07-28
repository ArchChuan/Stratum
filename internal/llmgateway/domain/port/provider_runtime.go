package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

// ProviderRuntime performs provider-facing operations without exposing
// infrastructure protocol types to the application layer.
type ProviderRuntime interface {
	ListModels(ctx context.Context, provider domain.Provider) ([]string, error)
	Health(ctx context.Context, provider domain.Provider) error
}
