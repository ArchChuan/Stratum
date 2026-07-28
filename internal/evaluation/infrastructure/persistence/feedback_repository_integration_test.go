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

	"github.com/byteBuilderX/stratum/internal/evaluation/application"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPgFeedbackRepositoryRejectsChangedReplay(t *testing.T) {
	pool, ctx, tenantID := feedbackRepositoryTestPool(t, "changed_replay")
	repo := NewPgFeedbackRepository(pool)
	original := domain.FeedbackRequest{
		TraceID: "trace-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
		RevisionID: "revision-1", Score: 1, Outcome: map[string]any{"label": "good"},
		IdempotencyKey: "feedback-1",
	}
	stored, err := repo.Record(ctx, tenantID, original)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		input domain.FeedbackRequest
	}{
		{name: "same trace with another key", input: domain.FeedbackRequest{
			TraceID: "trace-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
			RevisionID: "revision-1", Score: 0, Outcome: map[string]any{"label": "bad"},
			IdempotencyKey: "feedback-2",
		}},
		{name: "same key with another trace", input: domain.FeedbackRequest{
			TraceID: "trace-2", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
			RevisionID: "revision-1", Score: 0, Outcome: map[string]any{"label": "bad"},
			IdempotencyKey: "feedback-1",
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := repo.Record(ctx, tenantID, test.input); !errors.Is(err, domain.ErrFeedbackIdempotencyConflict) {
				t.Fatalf("changed replay error=%v", err)
			}
			replayed, err := repo.Record(ctx, tenantID, original)
			if err != nil {
				t.Fatal(err)
			}
			if replayed.ID != stored.ID || replayed.Score != stored.Score ||
				replayed.IdempotencyKey != stored.IdempotencyKey || replayed.Outcome["label"] != "good" {
				t.Fatalf("immutable feedback changed: before=%+v after=%+v", stored, replayed)
			}
		})
	}
}

func TestPgFeedbackRepositoryConcurrentChangedReplayHasOneWinner(t *testing.T) {
	pool, ctx, tenantID := feedbackRepositoryTestPool(t, "concurrent_replay")
	repo := NewPgFeedbackRepository(pool)
	inputs := []domain.FeedbackRequest{
		{TraceID: "trace-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
			RevisionID: "revision-1", Score: 1, IdempotencyKey: "feedback-1"},
		{TraceID: "trace-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
			RevisionID: "revision-1", Score: 0, IdempotencyKey: "feedback-2"},
	}
	errs := make(chan error, len(inputs))
	var wg sync.WaitGroup
	for _, input := range inputs {
		input := input
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repo.Record(ctx, tenantID, input)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	var succeeded, conflicted int
	for err := range errs {
		if err == nil {
			succeeded++
		} else if errors.Is(err, domain.ErrFeedbackIdempotencyConflict) {
			conflicted++
		} else {
			t.Fatalf("unexpected concurrent replay error: %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent changed replay succeeded=%d conflicted=%d", succeeded, conflicted)
	}
}

func feedbackRepositoryTestPool(t *testing.T, suffix string) (*pgxpool.Pool, context.Context, string) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	tenantID := fmt.Sprintf("eval_feedback_%s_%d", suffix, time.Now().UnixNano())
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "tenant_%s" CASCADE`, tenantID)) })
	return pool, ctx, tenantID
}

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
		(id, trace_id, resource_kind, resource_id, revision_id, experiment_id, variant, score, outcome, idempotency_key)
		VALUES
		 ('feedback-1','trace-1','skill','skill-1','stable-1','experiment-1','stable',0.8,
		  '{"security_violation":true}','key-1'),
		 ('feedback-old-experiment','trace-old-experiment','skill','skill-1','stable-1','experiment-old','stable',0.2,
		  '{}','key-old-experiment'),
		 ('feedback-wrong-kind','trace-wrong-kind','agent','skill-1','stable-1','experiment-1','stable',0.2,
		  '{}','key-wrong-kind'),
		 ('feedback-wrong-variant','trace-wrong-variant','skill','skill-1','stable-1','experiment-1','canary',0.2,
		  '{}','key-wrong-variant')`); err != nil {
		t.Fatal(err)
	}

	repo := NewPgFeedbackRepository(pool)
	feedback, _, err := repo.StageFeedback(ctx, tenantID, domain.Experiment{
		ID: "experiment-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
		StableRevisionID: "stable-1", CanaryRevisionID: "canary-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(feedback) != 1 || feedback[0].RevisionID != "stable-1" ||
		feedback[0].Outcome["security_violation"] != true {
		t.Fatalf("unexpected feedback: %+v", feedback)
	}
}

func TestPgFeedbackRepositoryPersistsTraceExperimentAttribution(t *testing.T) {
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

	tenantID := fmt.Sprintf("eval_feedback_attribution_%d", time.Now().UnixNano())
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "tenant_%s" CASCADE`, tenantID)) })

	stored, err := NewPgFeedbackRepository(pool).Record(ctx, tenantID, domain.FeedbackRequest{
		TraceID: "trace-1", ResourceKind: domain.ResourceKindAgent, ResourceID: "agent-1",
		RevisionID: "candidate-1", ExperimentID: "experiment-1", Variant: "canary",
		Score: 1, IdempotencyKey: "feedback-attribution-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if stored.ExperimentID != "experiment-1" || stored.Variant != "canary" {
		t.Fatalf("stored attribution = experiment %q variant %q", stored.ExperimentID, stored.Variant)
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

func TestFeedbackHoldDecisionDoesNotResetStageObservationWindow(t *testing.T) {
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

	tenantID := fmt.Sprintf("eval_feedback_hold_%d", time.Now().UnixNano())
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "tenant_%s" CASCADE`, tenantID)) })
	schema := `"tenant_` + tenantID + `"`
	if _, err := pool.Exec(ctx, `INSERT INTO `+schema+`.eval_suites (id, name) VALUES ('suite','suite');
		INSERT INTO `+schema+`.eval_suite_revisions
		(id, suite_id, version_no, status, resource_kind) VALUES ('suite-1','suite',1,'published','skill');
		INSERT INTO `+schema+`.evaluation_experiments
		(id, resource_kind, resource_id, stable_revision_id, canary_revision_id, suite_revision_id,
		 status, stage_percent, state_version, updated_at)
		VALUES ('experiment-1','skill','skill-1','stable-1','canary-1','suite-1','running',5,1,
		 NOW()-INTERVAL '2 minutes');
		INSERT INTO `+schema+`.evaluation_deployments
		(resource_kind, resource_id, stable_revision_id, canary_revision_id, canary_percent, experiment_id)
		VALUES ('skill','skill-1','stable-1','canary-1',5,'experiment-1');
		INSERT INTO `+schema+`.evaluation_feedback
		(id, trace_id, resource_kind, resource_id, revision_id, experiment_id, variant, score, outcome, idempotency_key)
		VALUES ('feedback-1','trace-1','skill','skill-1','stable-1','experiment-1','stable',0.8,'{}','key-1')`); err != nil {
		t.Fatal(err)
	}

	experiment := domain.Experiment{
		ID: "experiment-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
		StableRevisionID: "stable-1", CanaryRevisionID: "canary-1", SuiteRevisionID: "suite-1",
		Status: domain.ExperimentRunning, Stage: 5, StateVersion: 2, Recommendation: domain.DecisionHold,
	}
	if _, _, err := NewPgExperimentRepository(pool).SaveDecision(
		ctx, tenantID, experiment, domain.DecisionHold, domain.StageMetrics{Samples: 0},
		"hold-1", domain.MetricsFingerprint(domain.StageMetrics{Samples: 0}),
	); err != nil {
		t.Fatal(err)
	}

	feedback, _, err := NewPgFeedbackRepository(pool).StageFeedback(ctx, tenantID, experiment)
	if err != nil {
		t.Fatal(err)
	}
	if len(feedback) != 1 || feedback[0].ID != "feedback-1" {
		t.Fatalf("hold decision reset stage feedback window: %+v", feedback)
	}
}

type feedbackEvidenceFake struct {
	traces map[string]port.ObservedTrace
}

func (f feedbackEvidenceFake) Resolve(_ context.Context, _, traceID string) (port.ObservedTrace, error) {
	return f.traces[traceID], nil
}

func (f feedbackEvidenceFake) ResolveBatch(
	_ context.Context, _ string, traceIDs []string,
) (map[string]port.ObservedTrace, error) {
	result := make(map[string]port.ObservedTrace, len(traceIDs))
	for _, traceID := range traceIDs {
		result[traceID] = f.traces[traceID]
	}
	return result, nil
}

func TestFeedbackAlternatingVariantsAccumulateAndAdvanceStage(t *testing.T) {
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

	tenantID := fmt.Sprintf("eval_feedback_advance_%d", time.Now().UnixNano())
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "tenant_%s" CASCADE`, tenantID)) })
	schema := `"tenant_` + tenantID + `"`
	if _, err := pool.Exec(ctx, `INSERT INTO `+schema+`.eval_suites (id, name) VALUES ('suite','suite');
		INSERT INTO `+schema+`.eval_suite_revisions
		(id, suite_id, version_no, status, resource_kind) VALUES ('suite-1','suite',1,'published','skill');
		INSERT INTO `+schema+`.evaluation_experiments
		(id, resource_kind, resource_id, stable_revision_id, canary_revision_id, suite_revision_id,
		 status, stage_percent, state_version, policy)
		VALUES ('experiment-1','skill','skill-1','stable-1','canary-1','suite-1','running',5,1,
		 '{"stages":[5,20],"min_samples":2,"min_observation_minutes":0,"max_cost_regression":1,
		   "max_latency_regression":1,"max_error_rate_increase":1}');
		INSERT INTO `+schema+`.evaluation_deployments
		(resource_kind, resource_id, stable_revision_id, canary_revision_id, canary_percent, experiment_id)
		VALUES ('skill','skill-1','stable-1','canary-1',5,'experiment-1')`); err != nil {
		t.Fatal(err)
	}

	traces := map[string]port.ObservedTrace{}
	variants := []struct {
		traceID, revisionID, variant string
		score                        float64
	}{
		{traceID: "stable-1", revisionID: "stable-1", variant: "stable", score: 0},
		{traceID: "canary-1", revisionID: "canary-1", variant: "canary", score: 1},
		{traceID: "stable-2", revisionID: "stable-1", variant: "stable", score: 0},
		{traceID: "canary-2", revisionID: "canary-1", variant: "canary", score: 1},
	}
	for _, item := range variants {
		traces[item.traceID] = port.ObservedTrace{
			TraceID: item.traceID, Success: true,
			Assignments: map[string]port.ObservedResourceAssignment{
				"skill:skill-1": {
					RevisionID: item.revisionID, ExperimentID: "experiment-1", Variant: item.variant,
				},
			},
		}
	}
	feedbackRepo := NewPgFeedbackRepository(pool)
	service := application.NewFeedbackService(
		feedbackRepo, application.NewExperimentService(NewPgExperimentRepository(pool)),
		feedbackEvidenceFake{traces: traces},
	)
	var final application.FeedbackResult
	for index, item := range variants {
		final, err = service.Record(ctx, tenantID, domain.FeedbackRequest{
			TraceID: item.traceID, ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
			Score: item.score, IdempotencyKey: fmt.Sprintf("feedback-%d", index),
		})
		if err != nil {
			t.Fatalf("record feedback %d: %v", index, err)
		}
	}
	if final.Decision != domain.DecisionAdvance || final.Experiment == nil || final.Experiment.Stage != 20 {
		t.Fatalf("alternating feedback did not advance stage: %+v", final)
	}
}
