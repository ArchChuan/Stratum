package application

import (
	"context"
	"fmt"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
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
	svc := NewFeedbackService(repo, experiments, feedbackEvidence("trace-1", repo, "version-1", "stable"))

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
	svc := NewFeedbackService(repo, experiments, feedbackEvidence("trace-last", repo, "candidate-1", "canary"))

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

func TestFeedbackServiceValidatesAndPersistsObservedRevision(t *testing.T) {
	repo := &fakeFeedbackRepo{}
	evidence := &fakeTraceEvidenceReader{traces: map[string]port.ObservedTrace{
		"trace-1": {
			TraceID: "trace-1",
			Assignments: map[string]port.ObservedResourceAssignment{
				"skill:skill-1": {RevisionID: "revision-1", ExperimentID: "experiment-1", Variant: "canary"},
			},
		},
	}}
	svc := NewFeedbackService(repo, NewExperimentService(&feedbackExperimentRepo{}), evidence)

	_, err := svc.Record(context.Background(), "tenant-1", RecordFeedbackInput{
		TraceID: "trace-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
		Score: 0.9, IdempotencyKey: "feedback-1",
	})
	if err != nil {
		t.Fatalf("Record() error: %v", err)
	}
	if repo.recorded.RevisionID != "revision-1" {
		t.Fatalf("persisted revision = %q", repo.recorded.RevisionID)
	}
}

type fakeFeedbackRepo struct {
	experiment      domain.Experiment
	stable, canary  []domain.OnlineObservation
	observedMinutes int
	recorded        RecordFeedbackInput
}

func (f *fakeFeedbackRepo) Record(_ context.Context, _ string, input RecordFeedbackInput) (domain.EvaluationFeedback, error) {
	f.recorded = input
	return domain.EvaluationFeedback{ID: "feedback-1", TraceID: input.TraceID, ResourceID: input.ResourceID, Score: input.Score}, nil
}

type fakeTraceEvidenceReader struct {
	traces map[string]port.ObservedTrace
}

func feedbackEvidence(
	traceID string, repo *fakeFeedbackRepo, revisionID, variant string,
) *fakeTraceEvidenceReader {
	traces := map[string]port.ObservedTrace{
		traceID: {
			TraceID: traceID,
			Assignments: map[string]port.ObservedResourceAssignment{
				"skill:" + repo.experiment.ResourceID: {
					RevisionID: revisionID, ExperimentID: repo.experiment.ID, Variant: variant,
				},
			},
		},
	}
	for i, observation := range repo.stable {
		id := fmt.Sprintf("stable-%d", i)
		traces[id] = observedTrace(id, repo.experiment, repo.experiment.StableRevisionID, "stable", observation)
	}
	for i, observation := range repo.canary {
		id := fmt.Sprintf("canary-%d", i)
		traces[id] = observedTrace(id, repo.experiment, repo.experiment.CanaryRevisionID, "canary", observation)
	}
	return &fakeTraceEvidenceReader{traces: traces}
}

func observedTrace(
	traceID string, experiment domain.Experiment, revisionID, variant string, observation domain.OnlineObservation,
) port.ObservedTrace {
	return port.ObservedTrace{
		TraceID: traceID, CostUSD: observation.CostUSD, LatencyMs: observation.LatencyMs,
		Success: observation.Success, SecurityViolation: observation.SecurityViolation,
		Assignments: map[string]port.ObservedResourceAssignment{
			"skill:" + experiment.ResourceID: {
				RevisionID: revisionID, ExperimentID: experiment.ID, Variant: variant,
			},
		},
	}
}

func (f *fakeTraceEvidenceReader) Resolve(
	_ context.Context, _ string, traceID string,
) (port.ObservedTrace, error) {
	return f.traces[traceID], nil
}

func (f *fakeTraceEvidenceReader) ResolveBatch(
	_ context.Context, _ string, _ []string,
) (map[string]port.ObservedTrace, error) {
	return f.traces, nil
}

func (f *fakeFeedbackRepo) ActiveExperiment(_ context.Context, _ string, _, _ string) (domain.Experiment, bool, error) {
	return f.experiment, true, nil
}

func (f *fakeFeedbackRepo) StageFeedback(
	_ context.Context, _ string, experiment domain.Experiment,
) ([]domain.EvaluationFeedback, int, error) {
	rows := make([]domain.EvaluationFeedback, 0, len(f.stable)+len(f.canary))
	for i, observation := range f.stable {
		rows = append(rows, domain.EvaluationFeedback{
			TraceID: fmt.Sprintf("stable-%d", i), ResourceID: experiment.ResourceID,
			RevisionID: experiment.StableRevisionID, Score: observation.Score,
		})
	}
	for i, observation := range f.canary {
		outcome := map[string]any{}
		if observation.SecurityViolation {
			outcome["security_violation"] = true
		}
		rows = append(rows, domain.EvaluationFeedback{
			TraceID: fmt.Sprintf("canary-%d", i), ResourceID: experiment.ResourceID,
			RevisionID: experiment.CanaryRevisionID, Score: observation.Score, Outcome: outcome,
		})
	}
	return rows, f.observedMinutes, nil
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
