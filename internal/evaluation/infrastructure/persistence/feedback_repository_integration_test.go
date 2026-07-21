//go:build integration

package persistence

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPgFeedbackRepositoryStageFeedbackReadsOnlyControlPlaneRows(t *testing.T) {
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

	tenantID := fmt.Sprintf("eval_feedback_repo_%d", time.Now().UnixNano())
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "tenant_%s" CASCADE`, tenantID)) })

	schema := `"tenant_` + tenantID + `"`
	if _, err := pool.Exec(ctx, `INSERT INTO `+schema+`.eval_suites (id, name) VALUES ('suite','suite');
		INSERT INTO `+schema+`.eval_suite_revisions
		(id, suite_id, version_no, status, resource_kind) VALUES ('suite-1','suite',1,'published','skill')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO `+schema+`.evaluation_experiments
		(id, resource_kind, resource_id, stable_revision_id, canary_revision_id, suite_revision_id, status)
		VALUES ('experiment-1','skill','skill-1','stable-1','canary-1','suite-1','running')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO `+schema+`.evaluation_feedback
		(id, trace_id, resource_kind, resource_id, revision_id, score, outcome, idempotency_key)
		VALUES ('feedback-1','trace-1','skill','skill-1','stable-1',0.8,
		        '{"security_violation":true}','key-1')`); err != nil {
		t.Fatal(err)
	}

	repo := NewPgFeedbackRepository(pool)
	feedback, _, err := repo.StageFeedback(ctx, tenantID, domain.Experiment{
		ID: "experiment-1", ResourceID: "skill-1", StableRevisionID: "stable-1", CanaryRevisionID: "canary-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(feedback) != 1 || feedback[0].RevisionID != "stable-1" ||
		feedback[0].Outcome["security_violation"] != true {
		t.Fatalf("unexpected feedback: %+v", feedback)
	}
}

func TestPgFeedbackRepositoryObservationsExcludePreviousStageFeedback(t *testing.T) {
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

	tenantID := fmt.Sprintf("eval_feedback_stage_%d", time.Now().UnixNano())
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "tenant_%s" CASCADE`, tenantID)) })
	schema := `"tenant_` + tenantID + `"`
	if _, err := pool.Exec(ctx, `INSERT INTO `+schema+`.eval_suites (id, name) VALUES ('suite','suite');
		INSERT INTO `+schema+`.eval_suite_revisions
		(id, suite_id, version_no, status, resource_kind) VALUES ('suite-1','suite',1,'published','skill');
		INSERT INTO `+schema+`.evaluation_experiments
		(id, resource_kind, resource_id, stable_revision_id, canary_revision_id, suite_revision_id, status, updated_at)
		VALUES ('experiment-1','skill','skill-1','stable-1','canary-1','suite-1','running',NOW());
		INSERT INTO `+schema+`.evaluation_feedback
		(id, trace_id, resource_kind, resource_id, revision_id, score, idempotency_key, created_at)
		VALUES ('feedback-old','trace-old','skill','skill-1','stable-1',0.8,'key-old',NOW()-INTERVAL '1 minute')`); err != nil {
		t.Fatal(err)
	}

	repo := NewPgFeedbackRepository(pool)
	feedback, _, err := repo.StageFeedback(ctx, tenantID, domain.Experiment{
		ID: "experiment-1", ResourceID: "skill-1", StableRevisionID: "stable-1", CanaryRevisionID: "canary-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(feedback) != 0 {
		t.Fatalf("expected previous-stage feedback to be excluded, got %d", len(feedback))
	}
}
