package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

// ProviderRepository defines persistence operations for LLM providers.
type ProviderRepository interface {
	Create(ctx context.Context, tenantID string, p *domain.Provider) error
	Get(ctx context.Context, tenantID, id string) (*domain.Provider, error)
	List(ctx context.Context, tenantID string) ([]domain.Provider, error)
	Update(ctx context.Context, tenantID string, p *domain.Provider) error
	Delete(ctx context.Context, tenantID, id string) error
}
