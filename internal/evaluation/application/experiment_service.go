package application

import (
	"context"
	"errors"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/google/uuid"
)

var ErrExperimentNotFound = errors.New("evaluation experiment not found")

type CreateExperimentInput struct {
	Stable          domain.ResourceRef
	Canary          domain.ResourceRef
	SuiteRevisionID string
}

type ExperimentService struct {
	repo port.ExperimentRepository
}

func NewExperimentService(repo port.ExperimentRepository) *ExperimentService {
	return &ExperimentService{repo: repo}
}

func (s *ExperimentService) Create(
	ctx context.Context,
	tenantID string,
	input CreateExperimentInput,
) (domain.Experiment, domain.Deployment, error) {
	if err := input.Stable.Validate(); err != nil {
		return domain.Experiment{}, domain.Deployment{}, err
	}
	if err := input.Canary.Validate(); err != nil {
		return domain.Experiment{}, domain.Deployment{}, err
	}
	if input.Stable.Kind != input.Canary.Kind || input.Stable.ResourceID != input.Canary.ResourceID ||
		input.Stable.RevisionID == input.Canary.RevisionID {
		return domain.Experiment{}, domain.Deployment{}, errors.New("stable and canary must be different revisions of the same resource")
	}
	policy := domain.DefaultPromotionPolicy()
	experiment := domain.Experiment{
		ID: uuid.Must(uuid.NewV7()).String(), ResourceKind: input.Stable.Kind, ResourceID: input.Stable.ResourceID,
		StableRevisionID: input.Stable.RevisionID, CanaryRevisionID: input.Canary.RevisionID,
		SuiteRevisionID: input.SuiteRevisionID, Status: domain.ExperimentRunning, Stage: policy.Stages[0], Policy: policy,
	}
	deployment := domain.Deployment{
		ResourceKind: input.Stable.Kind, ResourceID: input.Stable.ResourceID,
		StableRevisionID: input.Stable.RevisionID, CanaryRevisionID: input.Canary.RevisionID,
		CanaryPercent: experiment.Stage, ExperimentID: experiment.ID, PolicyVersion: 1,
	}
	if err := s.repo.Create(ctx, tenantID, experiment, deployment); err != nil {
		return domain.Experiment{}, domain.Deployment{}, err
	}
	return experiment, deployment, nil
}

func (s *ExperimentService) EvaluateStage(
	ctx context.Context,
	tenantID, experimentID string,
	metrics domain.StageMetrics,
) (domain.Experiment, domain.Decision, error) {
	experiment, ok, err := s.repo.Get(ctx, tenantID, experimentID)
	if err != nil {
		return domain.Experiment{}, domain.DecisionHold, err
	}
	if !ok {
		return domain.Experiment{}, domain.DecisionHold, ErrExperimentNotFound
	}
	policy := experiment.Policy
	if len(policy.Stages) == 0 {
		policy = domain.DefaultPromotionPolicy()
	}
	next, decision := experiment.Decide(metrics, policy)
	if err := s.repo.SaveDecision(ctx, tenantID, next, decision, metrics); err != nil {
		return domain.Experiment{}, domain.DecisionHold, err
	}
	return next, decision, nil
}

func (s *ExperimentService) ResolveRevision(
	ctx context.Context,
	tenantID string,
	resourceKind domain.ResourceKind,
	resourceID, subjectID string,
) (string, bool, error) {
	assignment, ok, err := s.ResolveAssignment(ctx, tenantID, resourceKind, resourceID, subjectID)
	return assignment.RevisionID, ok, err
}

func (s *ExperimentService) ResolveAssignment(
	ctx context.Context,
	tenantID string,
	resourceKind domain.ResourceKind,
	resourceID, subjectID string,
) (domain.RevisionAssignment, bool, error) {
	deployment, ok, err := s.repo.ResolveDeployment(ctx, tenantID, string(resourceKind), resourceID)
	if err != nil || !ok {
		return domain.RevisionAssignment{}, ok, err
	}
	if deployment.CanaryRevisionID != "" && domain.AssignVariant(subjectID+":"+resourceID, deployment.CanaryPercent) {
		return domain.RevisionAssignment{
			RevisionID: deployment.CanaryRevisionID, ExperimentID: deployment.ExperimentID, Variant: "canary",
		}, true, nil
	}
	return domain.RevisionAssignment{
		RevisionID: deployment.StableRevisionID, ExperimentID: deployment.ExperimentID, Variant: "stable",
	}, true, nil
}
