package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

func TestFeedbackServiceAutomaticallyEvaluatesReadyExperiment(t *testing.T) {
	policy := domain.DefaultPromotionPolicy()
	stable := make([]domain.OnlineObservation, policy.MinSamples)
	canary := make([]domain.OnlineObservation, policy.MinSamples)
	for i := range stable {
		stable[i] = domain.OnlineObservation{Score: 0.5, CostUSD: 1, LatencyMs: 100, Success: true}
		canary[i] = domain.OnlineObservation{Score: 0.8, CostUSD: 1.3, LatencyMs: 100, Success: true}
	}
	repo := &fakeFeedbackRepo{
		experiment: domain.Experiment{
			ID: "experiment-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
			StableRevisionID: "version-1", CanaryRevisionID: "candidate-1",
			Status: domain.ExperimentRunning, Stage: 5, Policy: policy,
		},
		stable: stable, canary: canary, observedMinutes: policy.MinObservationMinutes,
	}
	experiments := NewExperimentService(&feedbackExperimentRepo{experiment: repo.experiment})
	svc := NewFeedbackService(repo, experiments)

	result, err := svc.Record(context.Background(), "tenant-1", RecordFeedbackInput{
		TraceID: "trace-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
		Score: 0.9, IdempotencyKey: "feedback-1",
	})
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	if result.Decision != domain.DecisionRollback {
		t.Fatalf("cost guardrail should roll back, got %s", result.Decision)
	}
}

func TestFeedbackServiceRollsBackForEarlierStageSecurityViolation(t *testing.T) {
	policy := domain.DefaultPromotionPolicy()
	stable := make([]domain.OnlineObservation, policy.MinSamples)
	canary := make([]domain.OnlineObservation, policy.MinSamples)
	for i := range stable {
		stable[i] = domain.OnlineObservation{Score: 0.5, CostUSD: 1, LatencyMs: 100, Success: true}
		canary[i] = domain.OnlineObservation{Score: 0.8, CostUSD: 1, LatencyMs: 100, Success: true}
	}
	canary[0].SecurityViolation = true
	repo := &fakeFeedbackRepo{
		experiment: domain.Experiment{
			ID: "experiment-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
			StableRevisionID: "version-1", CanaryRevisionID: "candidate-1",
			Status: domain.ExperimentRunning, Stage: 5, Policy: policy,
		},
		stable: stable, canary: canary, observedMinutes: policy.MinObservationMinutes,
	}
	experiments := NewExperimentService(&feedbackExperimentRepo{experiment: repo.experiment})
	svc := NewFeedbackService(repo, experiments)

	result, err := svc.Record(context.Background(), "tenant-1", RecordFeedbackInput{
		TraceID: "trace-last", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
		Score: 0.9, IdempotencyKey: "feedback-last",
	})
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	if result.Decision != domain.DecisionRollback {
		t.Fatalf("earlier security violation should roll back, got %s", result.Decision)
	}
}

type fakeFeedbackRepo struct {
	experiment      domain.Experiment
	stable, canary  []domain.OnlineObservation
	observedMinutes int
}

func (f *fakeFeedbackRepo) Record(_ context.Context, _ string, input RecordFeedbackInput) (domain.EvaluationFeedback, error) {
	return domain.EvaluationFeedback{ID: "feedback-1", TraceID: input.TraceID, ResourceID: input.ResourceID, Score: input.Score}, nil
}

func (f *fakeFeedbackRepo) ActiveExperiment(_ context.Context, _ string, _, _ string) (domain.Experiment, bool, error) {
	return f.experiment, true, nil
}

func (f *fakeFeedbackRepo) Observations(
	_ context.Context, _ string, _ domain.Experiment,
) ([]domain.OnlineObservation, []domain.OnlineObservation, int, error) {
	return f.stable, f.canary, f.observedMinutes, nil
}

type feedbackExperimentRepo struct{ experiment domain.Experiment }

func (f *feedbackExperimentRepo) Create(context.Context, string, domain.Experiment, domain.Deployment) error {
	return nil
}
func (f *feedbackExperimentRepo) Get(context.Context, string, string) (domain.Experiment, bool, error) {
	return f.experiment, true, nil
}
func (f *feedbackExperimentRepo) SaveDecision(_ context.Context, _ string, experiment domain.Experiment, _ domain.Decision, _ domain.StageMetrics) error {
	f.experiment = experiment
	return nil
}
func (f *feedbackExperimentRepo) ResolveDeployment(context.Context, string, string, string) (domain.Deployment, bool, error) {
	return domain.Deployment{}, false, nil
}
