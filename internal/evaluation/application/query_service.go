package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
)

const (
	defaultCenterLimit = 20
	maxCenterLimit     = 100
)

type QueryService struct{ repo port.CenterQueryRepository }

func NewQueryService(repo port.CenterQueryRepository) *QueryService { return &QueryService{repo: repo} }
func (s *QueryService) Overview(ctx context.Context, tenantID string) (domain.CenterOverview, error) {
	return s.repo.Overview(ctx, tenantID)
}

func normalizeCenterFilter(filter port.CenterFilter) (port.CenterFilter, error) {
	if filter.ResourceKind != "" && domain.ResourceKind(filter.ResourceKind).Validate() != nil {
		return filter, domain.ErrInvalidCenterQuery
	}
	allowedStatus := map[string]bool{"": true, "draft": true, "published": true, "queued": true, "running": true, "succeeded": true, "failed": true, "cancelled": true, "active": true, "paused": true, "completed": true, "stopped": true, "rolled_back": true, "proposed": true, "rejected": true}
	if !allowedStatus[filter.Status] || filter.Limit < 0 {
		return filter, domain.ErrInvalidCenterQuery
	}
	if filter.Cursor != "" {
		if _, err := domain.DecodeCenterCursor(filter.Cursor); err != nil {
			return filter, err
		}
	}
	if filter.Limit == 0 {
		filter.Limit = defaultCenterLimit
	}
	if filter.Limit > maxCenterLimit {
		filter.Limit = maxCenterLimit
	}
	return filter, nil
}

func mapCenterError(err error) error {
	if errors.Is(err, port.ErrCenterResourceNotFound) {
		return domain.ErrCenterResourceNotFound
	}
	return err
}

func (s *QueryService) ListResources(ctx context.Context, tenantID string, filter port.CenterFilter) (domain.ResourcePage, error) {
	f, e := normalizeCenterFilter(filter)
	if e != nil {
		return domain.ResourcePage{}, e
	}
	p, e := s.repo.ListResources(ctx, tenantID, f)
	return p, mapCenterError(e)
}
func (s *QueryService) ListSuites(ctx context.Context, tenantID string, filter port.CenterFilter) (domain.SuitePage, error) {
	f, e := normalizeCenterFilter(filter)
	if e != nil {
		return domain.SuitePage{}, e
	}
	p, e := s.repo.ListSuites(ctx, tenantID, f)
	return p, mapCenterError(e)
}
func (s *QueryService) ListRuns(ctx context.Context, tenantID string, filter port.CenterFilter) (domain.RunPage, error) {
	f, e := normalizeCenterFilter(filter)
	if e != nil {
		return domain.RunPage{}, e
	}
	p, e := s.repo.ListRuns(ctx, tenantID, f)
	return p, mapCenterError(e)
}
func (s *QueryService) ListCandidates(ctx context.Context, tenantID string, filter port.CenterFilter) (domain.CandidatePage, error) {
	f, e := normalizeCenterFilter(filter)
	if e != nil {
		return domain.CandidatePage{}, e
	}
	p, e := s.repo.ListCandidates(ctx, tenantID, f)
	return p, mapCenterError(e)
}
func (s *QueryService) ListExperiments(ctx context.Context, tenantID string, filter port.CenterFilter) (domain.ExperimentPage, error) {
	f, e := normalizeCenterFilter(filter)
	if e != nil {
		return domain.ExperimentPage{}, e
	}
	p, e := s.repo.ListExperiments(ctx, tenantID, f)
	return p, mapCenterError(e)
}
func (s *QueryService) Timeline(ctx context.Context, tenantID string, filter port.CenterFilter) (domain.TimelinePage, error) {
	f, e := normalizeCenterFilter(filter)
	if e != nil {
		return domain.TimelinePage{}, e
	}
	if strings.TrimSpace(f.ResourceKind) == "" || strings.TrimSpace(f.ResourceID) == "" {
		return domain.TimelinePage{}, fmt.Errorf("%w: resource required", domain.ErrInvalidCenterQuery)
	}
	p, e := s.repo.Timeline(ctx, tenantID, f)
	return p, mapCenterError(e)
}
