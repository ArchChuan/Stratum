package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/platform/domain"
)

type dashboardRepoFake struct {
	overview domain.DashboardOverview
	err      error
	tenantID string
	calls    int
}

func (f *dashboardRepoFake) Overview(_ context.Context, tenantID string) (domain.DashboardOverview, error) {
	f.calls++
	f.tenantID = tenantID
	return f.overview, f.err
}

func TestDashboardServiceOverview(t *testing.T) {
	want := domain.DashboardOverview{
		Agents: 1, Skills: 2, KnowledgeWorkspaces: 3, MCPServers: 4,
		ModelProviders: 5, TenantMembers: 6, Workflows: 7, AgentUserMessages7d: 8,
	}
	repo := &dashboardRepoFake{overview: want}

	got, err := NewDashboardService(repo).Overview(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if got != want {
		t.Fatalf("Overview() = %+v, want %+v", got, want)
	}
	if repo.calls != 1 || repo.tenantID != "tenant-1" {
		t.Fatalf("repository calls=%d tenantID=%q", repo.calls, repo.tenantID)
	}
}

func TestDashboardServiceOverviewRejectsEmptyTenant(t *testing.T) {
	repo := &dashboardRepoFake{}
	_, err := NewDashboardService(repo).Overview(context.Background(), " ")
	if err == nil {
		t.Fatal("Overview() error = nil, want tenant validation error")
	}
	if repo.calls != 0 {
		t.Fatalf("repository calls=%d, want 0", repo.calls)
	}
}

func TestDashboardServiceOverviewPropagatesRepositoryError(t *testing.T) {
	wantErr := errors.New("query failed")
	_, err := NewDashboardService(&dashboardRepoFake{err: wantErr}).Overview(context.Background(), "tenant-1")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Overview() error=%v, want wrapped %v", err, wantErr)
	}
}
