package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/platform/domain"
)

type DashboardRepository interface {
	Overview(ctx context.Context, tenantID string) (domain.DashboardOverview, error)
}
