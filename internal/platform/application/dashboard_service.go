package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/byteBuilderX/stratum/internal/platform/domain"
	"github.com/byteBuilderX/stratum/internal/platform/domain/port"
)

var ErrMissingTenant = errors.New("platform: tenant ID is required")

type DashboardService struct {
	repo port.DashboardRepository
}

func NewDashboardService(repo port.DashboardRepository) *DashboardService {
	return &DashboardService{repo: repo}
}

func (s *DashboardService) Overview(ctx context.Context, tenantID string) (domain.DashboardOverview, error) {
	if strings.TrimSpace(tenantID) == "" {
		return domain.DashboardOverview{}, ErrMissingTenant
	}
	overview, err := s.repo.Overview(ctx, tenantID)
	if err != nil {
		return domain.DashboardOverview{}, fmt.Errorf("platform: dashboard overview: %w", err)
	}
	return overview, nil
}
