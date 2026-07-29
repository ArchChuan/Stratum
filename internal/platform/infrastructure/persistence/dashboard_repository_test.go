package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/pashagolub/pgxmock/v2"

	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

func dashboardContext(tenantID string) context.Context {
	return pgstore.WithTenant(context.Background(), &pgstore.TenantContext{TenantID: tenantID})
}

func TestDashboardRepositoryOverview(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT.*FROM agents.*FROM skills.*FROM rag_workspaces.*FROM mcp_configs.*FROM providers.*public.tenant_members.*FROM workflow_definitions.*FROM chat_messages").
		WithArgs("tenant-1").
		WillReturnRows(pgxmock.NewRows([]string{"agents", "skills", "knowledge", "mcp", "providers", "members", "workflows", "messages"}).
			AddRow(1, 2, 3, 4, 5, 6, 7, 8))
	pool.ExpectCommit()

	got, err := (&DashboardRepository{pool: pool}).Overview(dashboardContext("tenant-1"), "tenant-1")
	if err != nil {
		t.Fatalf("Overview() error=%v", err)
	}
	if got.Agents != 1 || got.Skills != 2 || got.KnowledgeWorkspaces != 3 || got.MCPServers != 4 ||
		got.ModelProviders != 5 || got.TenantMembers != 6 || got.Workflows != 7 || got.AgentUserMessages7d != 8 {
		t.Fatalf("Overview()=%+v", got)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDashboardRepositoryOverviewRejectsTenantMismatch(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	_, err = (&DashboardRepository{pool: pool}).Overview(dashboardContext("tenant-2"), "tenant-1")
	if err == nil {
		t.Fatal("Overview() error=nil, want tenant mismatch")
	}
}

func TestDashboardRepositoryOverviewRollsBackQueryFailure(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	wantErr := errors.New("query failed")
	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT").WithArgs("tenant-1").WillReturnError(wantErr)
	pool.ExpectRollback()

	_, err = (&DashboardRepository{pool: pool}).Overview(dashboardContext("tenant-1"), "tenant-1")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Overview() error=%v, want wrapped %v", err, wantErr)
	}
}
