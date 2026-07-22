package application

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

func TestExperimentServiceEvaluationIsIdempotent(t *testing.T) {
	policy := domain.DefaultPromotionPolicy()
	repo := &fakeExperimentRepo{experiment: domain.Experiment{
		ID: "experiment-1", Status: domain.ExperimentRunning, Stage: 5, Policy: policy, StateVersion: 1,
	}}
	svc := NewExperimentService(repo)
	input := EvaluateStageInput{Metrics: domain.StageMetrics{
		Samples: policy.MinSamples, ObservedMinutes: policy.MinObservationMinutes,
		QualityImprovement: 0.1, QualitySignificant: true,
	}, IdempotencyKey: "evaluation-1"}
	first, firstDecision, err := svc.EvaluateStageIdempotent(context.Background(), "tenant-1", "experiment-1", input)
	if err != nil {
		t.Fatal(err)
	}
	second, secondDecision, err := svc.EvaluateStageIdempotent(context.Background(), "tenant-1", "experiment-1", input)
	if err != nil || second.StateVersion != first.StateVersion || second.Stage != first.Stage || secondDecision != firstDecision {
		t.Fatalf("retry first=%+v/%s second=%+v/%s err=%v", first, firstDecision, second, secondDecision, err)
	}
	input.Metrics.CostRegression = 0.01
	if _, _, err := svc.EvaluateStageIdempotent(context.Background(), "tenant-1", "experiment-1", input); !errors.Is(err, domain.ErrExperimentCommandConflict) {
		t.Fatalf("changed metrics error=%v", err)
	}
}

func TestExperimentServiceRejectsPausedEvaluationWithoutSaving(t *testing.T) {
	repo := &fakeExperimentRepo{experiment: domain.Experiment{
		ID: "experiment-1", Status: domain.ExperimentPaused, Stage: 50, StateVersion: 4,
	}}
	svc := NewExperimentService(repo)
	_, _, err := svc.EvaluateStageIdempotent(context.Background(), "tenant-1", "experiment-1", EvaluateStageInput{
		Metrics: domain.StageMetrics{SecurityViolation: true}, IdempotencyKey: "evaluation-paused",
	})
	if !errors.Is(err, domain.ErrExperimentCommandNotAllowed) || repo.saveDecisionCalls != 0 {
		t.Fatalf("paused evaluation err=%v save calls=%d", err, repo.saveDecisionCalls)
	}
}

func TestExperimentServiceCreatesFivePercentCanaryDeployment(t *testing.T) {
	repo := &fakeExperimentRepo{}
	svc := NewExperimentService(repo)
	baseline := domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "version-1"}
	canary := domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "candidate-1"}

	experiment, deployment, err := svc.Create(context.Background(), "tenant-1", CreateExperimentInput{
		Stable: baseline, Canary: canary, SuiteRevisionID: "suite-revision-1",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if experiment.Stage != 5 || deployment.CanaryPercent != 5 || deployment.StableRevisionID != "version-1" {
		t.Fatalf("unexpected experiment/deployment: %+v %+v", experiment, deployment)
	}
}

func TestExperimentServiceSafetyStopsWithoutRollingBackStable(t *testing.T) {
	repo := &fakeExperimentRepo{experiment: domain.Experiment{
		ID: "experiment-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
		StableRevisionID: "version-1", CanaryRevisionID: "candidate-1", Status: domain.ExperimentRunning, Stage: 20,
	}, deployment: domain.Deployment{StableRevisionID: "version-1", CanaryRevisionID: "candidate-1", CanaryPercent: 20}}
	svc := NewExperimentService(repo)
	policy := domain.DefaultPromotionPolicy()

	experiment, decision, err := svc.EvaluateStageIdempotent(context.Background(), "tenant-1", "experiment-1", EvaluateStageInput{Metrics: domain.StageMetrics{
		Samples: policy.MinSamples, ObservedMinutes: policy.MinObservationMinutes,
		QualityImprovement: 0.1, QualitySignificant: true, ErrorRateIncrease: 0.02,
	}, IdempotencyKey: "evaluation-1"})
	if err != nil {
		t.Fatalf("EvaluateStage returned error: %v", err)
	}
	if decision != domain.DecisionRollback || experiment.Status != domain.ExperimentRunning ||
		!experiment.SafetyStopped || repo.deployment.CanaryPercent != 0 || repo.deployment.StableRevisionID != "version-1" {
		t.Fatalf("safety stop not applied: decision=%s experiment=%+v deployment=%+v", decision, experiment, repo.deployment)
	}
}

func TestExperimentServiceRecommendationDoesNotPromoteStable(t *testing.T) {
	policy := domain.DefaultPromotionPolicy()
	repo := &fakeExperimentRepo{experiment: domain.Experiment{
		ID: "experiment-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
		StableRevisionID: "stable-1", CanaryRevisionID: "canary-1", Status: domain.ExperimentRunning,
		Stage: 50, Policy: policy, StateVersion: 1,
	}, deployment: domain.Deployment{StableRevisionID: "stable-1", CanaryRevisionID: "canary-1", CanaryPercent: 50}}
	svc := NewExperimentService(repo)

	experiment, recommendation, err := svc.EvaluateStageIdempotent(context.Background(), "tenant-1", "experiment-1", EvaluateStageInput{Metrics: domain.StageMetrics{
		Samples: policy.MinSamples, ObservedMinutes: policy.MinObservationMinutes,
		QualityImprovement: 0.1, QualitySignificant: true,
	}, IdempotencyKey: "evaluation-1"})
	if err != nil {
		t.Fatal(err)
	}
	if recommendation != domain.DecisionAdvance || experiment.Recommendation != domain.DecisionAdvance ||
		experiment.Status != domain.ExperimentRunning ||
		repo.deployment.StableRevisionID != "stable-1" {
		t.Fatalf("evaluation mutated stable deployment: experiment=%+v deployment=%+v", experiment, repo.deployment)
	}
}

func TestExperimentServicePromoteRequiresPersistedRecommendation(t *testing.T) {
	repo := &fakeExperimentRepo{experiment: domain.Experiment{ID: "experiment-1", Status: domain.ExperimentRunning, StateVersion: 2}}
	svc := NewExperimentService(repo)
	_, err := svc.Promote(context.Background(), "tenant-1", "experiment-1", validCommand(2))
	if err == nil {
		t.Fatal("expected promotion without persisted recommendation to fail")
	}
}

func TestExperimentServiceHumanCommands(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action domain.ExperimentCommandAction
		status domain.ExperimentStatus
	}{
		{"pause", domain.CommandPause, domain.ExperimentPaused},
		{"promote", domain.CommandPromote, domain.ExperimentCompleted},
		{"rollback", domain.CommandRollback, domain.ExperimentRolledBack},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeExperimentRepo{experiment: domain.Experiment{
				ID: "experiment-1", Status: domain.ExperimentRunning, Recommendation: domain.DecisionPromote, StateVersion: 3,
			}}
			svc := NewExperimentService(repo)
			var got domain.Experiment
			var err error
			switch tc.action {
			case domain.CommandPause:
				got, err = svc.Pause(context.Background(), "tenant-1", "experiment-1", validCommand(3))
			case domain.CommandPromote:
				got, err = svc.Promote(context.Background(), "tenant-1", "experiment-1", validCommand(3))
			case domain.CommandRollback:
				got, err = svc.Rollback(context.Background(), "tenant-1", "experiment-1", validCommand(3))
			}
			if err != nil || got.Status != tc.status || repo.commandAction != tc.action ||
				repo.commandActorType != domain.ActorTypeAdmin {
				t.Fatalf("command failed: got=%+v action=%s err=%v", got, repo.commandAction, err)
			}
		})
	}
}

func TestExperimentServiceAllowsPausedRollbackButRejectsPausedPromote(t *testing.T) {
	repo := &fakeExperimentRepo{experiment: domain.Experiment{
		ID: "experiment-1", Status: domain.ExperimentPaused, Recommendation: domain.DecisionPromote, StateVersion: 3,
	}}
	svc := NewExperimentService(repo)
	if _, err := svc.Promote(context.Background(), "tenant-1", "experiment-1", validCommand(3)); err == nil {
		t.Fatal("paused promotion must fail")
	}
	got, err := svc.Rollback(context.Background(), "tenant-1", "experiment-1", validCommand(3))
	if err != nil || got.Status != domain.ExperimentRolledBack {
		t.Fatalf("paused rollback got=%+v err=%v", got, err)
	}
}

func validCommand(version int64) ExperimentCommandInput {
	return ExperimentCommandInput{ActorID: "admin-1", Reason: "reviewed", IdempotencyKey: "command-1", ExpectedStateVersion: version}
}

func TestExperimentServiceResolveAssignmentIncludesVariantEvidence(t *testing.T) {
	repo := &fakeExperimentRepo{deployment: domain.Deployment{
		ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
		StableRevisionID: "stable-1", CanaryRevisionID: "canary-1",
		CanaryPercent: 50, ExperimentID: "experiment-1",
	}}
	svc := NewExperimentService(repo)
	canarySubject := ""
	for i := 0; i < 1000; i++ {
		candidate := fmt.Sprintf("subject-%d", i)
		if domain.AssignVariant(candidate+":skill-1", 50) {
			canarySubject = candidate
			break
		}
	}
	if canarySubject == "" {
		t.Fatal("failed to find deterministic canary subject")
	}

	assignment, found, err := svc.ResolveAssignment(
		context.Background(), "tenant-1", domain.ResourceKindSkill, "skill-1", canarySubject,
	)
	if err != nil || !found {
		t.Fatalf("ResolveAssignment failed: found=%v err=%v", found, err)
	}
	if assignment.RevisionID != "canary-1" || assignment.ExperimentID != "experiment-1" || assignment.Variant != "canary" {
		t.Fatalf("unexpected assignment: %+v", assignment)
	}
}

type fakeExperimentRepo struct {
	experiment        domain.Experiment
	deployment        domain.Deployment
	commandAction     domain.ExperimentCommandAction
	commandActorType  domain.ActorType
	decisions         map[string]fakeEvaluationDecision
	saveDecisionCalls int
}

type fakeEvaluationDecision struct {
	experiment  domain.Experiment
	decision    domain.Decision
	fingerprint string
}

func (f *fakeExperimentRepo) ApplyCommand(
	_ context.Context, _ string, experimentID string, action domain.ExperimentCommandAction, command domain.ExperimentCommand,
) (domain.Experiment, error) {
	if f.experiment.ID != experimentID || f.experiment.StateVersion != command.ExpectedStateVersion {
		return domain.Experiment{}, domain.ErrExperimentStateConflict
	}
	if !domain.CanApplyExperimentCommand(f.experiment.Status, action) ||
		(action == domain.CommandPromote && (f.experiment.Recommendation != domain.DecisionPromote || f.experiment.SafetyStopped)) {
		return domain.Experiment{}, domain.ErrExperimentCommandNotAllowed
	}
	f.commandAction = action
	f.commandActorType = command.ActorType
	f.experiment.StateVersion++
	switch action {
	case domain.CommandPause:
		f.experiment.Status = domain.ExperimentPaused
	case domain.CommandPromote:
		f.experiment.Status = domain.ExperimentCompleted
	case domain.CommandRollback:
		f.experiment.Status = domain.ExperimentRolledBack
	}
	return f.experiment, nil
}

func (f *fakeExperimentRepo) Create(
	_ context.Context, _ string, experiment domain.Experiment, deployment domain.Deployment,
) error {
	f.experiment, f.deployment = experiment, deployment
	return nil
}

func (f *fakeExperimentRepo) Get(_ context.Context, _ string, _ string) (domain.Experiment, bool, error) {
	return f.experiment, f.experiment.ID != "", nil
}

func (f *fakeExperimentRepo) SaveDecision(
	_ context.Context, _ string, experiment domain.Experiment, decision domain.Decision, _ domain.StageMetrics,
	idempotencyKey, fingerprint string,
) (domain.Experiment, domain.Decision, error) {
	f.saveDecisionCalls++
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
	f.deployment.CanaryPercent = experiment.Stage
	if experiment.Status == domain.ExperimentRolledBack {
		f.deployment.CanaryPercent = 0
	}
	if experiment.Status == domain.ExperimentCompleted {
		f.deployment.StableRevisionID = experiment.CanaryRevisionID
		f.deployment.CanaryRevisionID = ""
		f.deployment.CanaryPercent = 0
	}
	f.decisions[idempotencyKey] = fakeEvaluationDecision{
		experiment: experiment, decision: decision, fingerprint: fingerprint,
	}
	return experiment, decision, nil
}

func (f *fakeExperimentRepo) ResolveDeployment(_ context.Context, _ string, _, _ string) (domain.Deployment, bool, error) {
	return f.deployment, f.deployment.ResourceID != "", nil
}
