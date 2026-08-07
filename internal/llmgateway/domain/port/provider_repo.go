package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

// ProviderRepository defines persistence operations for LLM providers.
type ProviderRepository interface {
	Create(ctx context.Context, tenantID string, p *domain.Provider) error
	Get(ctx context.Context, tenantID, id string) (*domain.Provider, error)
	// GetMeta reads a provider's metadata without decrypting its API key.
	// Used by Update so a provider whose stored key is invalid (legacy
	// plaintext or corrupted ciphertext) can still be re-saved with a new
	// key — decrypting the old key first would lock the provider out.
	GetMeta(ctx context.Context, tenantID, id string) (*domain.Provider, error)
	List(ctx context.Context, tenantID string) ([]domain.Provider, error)
	Update(ctx context.Context, tenantID string, p *domain.Provider) error
	Delete(ctx context.Context, tenantID, id string) error
}
