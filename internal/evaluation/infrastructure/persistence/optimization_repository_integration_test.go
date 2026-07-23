//go:build integration

package persistence_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	evalpersist "github.com/byteBuilderX/stratum/internal/evaluation/infrastructure/persistence"
	skilldomain "github.com/byteBuilderX/stratum/internal/skill/domain"
	skillpersist "github.com/byteBuilderX/stratum/internal/skill/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPgOptimizationRepositoryIdempotencyRollbackAndTenantIsolation(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL not set; optimization repository integration test requires PostgreSQL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const tenantID = "optimization_atomic_test"
	const otherTenantID = "optimization_atomic_other"
	for _, id := range []string{tenantID, otherTenantID} {
		if _, err := pool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "tenant_%s" CASCADE`, id)); err != nil {
			t.Fatal(err)
		}
		if err := postgres.ProvisionTenantSchema(ctx, pool, id); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, id := range []string{tenantID, otherTenantID} {
			_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS "tenant_%s" CASCADE`, id))
		}
	})
	seedOptimizationDependencies(t, ctx, pool, tenantID)

	repo := evalpersist.NewPgOptimizationRepository(pool)
	job := integrationOptimizationJob("job-1", "suite-revision-1")
	candidates := []domain.OptimizationCandidate{integrationOptimizationCandidate("candidate-1", job.ID, "revision-1")}
	created, err := repo.SaveJobWithCandidates(ctx, tenantID, job, candidates, "request-1", "fingerprint-1")
	if err != nil || !created {
		t.Fatalf("first save: created=%v err=%v", created, err)
	}
	replayed, err := repo.SaveJobWithCandidates(ctx, tenantID, integrationOptimizationJob("job-2", "suite-revision-1"),
		candidates, "request-1", "fingerprint-1")
	if err != nil || replayed {
		t.Fatalf("same payload replay: created=%v err=%v", replayed, err)
	}
	if _, err := repo.SaveJobWithCandidates(ctx, tenantID, integrationOptimizationJob("job-3", "suite-revision-1"),
		candidates, "request-1", "different"); !errors.Is(err, domain.ErrOptimizationIdempotencyConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	got, gotCandidates, fingerprint, found, err := repo.GetByIdempotencyKey(ctx, tenantID, "request-1")
	if err != nil || !found || got.ID != job.ID || len(gotCandidates) != 1 || fingerprint != "fingerprint-1" {
		t.Fatalf("unexpected replay read: job=%+v candidates=%+v fingerprint=%q found=%v err=%v",
			got, gotCandidates, fingerprint, found, err)
	}
	if _, _, _, found, err := repo.GetByIdempotencyKey(ctx, otherTenantID, "request-1"); err != nil || found {
		t.Fatalf("cross-tenant idempotency lookup leaked: found=%v err=%v", found, err)
	}
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			concurrentJob := integrationOptimizationJob(fmt.Sprintf("concurrent-job-%d", index), "suite-revision-1")
			created, err := repo.SaveJobWithCandidates(ctx, tenantID, concurrentJob,
				[]domain.OptimizationCandidate{integrationOptimizationCandidate(
					fmt.Sprintf("concurrent-candidate-%d", index), concurrentJob.ID, fmt.Sprintf("revision-%d", index),
				)}, "concurrent-request", "concurrent-fingerprint")
			results <- created
			errs <- err
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)
	createdCount := 0
	for created := range results {
		if created {
			createdCount++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent save failed: %v", err)
		}
	}
	if createdCount != 1 {
		t.Fatalf("concurrent idempotency created %d jobs, want 1", createdCount)
	}

	skillRepo := skillpersist.NewPgSkillRevisionRepo(pool)
	orphan := skilldomain.SkillRevision{
		ID: "orphan-candidate", SkillID: "skill-1", ParentRevisionID: "published-1",
		Status: skilldomain.VersionStatusCandidate, Source: "llm_rewrite", ContentHash: "candidate-hash",
		GenerationMetadata: map[string]any{}, Instructions: "candidate",
	}
	err = repo.WithinTransaction(ctx, tenantID, func(txCtx context.Context) error {
		if err := skillRepo.InsertCandidate(txCtx, orphan); err != nil {
			return err
		}
		_, err := repo.SaveJobWithCandidates(txCtx, tenantID,
			integrationOptimizationJob("failed-job", "missing-suite"),
			[]domain.OptimizationCandidate{integrationOptimizationCandidate("failed-candidate", "failed-job", orphan.ID)},
			"failed-request", "failed-fingerprint")
		return err
	})
	if err == nil {
		t.Fatal("expected foreign key failure")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM tenant_optimization_atomic_test.skill_revisions WHERE id=$1`, orphan.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed optimization left %d orphan Skill revisions", count)
	}
}

func seedOptimizationDependencies(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID string) {
	t.Helper()
	schema := "tenant_" + tenantID
	statements := []string{
		`INSERT INTO ` + schema + `.skills(id,name,description,status,active_revision_id) VALUES('skill-1','skill','skill','published','published-1')`,
		`INSERT INTO ` + schema + `.skill_revisions(id,skill_id,status,source,content_hash,generation_metadata,capability,activation_contract,instructions,requirements,publish_checks) VALUES('published-1','skill-1','published','manual','hash','{}','{}','{}','baseline','{}','{}')`,
		`INSERT INTO ` + schema + `.eval_suites(id,name) VALUES('suite-1','suite')`,
		`INSERT INTO ` + schema + `.eval_suite_revisions(id,suite_id,version_no,status,resource_kind) VALUES('suite-revision-1','suite-1',1,'published','skill')`,
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
}

func integrationOptimizationJob(id, suiteRevisionID string) domain.OptimizationJob {
	return domain.OptimizationJob{
		ID: id, Baseline: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "published-1"},
		SuiteRevisionID: suiteRevisionID, Status: domain.JobSucceeded, SearchSpace: map[string][]any{},
		FailureSummaries: []string{"failure"}, CreatedAt: time.Now().UTC(),
	}
}

func integrationOptimizationCandidate(id, jobID, revisionID string) domain.OptimizationCandidate {
	return domain.OptimizationCandidate{
		ID: id, OptimizationJobID: jobID,
		Revision:         domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: revisionID},
		ParentRevisionID: "published-1", Source: "llm_rewrite", GenerationMetadata: map[string]any{}, CreatedAt: time.Now().UTC(),
	}
}
