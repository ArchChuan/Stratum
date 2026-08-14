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
	tenantID := "eval_repo_test"
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "tenant_%s" CASCADE`, tenantID)); err != nil {
			t.Logf("cleanup tenant %s: %v", tenantID, err)
		}
		pool.Close()
	})

	repo := NewPgSuiteRepository(pool)
	suite := domain.EvalSuite{ID: "suite-1", Name: "基线", DraftRevisionID: "suite-rev-1"}
	revision := domain.EvalSuiteRevision{
		ID: "suite-rev-1", SuiteID: suite.ID, Status: domain.SuiteRevisionDraft, ResourceKind: domain.ResourceKindSkill,
		Cases: []domain.EvalCase{
			{ID: "case-1", Name: "用例", Input: "输入", ExpectedOutput: "输出", AssertionMode: domain.AssertionExact, Enabled: true},
			{ID: "case-2", Name: "法官用例", Input: "问题", AssertionMode: domain.AssertionJudge, Enabled: true,
				JudgeSpec: &domain.JudgeSpec{Model: "qwen-max", Rubric: "自定义 rubric"}},
		},
	}
	if err := repo.CreateSuite(ctx, tenantID, suite, revision); err != nil {
		t.Fatal(err)
	}
	published, err := repo.PublishRevision(ctx, tenantID, suite.ID, revision.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if published.Status != domain.SuiteRevisionPublished || len(published.Cases) != 2 {
		t.Fatalf("unexpected published revision: %+v", published)
	}
	var judgeCase *domain.EvalCase
	for i := range published.Cases {
		if published.Cases[i].ID == "case-2" {
			judgeCase = &published.Cases[i]
		}
	}
	if judgeCase == nil || judgeCase.AssertionMode != domain.AssertionJudge || judgeCase.JudgeSpec == nil {
		t.Fatalf("judge case lost on round-trip: %+v", published.Cases)
	}
	if judgeCase.JudgeSpec.Model != "qwen-max" || judgeCase.JudgeSpec.Rubric != "自定义 rubric" {
		t.Fatalf("judge spec mismatch on round-trip: %+v", judgeCase.JudgeSpec)
	}

	// 矩阵评测 seed 复用路径：active revision 必须是发布态且携带完整 cases。
	active, found, err := repo.GetActiveRevision(ctx, tenantID, suite.ID)
	if err != nil || !found {
		t.Fatalf("GetActiveRevision: found=%v err=%v", found, err)
	}
	if active.ID != revision.ID || active.Status != domain.SuiteRevisionPublished || len(active.Cases) != 2 {
		t.Fatalf("unexpected active revision: %+v", active)
	}

	// 从未发布的套件 found=false。
	neverPublished := domain.EvalSuite{ID: "suite-2", Name: "未发布基线", DraftRevisionID: "suite-rev-2"}
	draftRev := domain.EvalSuiteRevision{
		ID: "suite-rev-2", SuiteID: "suite-2", Status: domain.SuiteRevisionDraft,
		ResourceKind: domain.ResourceKindSkill,
		Cases:        []domain.EvalCase{{ID: "case-3", Name: "未发布", Input: "x", AssertionMode: domain.AssertionContains, Enabled: true}},
	}
	if err := repo.CreateSuite(ctx, tenantID, neverPublished, draftRev); err != nil {
		t.Fatal(err)
	}
	if _, found, err := repo.GetActiveRevision(ctx, tenantID, "suite-2"); err != nil || found {
		t.Fatalf("expected no active revision for unpublished suite: found=%v err=%v", found, err)
	}
}
