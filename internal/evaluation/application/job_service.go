package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/google/uuid"
)

type StoredRunner interface {
	RunStored(ctx context.Context, tenantID string, resource domain.ResourceRef, suiteRevisionID string) (domain.EvalRun, error)
}

var ErrJobNotFound = errors.New("evaluation job not found")

type EnqueueRunInput struct {
	Resource        domain.ResourceRef
	SuiteRevisionID string
	IdempotencyKey  string
}

type JobService struct {
	repo   port.JobRepository
	runner StoredRunner
}

func NewJobService(repo port.JobRepository, runner StoredRunner) *JobService {
	return &JobService{repo: repo, runner: runner}
}

func (s *JobService) EnqueueRun(ctx context.Context, tenantID string, input EnqueueRunInput) (domain.EvaluationJob, error) {
	if err := input.Resource.Validate(); err != nil {
		return domain.EvaluationJob{}, err
	}
	if input.SuiteRevisionID == "" {
		return domain.EvaluationJob{}, errors.New("suite revision id required")
	}
	if input.IdempotencyKey == "" {
		return domain.EvaluationJob{}, errors.New("idempotency key required")
	}
	job := domain.EvaluationJob{
		ID: uuid.Must(uuid.NewV7()).String(), Type: domain.JobTypeEvalRun, Status: domain.JobQueued,
		Payload:        domain.EvalRunJobPayload{Resource: input.Resource, SuiteRevisionID: input.SuiteRevisionID},
		IdempotencyKey: input.IdempotencyKey, CreatedAt: time.Now().UTC(),
	}
	return s.repo.Enqueue(ctx, tenantID, job)
}

func (s *JobService) Get(ctx context.Context, tenantID, jobID string) (domain.EvaluationJob, error) {
	job, ok, err := s.repo.Get(ctx, tenantID, jobID)
	if err != nil {
		return domain.EvaluationJob{}, err
	}
	if !ok {
		return domain.EvaluationJob{}, ErrJobNotFound
	}
	return job, nil
}

func (s *JobService) RunOnce(
	ctx context.Context,
	tenantID, workerID string,
	lease time.Duration,
) (bool, error) {
	job, err := s.repo.Claim(ctx, tenantID, workerID, lease)
	if err != nil || job == nil {
		return false, err
	}
	if job.Type != domain.JobTypeEvalRun {
		err := fmt.Errorf("unsupported evaluation job type: %s", job.Type)
		_ = s.repo.Fail(ctx, tenantID, job.ID, err.Error())
		return true, err
	}
	if s.runner == nil {
		err := errors.New("stored evaluation runner not configured")
		_ = s.repo.Fail(ctx, tenantID, job.ID, err.Error())
		return true, err
	}
	run, err := s.runner.RunStored(ctx, tenantID, job.Payload.Resource, job.Payload.SuiteRevisionID)
	if err != nil {
		_ = s.repo.Fail(ctx, tenantID, job.ID, err.Error())
		return true, err
	}
	if err := s.repo.Complete(ctx, tenantID, job.ID, run.ID); err != nil {
		return true, err
	}
	return true, nil
}
