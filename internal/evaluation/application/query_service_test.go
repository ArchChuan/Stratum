package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
)

func TestQueryServiceNormalizesLimitsAndDelegates(t *testing.T) {
	repo := &queryRepoStub{}
	svc := NewQueryService(repo)

	if _, err := svc.ListResources(context.Background(), "tenant-1", port.CenterFilter{}); err != nil {
		t.Fatal(err)
	}
	if repo.filter.Limit != 20 {
		t.Fatalf("default limit = %d, want 20", repo.filter.Limit)
	}
	if _, err := svc.ListResources(context.Background(), "tenant-1", port.CenterFilter{Limit: 999}); err != nil {
		t.Fatal(err)
	}
	if repo.filter.Limit != 100 {
		t.Fatalf("maximum limit = %d, want 100", repo.filter.Limit)
	}
}

func TestQueryServiceRejectsInvalidFilters(t *testing.T) {
	svc := NewQueryService(&queryRepoStub{})
	tests := []port.CenterFilter{
		{ResourceKind: "invalid"},
		{Status: "invalid"},
		{Cursor: "not-a-cursor"},
		{Limit: -1},
	}
	for _, filter := range tests {
		if _, err := svc.ListResources(context.Background(), "tenant-1", filter); !errors.Is(err, domain.ErrInvalidCenterQuery) {
			t.Errorf("filter %+v error = %v, want invalid center query", filter, err)
		}
	}
}

func TestQueryServiceTimelineRequiresResourceAndHidesNotFound(t *testing.T) {
	repo := &queryRepoStub{}
	svc := NewQueryService(repo)
	if _, err := svc.Timeline(context.Background(), "tenant-1", port.CenterFilter{ResourceKind: "skill"}); !errors.Is(err, domain.ErrInvalidCenterQuery) {
		t.Fatalf("missing resource error = %v", err)
	}
	repo.err = port.ErrCenterResourceNotFound
	if _, err := svc.Timeline(context.Background(), "tenant-1", port.CenterFilter{
		ResourceKind: "skill", ResourceID: "same-id",
	}); !errors.Is(err, domain.ErrCenterResourceNotFound) {
		t.Fatalf("not found error = %v", err)
	}
}

type queryRepoStub struct {
	filter port.CenterFilter
	err    error
}

func (r *queryRepoStub) Overview(context.Context, string) (domain.CenterOverview, error) {
	return domain.CenterOverview{}, r.err
}
func (r *queryRepoStub) ListResources(_ context.Context, _ string, filter port.CenterFilter) (domain.ResourcePage, error) {
	r.filter = filter
	return domain.ResourcePage{}, r.err
}
func (r *queryRepoStub) ListSuites(context.Context, string, port.CenterFilter) (domain.SuitePage, error) {
	return domain.SuitePage{}, r.err
}
func (r *queryRepoStub) ListRuns(context.Context, string, port.CenterFilter) (domain.RunPage, error) {
	return domain.RunPage{}, r.err
}
func (r *queryRepoStub) ListCandidates(context.Context, string, port.CenterFilter) (domain.CandidatePage, error) {
	return domain.CandidatePage{}, r.err
}
func (r *queryRepoStub) ListExperiments(context.Context, string, port.CenterFilter) (domain.ExperimentPage, error) {
	return domain.ExperimentPage{}, r.err
}
func (r *queryRepoStub) Timeline(_ context.Context, _ string, filter port.CenterFilter) (domain.TimelinePage, error) {
	r.filter = filter
	return domain.TimelinePage{}, r.err
}
