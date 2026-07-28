package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

// ModelFilter carries optional filter criteria for listing models.
type ModelFilter struct {
	Capability domain.ModelCapability
	ProviderID string
	Enabled    *bool
}

// ModelRepository defines persistence operations for LLM models.
type ModelRepository interface {
	Create(ctx context.Context, tenantID string, m *domain.Model) error
	Get(ctx context.Context, tenantID, id string) (*domain.Model, error)
	List(ctx context.Context, tenantID string, filter ModelFilter) ([]domain.Model, error)
	Update(ctx context.Context, tenantID string, m *domain.Model) error
	UpsertDiscovered(ctx context.Context, tenantID, providerID string, models []domain.Model) ([]domain.Model, error)
	Delete(ctx context.Context, tenantID, id string) error
	Toggle(ctx context.Context, tenantID, id string, enabled bool) error
}
