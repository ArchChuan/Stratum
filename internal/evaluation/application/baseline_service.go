package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

type BaselineCreator interface {
	CreatePublishedBaseline(context.Context, string, domain.ResourceKind, string) (domain.ResourceRef, error)
}

type BaselineService struct {
	creator BaselineCreator
}

func NewBaselineService(creator BaselineCreator) *BaselineService {
	return &BaselineService{creator: creator}
}

func (s *BaselineService) CreatePublishedBaseline(
	ctx context.Context, tenantID string, kind domain.ResourceKind, resourceID string,
) (domain.ResourceRef, error) {
	if s == nil || s.creator == nil {
		return domain.ResourceRef{}, errors.New("evaluation baseline service unavailable")
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(resourceID) == "" {
		return domain.ResourceRef{}, errors.New("evaluation baseline tenant and resource required")
	}
	if err := kind.Validate(); err != nil {
		return domain.ResourceRef{}, err
	}
	ref, err := s.creator.CreatePublishedBaseline(ctx, tenantID, kind, resourceID)
	if err != nil {
		return domain.ResourceRef{}, fmt.Errorf("evaluation baseline: create published revision: %w", err)
	}
	return ref, nil
}
