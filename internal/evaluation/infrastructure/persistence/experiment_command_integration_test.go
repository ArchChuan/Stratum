//go:build integration

package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPgExperimentRepositoryHumanGates(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL not set; experiment command integration test requires a real tenant database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	tenantID := fmt.Sprintf("eval_commands_%d", time.Now().UnixNano())
	otherTenantID := tenantID + "_other"
	for _, id := range []string{tenantID, otherTenantID} {
		if err := postgres.ProvisionTenantSchema(ctx, pool, id); err != nil {
			t.Fatal(err)
		}
		id := id
		t.Cleanup(func() { _, _ = pool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "tenant_%s" CASCADE`, id)) })
	}
	repo := NewPgExperimentRepository(pool)
	seedExperimentSuite(t, ctx, pool, tenantID)
	seedExperimentSuite(t, ctx, pool, otherTenantID)

	t.Run("concurrent command and idempotent retry", func(t *testing.T) {
		experiment := createCommandExperiment(t, ctx, repo, tenantID, "concurrent", domain.DecisionPromote)
		commands := []domain.ExperimentCommand{
			commandFor("command-a", experiment.StateVersion),
			commandFor("command-b", experiment.StateVersion),
		}
		errs := make(chan error, len(commands))
		var wg sync.WaitGroup
		for _, command := range commands {
			command := command
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := repo.ApplyCommand(ctx, tenantID, experiment.ID, domain.CommandPause, command)
				errs <- err
			}()
		}
		wg.Wait()
		close(errs)
		var succeeded, conflicted int
		for err := range errs {
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, domain.ErrExperimentStateConflict):
				conflicted++
			default:
				t.Fatalf("unexpected concurrent command error: %v", err)
			}
		}
		if succeeded != 1 || conflicted != 1 {
			t.Fatalf("succeeded=%d conflicted=%d", succeeded, conflicted)
		}

		var winningKey string
		schema := `"tenant_` + tenantID + `"`
		if err := pool.QueryRow(ctx, `SELECT idempotency_key FROM `+schema+`.experiment_decisions
			WHERE experiment_id=$1`, experiment.ID).Scan(&winningKey); err != nil {
			t.Fatal(err)
		}
		retry, err := repo.ApplyCommand(ctx, tenantID, experiment.ID, domain.CommandPause,
			commandFor(winningKey, experiment.StateVersion))
		if err != nil || retry.Status != domain.ExperimentPaused {
			t.Fatalf("idempotent retry failed: experiment=%+v err=%v", retry, err)
		}
		conflictingRetry := commandFor(winningKey, experiment.StateVersion)
		conflictingRetry.Reason = "changed reason"
		if _, err := repo.ApplyCommand(ctx, tenantID, experiment.ID, domain.CommandPause, conflictingRetry); !errors.Is(err, domain.ErrExperimentCommandConflict) {
			t.Fatalf("changed idempotent command error=%v", err)
		}
		if _, err := repo.ApplyCommand(ctx, tenantID, experiment.ID, domain.CommandRollback,
			commandFor("after-terminal", retry.StateVersion)); !errors.Is(err, domain.ErrExperimentCommandNotAllowed) {
			t.Fatalf("terminal command error=%v", err)
		}
		var percent int
		if err := pool.QueryRow(ctx, `SELECT canary_percent FROM `+schema+`.evaluation_deployments
			WHERE resource_id=$1`, experiment.ResourceID).Scan(&percent); err != nil || percent != 0 {
			t.Fatalf("paused deployment percent=%d err=%v", percent, err)
		}
	})

	t.Run("promote rollback safety stop and tenant isolation", func(t *testing.T) {
		promote := createCommandExperiment(t, ctx, repo, tenantID, "promote", domain.DecisionPromote)
		if _, err := repo.ApplyCommand(ctx, tenantID, promote.ID, domain.CommandPromote,
			commandFor("promote-1", promote.StateVersion)); err != nil {
			t.Fatal(err)
		}
		assertDeployment(t, ctx, pool, tenantID, promote.ResourceID, promote.CanaryRevisionID, "", 0)

		rollback := createCommandExperiment(t, ctx, repo, tenantID, "rollback", domain.DecisionRollback)
		if _, err := repo.ApplyCommand(ctx, tenantID, rollback.ID, domain.CommandRollback,
			commandFor("rollback-1", rollback.StateVersion)); err != nil {
			t.Fatal(err)
		}
		assertDeployment(t, ctx, pool, tenantID, rollback.ResourceID, rollback.StableRevisionID, "", 0)

		safety := createCommandExperiment(t, ctx, repo, tenantID, "safety", domain.DecisionHold)
		policy := safety.Policy
		next, recommendation := safety.Decide(domain.StageMetrics{SecurityViolation: true}, policy)
		next.StateVersion = safety.StateVersion + 1
		if err := repo.SaveDecision(ctx, tenantID, next, recommendation, domain.StageMetrics{SecurityViolation: true}); err != nil {
			t.Fatal(err)
		}
		assertDeployment(t, ctx, pool, tenantID, safety.ResourceID, safety.StableRevisionID, safety.CanaryRevisionID, 0)
		schema := `"tenant_` + tenantID + `"`
		var action string
		if err := pool.QueryRow(ctx, `SELECT action FROM `+schema+`.experiment_decisions
			WHERE experiment_id=$1`, safety.ID).Scan(&action); err != nil || action != "safety_stop" {
			t.Fatalf("safety audit action=%q err=%v", action, err)
		}

		if _, err := repo.ApplyCommand(ctx, otherTenantID, promote.ID, domain.CommandRollback,
			commandFor("isolated", promote.StateVersion)); err == nil {
			t.Fatal("command crossed tenant boundary")
		}
	})
}

func seedExperimentSuite(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID string) {
	t.Helper()
	schema := `"tenant_` + tenantID + `"`
	_, err := pool.Exec(ctx, `INSERT INTO `+schema+`.eval_suites (id, name) VALUES ('suite','suite');
		INSERT INTO `+schema+`.eval_suite_revisions
		(id, suite_id, version_no, status, resource_kind) VALUES ('suite-revision','suite',1,'published','skill')`)
	if err != nil {
		t.Fatal(err)
	}
}

func createCommandExperiment(
	t *testing.T, ctx context.Context, repo *PgExperimentRepository, tenantID, suffix string, recommendation domain.Decision,
) domain.Experiment {
	t.Helper()
	policy := domain.DefaultPromotionPolicy()
	experiment := domain.Experiment{
		ID: "experiment-" + suffix, ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-" + suffix,
		StableRevisionID: "stable-" + suffix, CanaryRevisionID: "canary-" + suffix,
		SuiteRevisionID: "suite-revision", Status: domain.ExperimentRunning, Stage: 5, Policy: policy,
		StateVersion: 1, Recommendation: recommendation,
	}
	deployment := domain.Deployment{
		ResourceKind: domain.ResourceKindSkill, ResourceID: experiment.ResourceID,
		StableRevisionID: experiment.StableRevisionID, CanaryRevisionID: experiment.CanaryRevisionID,
		CanaryPercent: 5, ExperimentID: experiment.ID, PolicyVersion: 1,
	}
	if err := repo.Create(ctx, tenantID, experiment, deployment); err != nil {
		t.Fatal(err)
	}
	return experiment
}

func commandFor(key string, version int64) domain.ExperimentCommand {
	return domain.ExperimentCommand{
		ActorID: "admin-1", ActorType: domain.ActorTypeAdmin, Reason: "reviewed",
		IdempotencyKey: key, ExpectedStateVersion: version,
	}
}

func assertDeployment(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, resourceID, stable, canary string, percent int,
) {
	t.Helper()
	schema := `"tenant_` + tenantID + `"`
	var gotStable, gotCanary string
	var gotPercent int
	err := pool.QueryRow(ctx, `SELECT stable_revision_id, COALESCE(canary_revision_id,''), canary_percent
		FROM `+schema+`.evaluation_deployments WHERE resource_id=$1`, resourceID).Scan(&gotStable, &gotCanary, &gotPercent)
	if err != nil || gotStable != stable || gotCanary != canary || gotPercent != percent {
		t.Fatalf("deployment stable=%q canary=%q percent=%d err=%v", gotStable, gotCanary, gotPercent, err)
	}
}
