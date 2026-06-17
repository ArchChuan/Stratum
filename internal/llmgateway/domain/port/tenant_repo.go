package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

type TenantSettingsRepo interface {
	Get(ctx context.Context, tenantID string) (*domain.TenantSettings, error)
	Save(ctx context.Context, s *domain.TenantSettings) error
}
