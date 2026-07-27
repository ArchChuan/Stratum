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
		if _, err := repo.ApplyCommand(ctx, tenantID, experiment.ID, domain.CommandPromote,
			commandFor("promote-paused", retry.StateVersion)); !errors.Is(err, domain.ErrExperimentCommandNotAllowed) {
			t.Fatalf("paused promotion error=%v", err)
		}
		pausedEvaluation := retry
		pausedEvaluation.StateVersion++
		securityMetrics := domain.StageMetrics{SecurityViolation: true}
		if _, _, err := repo.SaveDecision(ctx, tenantID, pausedEvaluation, domain.DecisionRollback,
			securityMetrics, "paused-evaluation", domain.MetricsFingerprint(securityMetrics)); !errors.Is(err, domain.ErrExperimentCommandNotAllowed) {
			t.Fatalf("paused evaluation error=%v", err)
		}
		var pausedVersion int64
		var pausedPercent, pausedAudits int
		if err := pool.QueryRow(ctx, "SELECT state_version FROM "+schema+".evaluation_experiments WHERE id=$1",
			experiment.ID).Scan(&pausedVersion); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, "SELECT canary_percent FROM "+schema+".evaluation_deployments WHERE experiment_id=$1",
			experiment.ID).Scan(&pausedPercent); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+schema+".experiment_decisions WHERE experiment_id=$1",
			experiment.ID).Scan(&pausedAudits); err != nil {
			t.Fatal(err)
		}
		if pausedVersion != retry.StateVersion || pausedPercent != 0 || pausedAudits != 1 {
			t.Fatalf("paused evaluation mutated state: version=%d percent=%d audits=%d",
				pausedVersion, pausedPercent, pausedAudits)
		}
		rolledBack, err := repo.ApplyCommand(ctx, tenantID, experiment.ID, domain.CommandRollback,
			commandFor("rollback-paused", retry.StateVersion))
		if err != nil || rolledBack.Status != domain.ExperimentRolledBack {
			t.Fatalf("paused rollback experiment=%+v err=%v", rolledBack, err)
		}
		replayedPause, err := repo.ApplyCommand(ctx, tenantID, experiment.ID, domain.CommandPause,
			commandFor(winningKey, experiment.StateVersion))
		if err != nil || replayedPause.Status != domain.ExperimentPaused || replayedPause.StateVersion != retry.StateVersion {
			t.Fatalf("pause replay returned later state: replay=%+v err=%v", replayedPause, err)
		}
		var percent int
		if err := pool.QueryRow(ctx, `SELECT canary_percent FROM `+schema+`.evaluation_deployments
			WHERE resource_id=$1`, experiment.ResourceID).Scan(&percent); err != nil || percent != 0 {
			t.Fatalf("paused deployment percent=%d err=%v", percent, err)
		}
	})

	t.Run("promote rollback safety stop and tenant isolation", func(t *testing.T) {
		promote := createCommandExperiment(t, ctx, repo, tenantID, "promote", domain.DecisionPromote)
		duplicate := promote
		duplicate.ID = "experiment-promote-duplicate"
		if err := repo.Create(ctx, tenantID, duplicate, domain.Deployment{
			ResourceKind: duplicate.ResourceKind, ResourceID: duplicate.ResourceID,
			StableRevisionID: duplicate.StableRevisionID, CanaryRevisionID: duplicate.CanaryRevisionID,
			CanaryPercent: duplicate.Stage, ExperimentID: duplicate.ID,
		}); !errors.Is(err, domain.ErrExperimentDeploymentConflict) {
			t.Fatalf("active deployment conflict error=%v", err)
		}
		if _, err := repo.ApplyCommand(ctx, tenantID, promote.ID, domain.CommandPromote,
			commandFor("promote-1", promote.StateVersion)); err != nil {
			t.Fatal(err)
		}
		assertDeployment(t, ctx, pool, tenantID, promote.ResourceID, promote.CanaryRevisionID, "", 0)
		assertPromotedSkillLifecycle(t, ctx, pool, tenantID, promote)

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
		stored, storedDecision, err := repo.SaveDecision(ctx, tenantID, next, recommendation,
			domain.StageMetrics{SecurityViolation: true}, "safety-evaluation", domain.MetricsFingerprint(
				domain.StageMetrics{SecurityViolation: true},
			))
		if err != nil {
			t.Fatal(err)
		}
		retryStored, retryDecision, err := repo.SaveDecision(ctx, tenantID, next, recommendation,
			domain.StageMetrics{SecurityViolation: true}, "safety-evaluation", domain.MetricsFingerprint(
				domain.StageMetrics{SecurityViolation: true},
			))
		if err != nil || retryStored.StateVersion != stored.StateVersion || retryDecision != storedDecision {
			t.Fatalf("evaluation retry stored=%+v decision=%s err=%v", retryStored, retryDecision, err)
		}
		if _, _, err := repo.SaveDecision(ctx, tenantID, next, recommendation,
			domain.StageMetrics{SecurityViolation: true}, "safety-evaluation", "different"); !errors.Is(err, domain.ErrExperimentCommandConflict) {
			t.Fatalf("evaluation fingerprint conflict error=%v", err)
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

	t.Run("missing owned deployment rolls back command", func(t *testing.T) {
		experiment := createCommandExperiment(t, ctx, repo, tenantID, "missing-deployment", domain.DecisionPromote)
		schema := `"tenant_` + tenantID + `"`
		if _, err := pool.Exec(ctx, `DELETE FROM `+schema+`.evaluation_deployments WHERE experiment_id=$1`, experiment.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.ApplyCommand(ctx, tenantID, experiment.ID, domain.CommandPromote,
			commandFor("missing-deployment", experiment.StateVersion)); !errors.Is(err, domain.ErrExperimentStateConflict) {
			t.Fatalf("missing deployment error=%v", err)
		}
		stored, found, err := repo.Get(ctx, tenantID, experiment.ID)
		if err != nil || !found || stored.Status != domain.ExperimentRunning || stored.StateVersion != experiment.StateVersion {
			t.Fatalf("command transaction did not roll back: stored=%+v found=%v err=%v", stored, found, err)
		}
	})

	for _, kind := range []domain.ResourceKind{domain.ResourceKindAgent, domain.ResourceKindMCP, domain.ResourceKindKnowledge} {
		kind := kind
		t.Run("promoted "+string(kind)+" starts the next evolution cycle", func(t *testing.T) {
			experiment, nextCanary, suiteRevisionID := seedGenericPromotionCycle(
				t, ctx, pool, tenantID, kind,
			)
			if err := repo.Create(ctx, tenantID, experiment, domain.Deployment{
				ResourceKind: kind, ResourceID: experiment.ResourceID,
				StableRevisionID: experiment.StableRevisionID, CanaryRevisionID: experiment.CanaryRevisionID,
				CanaryPercent: experiment.Stage, ExperimentID: experiment.ID, PolicyVersion: 1,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.ApplyCommand(ctx, tenantID, experiment.ID, domain.CommandPromote,
				commandFor("promote-"+string(kind), experiment.StateVersion)); err != nil {
				t.Fatal(err)
			}
			assertGenericPromotion(t, ctx, pool, tenantID, experiment)
			if err := repo.ValidatePrerequisites(ctx, tenantID, domain.ResourceRef{
				Kind: kind, ResourceID: experiment.ResourceID, RevisionID: experiment.CanaryRevisionID,
			}, nextCanary, suiteRevisionID); err != nil {
				t.Fatalf("promoted revision cannot start the next cycle: %v", err)
			}
		})
	}
}

func seedGenericPromotionCycle(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID string, kind domain.ResourceKind,
) (domain.Experiment, domain.ResourceRef, string) {
	t.Helper()
	schema := `"tenant_` + tenantID + `"`
	label := string(kind)
	resourceID := label + "-cycle-resource"
	stableID, canaryID, nextID := label+"-stable", label+"-canary", label+"-next-canary"
	suiteID, suiteRevisionID := label+"-suite", label+"-suite-revision"
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO ` + schema + `.eval_suites(id,name) VALUES($1,$2)`, []any{suiteID, label + " suite"}},
		{`INSERT INTO ` + schema + `.eval_suite_revisions(id,suite_id,version_no,status,resource_kind)
			VALUES($1,$2,1,'published',$3)`, []any{suiteRevisionID, suiteID, label}},
		{`INSERT INTO ` + schema + `.resource_revisions
			(id,resource_kind,resource_id,source,status,content_hash,payload_hash,payload_ref,safe_summary)
			VALUES($1,$4,$5,'manual','published','stable','stable','test://stable','{}'),
			      ($2,$4,$5,'optimization','draft','canary','canary','test://canary','{}'),
			      ($3,$4,$5,'optimization','draft','next','next','test://next','{}')`,
			[]any{stableID, canaryID, nextID, label, resourceID}},
		{`INSERT INTO ` + schema + `.optimization_jobs
			(id,resource_kind,resource_id,baseline_revision_id,suite_revision_id,status)
			VALUES($1,$3,$4,$5,$6,'succeeded'),($2,$3,$4,$7,$6,'succeeded')`,
			[]any{label + "-job", label + "-next-job", label, resourceID, stableID, suiteRevisionID, canaryID}},
		{`INSERT INTO ` + schema + `.optimization_candidates
			(id,optimization_job_id,revision_id,parent_revision_id,source)
			VALUES($1,$2,$3,$4,'rewrite'),($5,$6,$7,$3,'rewrite')`,
			[]any{label + "-candidate", label + "-job", canaryID, stableID,
				label + "-next-candidate", label + "-next-job", nextID}},
		{`INSERT INTO ` + schema + `.eval_runs
			(id,resource_kind,resource_id,revision_id,suite_revision_id,status,passed)
			VALUES($1,$2,$3,$4,$5,'succeeded',true)`,
			[]any{label + "-next-run", label, resourceID, nextID, suiteRevisionID}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	experiment := domain.Experiment{
		ID: label + "-cycle-experiment", ResourceKind: kind, ResourceID: resourceID,
		StableRevisionID: stableID, CanaryRevisionID: canaryID, SuiteRevisionID: suiteRevisionID,
		Status: domain.ExperimentRunning, Stage: 5, Policy: domain.DefaultPromotionPolicy(),
		StateVersion: 1, Recommendation: domain.DecisionPromote,
	}
	return experiment, domain.ResourceRef{Kind: kind, ResourceID: resourceID, RevisionID: nextID}, suiteRevisionID
}

func assertGenericPromotion(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID string, experiment domain.Experiment,
) {
	t.Helper()
	schema := `"tenant_` + tenantID + `"`
	var revisionStatus, candidateStatus string
	err := pool.QueryRow(ctx, `SELECT r.status,c.status FROM `+schema+`.resource_revisions r
		JOIN `+schema+`.optimization_candidates c ON c.revision_id=r.id
		WHERE r.id=$1 AND r.resource_kind=$2 AND r.resource_id=$3`, experiment.CanaryRevisionID,
		string(experiment.ResourceKind), experiment.ResourceID).Scan(&revisionStatus, &candidateStatus)
	if err != nil || revisionStatus != "published" || candidateStatus != "promoted" {
		t.Fatalf("promoted lifecycle revision=%q candidate=%q err=%v", revisionStatus, candidateStatus, err)
	}
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
	seedCommandExperimentRevisions(t, ctx, repo.pool, tenantID, experiment)
	if err := repo.Create(ctx, tenantID, experiment, deployment); err != nil {
		t.Fatal(err)
	}
	return experiment
}

func seedCommandExperimentRevisions(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID string, experiment domain.Experiment,
) {
	t.Helper()
	schema := `"tenant_` + tenantID + `"`
	_, err := pool.Exec(ctx, `INSERT INTO `+schema+`.skills
		(id,name,description,status,active_revision_id) VALUES ($1,$2,'test','published',$3)`,
		experiment.ResourceID, "skill "+experiment.ResourceID, experiment.StableRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO `+schema+`.skill_revisions
		(id,skill_id,status,source,content_hash,generation_metadata,capability,activation_contract,
		 instructions,requirements,publish_checks)
		VALUES ($2,$1,'published','manual','stable-hash','{}','{}','{}','stable','{}','{}'),
		       ($3,$1,'candidate','optimization','canary-hash','{}','{}','{}','canary','{}','{}')`,
		experiment.ResourceID, experiment.StableRevisionID, experiment.CanaryRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO `+schema+`.optimization_jobs
		(id,resource_kind,resource_id,baseline_revision_id,suite_revision_id,status)
		VALUES ($1,'skill',$2,$3,'suite-revision','succeeded')`,
		"job-"+experiment.ID, experiment.ResourceID, experiment.StableRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO `+schema+`.optimization_candidates
		(id,optimization_job_id,revision_id,parent_revision_id,source)
		VALUES ($1,$2,$3,$4,'rewrite')`, "candidate-"+experiment.ID, "job-"+experiment.ID,
		experiment.CanaryRevisionID, experiment.StableRevisionID)
	if err != nil {
		t.Fatal(err)
	}
}

func assertPromotedSkillLifecycle(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID string, experiment domain.Experiment,
) {
	t.Helper()
	schema := `"tenant_` + tenantID + `"`
	var activeRevisionID, revisionStatus, candidateStatus string
	err := pool.QueryRow(ctx, `SELECT s.active_revision_id,r.status,c.status
		FROM `+schema+`.skills s
		JOIN `+schema+`.skill_revisions r ON r.id=$2 AND r.skill_id=s.id
		JOIN `+schema+`.optimization_candidates c ON c.revision_id=r.id
		WHERE s.id=$1`, experiment.ResourceID, experiment.CanaryRevisionID).Scan(
		&activeRevisionID, &revisionStatus, &candidateStatus,
	)
	if err != nil || activeRevisionID != experiment.CanaryRevisionID || revisionStatus != "published" ||
		candidateStatus != "promoted" {
		t.Fatalf("promoted lifecycle active=%q revision=%q candidate=%q err=%v",
			activeRevisionID, revisionStatus, candidateStatus, err)
	}
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
