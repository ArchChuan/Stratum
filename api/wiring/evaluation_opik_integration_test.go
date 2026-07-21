//go:build integration

package wiring

import (
	"context"
	"os"
	"testing"
	"time"

	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/infrastructure/opik"
	evalapp "github.com/byteBuilderX/stratum/internal/evaluation/application"
	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

func TestRealOpikEvidenceDrivesFeedbackRollback(t *testing.T) {
	if os.Getenv("TEST_OPIK_E2E") != "1" {
		t.Skip("set TEST_OPIK_E2E=1 after the real Opik evidence parity fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	provider := opik.NewClient(opik.Config{
		BaseURL: "http://127.0.0.1:5173/api", Project: "Default Project", Timeout: 3 * time.Second,
	})
	executions, _, err := provider.ListExecutions(ctx, "tenant-e2e", agentdomain.ListOptions{Page: 1, PageSize: 100})
	if err != nil {
		t.Fatalf("list real Opik executions: %v", err)
	}
	traceIDs := map[string]string{}
	for _, execution := range executions {
		traceIDs[execution.ID] = execution.TraceID
	}
	stableTraceID := traceIDs["execution-e2e-stable"]
	canaryTraceID := traceIDs["execution-e2e"]
	if stableTraceID == "" || canaryTraceID == "" {
		t.Fatalf("real stable/canary traces missing: %#v", traceIDs)
	}

	policy := evaldomain.DefaultPromotionPolicy()
	experiment := evaldomain.Experiment{
		ID: "experiment-e2e", ResourceKind: evaldomain.ResourceKindSkill, ResourceID: "skill-e2e",
		StableRevisionID: "revision-stable", CanaryRevisionID: "revision-e2e",
		Status: evaldomain.ExperimentRunning, Stage: 5, Policy: policy,
	}
	feedbackRepo := &realEvidenceFeedbackRepo{
		experiment: experiment, stableTraceID: stableTraceID, canaryTraceID: canaryTraceID,
	}
	experimentRepo := &realEvidenceExperimentRepo{experiment: experiment}
	service := evalapp.NewFeedbackService(
		feedbackRepo, evalapp.NewExperimentService(experimentRepo),
		evaluationTraceEvidenceAdapter{provider: provider},
	)
	result, err := service.Record(ctx, "tenant-e2e", evalapp.RecordFeedbackInput{
		TraceID: canaryTraceID, ResourceKind: evaldomain.ResourceKindSkill, ResourceID: "skill-e2e",
		Score: 0.9, IdempotencyKey: "feedback-e2e",
	})
	if err != nil {
		t.Fatalf("record feedback from real Opik evidence: %v", err)
	}
	if feedbackRepo.recorded.RevisionID != "revision-e2e" || feedbackRepo.recorded.Variant != "canary" {
		t.Fatalf("observed attribution = %#v", feedbackRepo.recorded)
	}
	if result.Decision != evaldomain.DecisionRollback {
		t.Fatalf("security evidence decision = %s, want rollback", result.Decision)
	}
}

type realEvidenceFeedbackRepo struct {
	experiment                   evaldomain.Experiment
	stableTraceID, canaryTraceID string
	recorded                     evaldomain.FeedbackRequest
}

func (r *realEvidenceFeedbackRepo) Record(
	_ context.Context, _ string, input evaldomain.FeedbackRequest,
) (evaldomain.EvaluationFeedback, error) {
	r.recorded = input
	return evaldomain.EvaluationFeedback{
		ID: "feedback-e2e", TraceID: input.TraceID, ResourceID: input.ResourceID,
		RevisionID: input.RevisionID, Score: input.Score,
	}, nil
}

func (r *realEvidenceFeedbackRepo) ActiveExperiment(
	context.Context, string, string, string,
) (evaldomain.Experiment, bool, error) {
	return r.experiment, true, nil
}

func (r *realEvidenceFeedbackRepo) StageFeedback(
	context.Context, string, evaldomain.Experiment,
) ([]evaldomain.EvaluationFeedback, int, error) {
	policy := r.experiment.Policy
	rows := make([]evaldomain.EvaluationFeedback, 0, policy.MinSamples*2)
	for range policy.MinSamples {
		rows = append(rows, evaldomain.EvaluationFeedback{
			TraceID: r.stableTraceID, ResourceID: r.experiment.ResourceID,
			RevisionID: r.experiment.StableRevisionID, Score: 0.5,
		})
		rows = append(rows, evaldomain.EvaluationFeedback{
			TraceID: r.canaryTraceID, ResourceID: r.experiment.ResourceID,
			RevisionID: r.experiment.CanaryRevisionID, Score: 0.8,
		})
	}
	return rows, policy.MinObservationMinutes, nil
}

type realEvidenceExperimentRepo struct{ experiment evaldomain.Experiment }

func (r *realEvidenceExperimentRepo) Create(
	context.Context, string, evaldomain.Experiment, evaldomain.Deployment,
) error {
	return nil
}

func (r *realEvidenceExperimentRepo) Get(
	context.Context, string, string,
) (evaldomain.Experiment, bool, error) {
	return r.experiment, true, nil
}

func (r *realEvidenceExperimentRepo) SaveDecision(
	_ context.Context, _ string, experiment evaldomain.Experiment, _ evaldomain.Decision, _ evaldomain.StageMetrics,
) error {
	r.experiment = experiment
	return nil
}

func (r *realEvidenceExperimentRepo) ResolveDeployment(
	context.Context, string, string, string,
) (evaldomain.Deployment, bool, error) {
	return evaldomain.Deployment{}, false, nil
}
