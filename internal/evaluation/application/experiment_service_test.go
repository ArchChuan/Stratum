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

func TestExperimentServiceAppliesRollbackDecision(t *testing.T) {
	repo := &fakeExperimentRepo{experiment: domain.Experiment{
		ID: "experiment-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
		StableRevisionID: "version-1", CanaryRevisionID: "candidate-1", Status: domain.ExperimentRunning, Stage: 20,
	}}
	svc := NewExperimentService(repo)
	policy := domain.DefaultPromotionPolicy()

	experiment, decision, err := svc.EvaluateStage(context.Background(), "tenant-1", "experiment-1", domain.StageMetrics{
		Samples: policy.MinSamples, ObservedMinutes: policy.MinObservationMinutes,
		QualityImprovement: 0.1, QualitySignificant: true, ErrorRateIncrease: 0.02,
	})
	if err != nil {
		t.Fatalf("EvaluateStage returned error: %v", err)
	}
	if decision != domain.DecisionRollback || experiment.Status != domain.ExperimentRolledBack || repo.deployment.CanaryPercent != 0 {
		t.Fatalf("rollback not applied: decision=%s experiment=%+v deployment=%+v", decision, experiment, repo.deployment)
	}
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
	experiment domain.Experiment
	deployment domain.Deployment
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
