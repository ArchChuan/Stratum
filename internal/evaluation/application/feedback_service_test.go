package application

import (
	"context"
	"errors"
	"fmt"
	"testing"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
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

func TestFeedbackServicePersistsInsufficientSampleEvidence(t *testing.T) {
	policy := domain.DefaultPromotionPolicy()
	repo := &fakeFeedbackRepo{experiment: domain.Experiment{
		ID: "experiment-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
		StableRevisionID: "stable-1", CanaryRevisionID: "canary-1", Status: domain.ExperimentRunning,
		Stage: 5, Policy: policy, StateVersion: 1,
	}, stable: []domain.OnlineObservation{{Score: .5}}, canary: []domain.OnlineObservation{{Score: .6}}}
	experimentRepo := &feedbackExperimentRepo{experiment: repo.experiment}
	svc := NewFeedbackService(repo, NewExperimentService(experimentRepo),
		feedbackEvidence("trace-low", repo, "canary-1", "canary"))
	result, err := svc.Record(context.Background(), "tenant-1", RecordFeedbackInput{
		TraceID: "trace-low", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
		Score: .6, IdempotencyKey: "feedback-low",
	})
	if err != nil || result.Experiment == nil || experimentRepo.decisionCount != 1 {
		t.Fatalf("insufficient evidence was not persisted: result=%+v decisions=%d err=%v",
			result, experimentRepo.decisionCount, err)
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

func TestFeedbackServiceSafetyStopsOnFirstSecurityViolation(t *testing.T) {
	policy := domain.DefaultPromotionPolicy()
	repo := &fakeFeedbackRepo{experiment: domain.Experiment{
		ID: "experiment-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
		StableRevisionID: "stable-1", CanaryRevisionID: "canary-1", Status: domain.ExperimentRunning,
		Stage: 5, Policy: policy, StateVersion: 1,
	}}
	experimentRepo := &feedbackExperimentRepo{experiment: repo.experiment}
	evidence := feedbackEvidence("trace-security", repo, "canary-1", "canary")
	evidence.traces["trace-security"] = observedTrace("trace-security", repo.experiment, "canary-1", "canary",
		domain.OnlineObservation{SecurityViolation: true})
	svc := NewFeedbackService(repo, NewExperimentService(experimentRepo), evidence)

	result, err := svc.Record(context.Background(), "tenant-1", RecordFeedbackInput{
		TraceID: "trace-security", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
		Score: 0.1, IdempotencyKey: "feedback-security",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != domain.DecisionRollback || result.Experiment == nil ||
		!result.Experiment.SafetyStopped || experimentRepo.experiment.Stage != 0 {
		t.Fatalf("first security violation did not safety stop: result=%+v stored=%+v", result, experimentRepo.experiment)
	}
	retry, err := svc.Record(context.Background(), "tenant-1", RecordFeedbackInput{
		TraceID: "trace-security", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
		Score: 0.1, IdempotencyKey: "feedback-security",
	})
	if err != nil || retry.Experiment == nil || retry.Experiment.StateVersion != result.Experiment.StateVersion ||
		experimentRepo.decisionCount != 1 {
		t.Fatalf("security retry result=%+v decisions=%d err=%v", retry, experimentRepo.decisionCount, err)
	}
}

func TestFeedbackServiceDoesNotSafetyStopActiveExperimentFromStaleTrace(t *testing.T) {
	policy := domain.DefaultPromotionPolicy()
	repo := &fakeFeedbackRepo{experiment: domain.Experiment{
		ID: "experiment-new", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
		StableRevisionID: "stable-1", CanaryRevisionID: "canary-new", Status: domain.ExperimentRunning,
		Stage: 5, Policy: policy, StateVersion: 1,
	}}
	experimentRepo := &feedbackExperimentRepo{experiment: repo.experiment}
	evidence := feedbackEvidence("trace-old-security", repo, "stable-1", "stable")
	trace := evidence.traces["trace-old-security"]
	trace.SecurityViolation = true
	trace.Assignments["skill:skill-1"] = port.ObservedResourceAssignment{
		RevisionID: "stable-1", ExperimentID: "experiment-old", Variant: "stable",
	}
	evidence.traces["trace-old-security"] = trace
	svc := NewFeedbackService(repo, NewExperimentService(experimentRepo), evidence)

	result, err := svc.Record(context.Background(), "tenant-1", RecordFeedbackInput{
		TraceID: "trace-old-security", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
		Score: 0.1, IdempotencyKey: "feedback-old-security",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Experiment != nil || result.Decision != domain.DecisionHold || experimentRepo.decisionCount != 0 ||
		experimentRepo.experiment.Stage != 5 || experimentRepo.experiment.SafetyStopped {
		t.Fatalf("stale trace changed active experiment: result=%+v stored=%+v decisions=%d",
			result, experimentRepo.experiment, experimentRepo.decisionCount)
	}
}

func TestFeedbackServiceDoesNotTrustClientSecurityClaimsForHardStop(t *testing.T) {
	for _, test := range []struct {
		name  string
		input RecordFeedbackInput
	}{
		{name: "top-level flag", input: RecordFeedbackInput{SecurityViolation: true}},
		{name: "outcome flag", input: RecordFeedbackInput{Outcome: map[string]any{"security_violation": true}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := domain.DefaultPromotionPolicy()
			repo := &fakeFeedbackRepo{experiment: domain.Experiment{
				ID: "experiment-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
				StableRevisionID: "stable-1", CanaryRevisionID: "canary-1", Status: domain.ExperimentRunning,
				Stage: 5, Policy: policy, StateVersion: 1,
			}}
			experimentRepo := &feedbackExperimentRepo{experiment: repo.experiment}
			svc := NewFeedbackService(repo, NewExperimentService(experimentRepo),
				feedbackEvidence("trace-client-claim", repo, "canary-1", "canary"))
			test.input.TraceID = "trace-client-claim"
			test.input.ResourceKind = domain.ResourceKindSkill
			test.input.ResourceID = "skill-1"
			test.input.Score = 0.1
			test.input.IdempotencyKey = "feedback-client-claim"

			result, err := svc.Record(context.Background(), "tenant-1", test.input)
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision == domain.DecisionRollback || experimentRepo.experiment.Stage != 5 ||
				experimentRepo.experiment.SafetyStopped {
				t.Fatalf("client claim triggered hard stop: result=%+v stored=%+v", result, experimentRepo.experiment)
			}
		})
	}
}

func TestEvaluationIdempotencyKeyDoesNotDependOnMutableStage(t *testing.T) {
	first := evaluationIdempotencyKey("feedback-1", "experiment-1")
	retry := evaluationIdempotencyKey("feedback-1", "experiment-1")
	if first != retry {
		t.Fatalf("immutable feedback identity produced different keys: %q %q", first, retry)
	}
}

func TestFeedbackRetryReplaysStageAdvanceOnce(t *testing.T) {
	policy := domain.DefaultPromotionPolicy()
	stable := make([]domain.OnlineObservation, policy.MinSamples)
	canary := make([]domain.OnlineObservation, policy.MinSamples)
	for i := range stable {
		stable[i] = domain.OnlineObservation{Score: 0.2, CostUSD: 1, LatencyMs: 100, Success: true}
		canary[i] = domain.OnlineObservation{Score: 0.9, CostUSD: 1, LatencyMs: 100, Success: true}
	}
	repo := &fakeFeedbackRepo{experiment: domain.Experiment{
		ID: "experiment-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
		StableRevisionID: "stable-1", CanaryRevisionID: "canary-1", Status: domain.ExperimentRunning,
		Stage: 5, Policy: policy, StateVersion: 1,
	}, stable: stable, canary: canary, observedMinutes: policy.MinObservationMinutes}
	experimentRepo := &feedbackExperimentRepo{experiment: repo.experiment}
	svc := NewFeedbackService(repo, NewExperimentService(experimentRepo), feedbackEvidence("trace-1", repo, "canary-1", "canary"))
	input := RecordFeedbackInput{TraceID: "trace-1", ResourceKind: domain.ResourceKindSkill,
		ResourceID: "skill-1", Score: 0.9, IdempotencyKey: "feedback-1"}
	first, err := svc.Record(context.Background(), "tenant-1", input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Record(context.Background(), "tenant-1", input)
	if err != nil || first.Experiment == nil || second.Experiment == nil ||
		first.Experiment.Stage != 20 || second.Experiment.Stage != 20 || experimentRepo.decisionCount != 1 {
		t.Fatalf("feedback retry first=%+v second=%+v decisions=%d err=%v",
			first.Experiment, second.Experiment, experimentRepo.decisionCount, err)
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

func TestFeedbackServiceRejectsFeedbackFromAnotherTraceOwner(t *testing.T) {
	repo := &fakeFeedbackRepo{}
	evidence := &fakeTraceEvidenceReader{traces: map[string]port.ObservedTrace{
		"trace-1": {
			TraceID: "trace-1", UserID: "user-a",
			Assignments: map[string]port.ObservedResourceAssignment{
				"skill:skill-1": {RevisionID: "revision-1"},
			},
		},
	}}
	svc := NewFeedbackService(repo, NewExperimentService(&feedbackExperimentRepo{}), evidence)
	_, err := svc.Record(context.Background(), "tenant-1", RecordFeedbackInput{
		ActorID: "user-b", TraceID: "trace-1", ResourceKind: domain.ResourceKindSkill,
		ResourceID: "skill-1", Score: 1, IdempotencyKey: "feedback-1",
	})
	if !errors.Is(err, domain.ErrFeedbackTraceForbidden) {
		t.Fatalf("cross-user feedback error = %v, want forbidden", err)
	}
	if repo.recorded.TraceID != "" {
		t.Fatal("cross-user feedback reached persistence")
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
	outcome := make(map[string]any, len(input.Outcome)+1)
	for key, value := range input.Outcome {
		outcome[key] = value
	}
	if input.SecurityViolation {
		outcome["security_violation"] = true
	}
	return domain.EvaluationFeedback{
		ID: "feedback-1", TraceID: input.TraceID, ResourceID: input.ResourceID, Score: input.Score, Outcome: outcome,
	}, nil
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
	if f.experiment.ID == "" {
		return domain.Experiment{}, false, nil
	}
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

type feedbackExperimentRepo struct {
	experiment    domain.Experiment
	decisions     map[string]fakeEvaluationDecision
	decisionCount int
}

func (f *feedbackExperimentRepo) Create(context.Context, string, domain.Experiment, domain.Deployment,
	*auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
func (f *feedbackExperimentRepo) ValidatePrerequisites(context.Context, string, domain.ResourceRef,
	domain.ResourceRef, string) error {
	return nil
}
func (f *feedbackExperimentRepo) Get(context.Context, string, string) (domain.Experiment, bool, error) {
	return f.experiment, true, nil
}
func (f *feedbackExperimentRepo) SaveDecision(
	_ context.Context, _ string, experiment domain.Experiment, decision domain.Decision, _ domain.StageMetrics,
	idempotencyKey, fingerprint string,
) (domain.Experiment, domain.Decision, error) {
	if previous, ok := f.decisions[idempotencyKey]; ok {
		if previous.fingerprint != fingerprint {
			return domain.Experiment{}, domain.DecisionHold, domain.ErrExperimentCommandConflict
		}
		return previous.experiment, previous.decision, nil
	}
	if f.decisions == nil {
		f.decisions = make(map[string]fakeEvaluationDecision)
	}
	f.experiment = experiment
	f.decisions[idempotencyKey] = fakeEvaluationDecision{experiment: experiment, decision: decision, fingerprint: fingerprint}
	f.decisionCount++
	return experiment, decision, nil
}
func (f *feedbackExperimentRepo) ApplyCommand(
	context.Context, string, string, domain.ExperimentCommandAction, domain.ExperimentCommand,
) (domain.Experiment, error) {
	return domain.Experiment{}, domain.ErrExperimentCommandNotAllowed
}
func (f *feedbackExperimentRepo) ResolveDeployment(context.Context, string, string, string) (domain.Deployment, bool, error) {
	return domain.Deployment{}, false, nil
}

func (f *feedbackExperimentRepo) HasRunningExperiment(_ context.Context, _ string, _, _ string) (bool, error) {
	return false, nil
}

func (f *feedbackExperimentRepo) ListPendingExperiments(_ context.Context, _ string, _, _ string) ([]domain.Experiment, error) {
	return nil, nil
}

func (f *feedbackExperimentRepo) ListRunningExperiments(_ context.Context, _ string) ([]domain.Experiment, error) {
	return nil, nil
}
