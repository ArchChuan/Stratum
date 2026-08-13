package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

// ProviderRepository defines persistence operations for LLM providers.
// providers 已提升为 public 平台全局目录，方法不再携带 tenantID。
type ProviderRepository interface {
	Create(ctx context.Context, p *domain.Provider) error
	Get(ctx context.Context, id string) (*domain.Provider, error)
	// GetMeta reads a provider's metadata without decrypting its API key.
	// Used by Update so a provider whose stored key is invalid (legacy
	// plaintext or corrupted ciphertext) can still be re-saved with a new
	// key — decrypting the old key first would lock the provider out.
	GetMeta(ctx context.Context, id string) (*domain.Provider, error)
	List(ctx context.Context) ([]domain.Provider, error)
	Update(ctx context.Context, p *domain.Provider) error
	Delete(ctx context.Context, id string) error
}
