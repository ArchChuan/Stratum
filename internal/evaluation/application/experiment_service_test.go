package application

import (
	"context"
	"fmt"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

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

	experiment, decision, err := svc.EvaluateStage(context.Background(), "tenant-1", "experiment-1", domain.StageMetrics{
		Samples: policy.MinSamples, ObservedMinutes: policy.MinObservationMinutes,
		QualityImprovement: 0.1, QualitySignificant: true, ErrorRateIncrease: 0.02,
	})
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

	experiment, recommendation, err := svc.EvaluateStage(context.Background(), "tenant-1", "experiment-1", domain.StageMetrics{
		Samples: policy.MinSamples, ObservedMinutes: policy.MinObservationMinutes,
		QualityImprovement: 0.1, QualitySignificant: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if recommendation != domain.DecisionPromote || experiment.Status != domain.ExperimentRunning ||
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
			if err != nil || got.Status != tc.status || repo.commandAction != tc.action {
				t.Fatalf("command failed: got=%+v action=%s err=%v", got, repo.commandAction, err)
			}
		})
	}
}

func validCommand(version int64) domain.ExperimentCommand {
	return domain.ExperimentCommand{ActorID: "admin-1", ActorType: domain.ActorTypeAdmin, Reason: "reviewed", IdempotencyKey: "command-1", ExpectedStateVersion: version}
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
	experiment    domain.Experiment
	deployment    domain.Deployment
	commandAction domain.ExperimentCommandAction
}

func (f *fakeExperimentRepo) ApplyCommand(
	_ context.Context, _ string, experimentID string, action domain.ExperimentCommandAction, command domain.ExperimentCommand,
) (domain.Experiment, error) {
	if f.experiment.ID != experimentID || f.experiment.StateVersion != command.ExpectedStateVersion {
		return domain.Experiment{}, domain.ErrExperimentStateConflict
	}
	if f.experiment.Status != domain.ExperimentRunning ||
		(action == domain.CommandPromote && (f.experiment.Recommendation != domain.DecisionPromote || f.experiment.SafetyStopped)) {
		return domain.Experiment{}, domain.ErrExperimentCommandNotAllowed
	}
	f.commandAction = action
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
	_ context.Context, _ string, experiment domain.Experiment, _ domain.Decision, _ domain.StageMetrics,
) error {
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
	return nil
}

func (f *fakeExperimentRepo) ResolveDeployment(_ context.Context, _ string, _, _ string) (domain.Deployment, bool, error) {
	return f.deployment, f.deployment.ResourceID != "", nil
}
