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
	// SetDefaultEmbedding 设置或取消模型的默认嵌入标记。enabled=true 时在
	// 单事务内清除同 tenant 其他默认标记后设置目标；目标必须 enabled 且
	// capability 含 embedding（fail-closed），否则返回错误。
	SetDefaultEmbedding(ctx context.Context, tenantID, id string, enabled bool) error
}
