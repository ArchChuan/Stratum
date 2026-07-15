//go:build integration

package persistence

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPgSuiteRepositoryCreatePublishAndLoad(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tenantID := "eval_repo_test"
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "tenant_%s" CASCADE`, tenantID)) })

	repo := NewPgSuiteRepository(pool)
	suite := domain.EvalSuite{ID: "suite-1", Name: "基线", DraftRevisionID: "suite-rev-1"}
	revision := domain.EvalSuiteRevision{
		ID: "suite-rev-1", SuiteID: suite.ID, Status: domain.SuiteRevisionDraft, ResourceKind: domain.ResourceKindSkill,
		Cases: []domain.EvalCase{{ID: "case-1", Name: "用例", Input: "输入", ExpectedOutput: "输出", AssertionMode: domain.AssertionExact, Enabled: true}},
	}
	if err := repo.CreateSuite(ctx, tenantID, suite, revision); err != nil {
		t.Fatal(err)
	}
	published, err := repo.PublishRevision(ctx, tenantID, suite.ID, revision.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if published.Status != domain.SuiteRevisionPublished || len(published.Cases) != 1 {
		t.Fatalf("unexpected published revision: %+v", published)
	}
}
