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

type ExperimentCommandInput struct {
	ActorID              string
	Reason               string
	IdempotencyKey       string
	ExpectedStateVersion int64
}

type EvaluateStageInput struct {
	Metrics        domain.StageMetrics
	IdempotencyKey string
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
		StateVersion: 1, Recommendation: domain.DecisionHold,
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

func (s *ExperimentService) Pause(
	ctx context.Context, tenantID, experimentID string, command ExperimentCommandInput,
) (domain.Experiment, error) {
	return s.applyCommand(ctx, tenantID, experimentID, domain.CommandPause, command)
}

func (s *ExperimentService) Promote(
	ctx context.Context, tenantID, experimentID string, command ExperimentCommandInput,
) (domain.Experiment, error) {
	return s.applyCommand(ctx, tenantID, experimentID, domain.CommandPromote, command)
}

func (s *ExperimentService) Rollback(
	ctx context.Context, tenantID, experimentID string, command ExperimentCommandInput,
) (domain.Experiment, error) {
	return s.applyCommand(ctx, tenantID, experimentID, domain.CommandRollback, command)
}

func (s *ExperimentService) applyCommand(
	ctx context.Context,
	tenantID, experimentID string,
	action domain.ExperimentCommandAction,
	input ExperimentCommandInput,
) (domain.Experiment, error) {
	command := domain.ExperimentCommand{
		ActorID: input.ActorID, ActorType: domain.ActorTypeAdmin, Reason: input.Reason,
		IdempotencyKey: input.IdempotencyKey, ExpectedStateVersion: input.ExpectedStateVersion,
	}
	if err := command.Validate(); err != nil {
		return domain.Experiment{}, err
	}
	return s.repo.ApplyCommand(ctx, tenantID, experimentID, action, command)
}

func (s *ExperimentService) EvaluateStage(
	ctx context.Context,
	tenantID, experimentID string,
	metrics domain.StageMetrics,
) (domain.Experiment, domain.Decision, error) {
	return s.EvaluateStageIdempotent(ctx, tenantID, experimentID, EvaluateStageInput{
		Metrics: metrics, IdempotencyKey: uuid.NewString(),
	})
}

func (s *ExperimentService) EvaluateStageIdempotent(
	ctx context.Context,
	tenantID, experimentID string,
	input EvaluateStageInput,
) (domain.Experiment, domain.Decision, error) {
	if input.IdempotencyKey == "" {
		return domain.Experiment{}, domain.DecisionHold, errors.New("evaluation idempotency key is required")
	}
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
	next, decision := experiment.Decide(input.Metrics, policy)
	next.StateVersion = experiment.StateVersion + 1
	stored, storedDecision, err := s.repo.SaveDecision(
		ctx, tenantID, next, decision, input.Metrics, input.IdempotencyKey, domain.MetricsFingerprint(input.Metrics),
	)
	if err != nil {
		return domain.Experiment{}, domain.DecisionHold, err
	}
	return stored, storedDecision, nil
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
