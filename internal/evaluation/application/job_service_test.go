package application

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

func TestJobServiceEnqueuesIdempotentEvaluationRun(t *testing.T) {
	repo := &fakeJobRepo{}
	svc := NewJobService(repo, nil)

	job, err := svc.EnqueueRun(context.Background(), "tenant-1", EnqueueRunInput{
		Resource:        domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "version-2"},
		SuiteRevisionID: "suite-revision-1",
		IdempotencyKey:  "request-1",
	})
	if err != nil {
		t.Fatalf("EnqueueRun returned error: %v", err)
	}
	if job.ID == "" || job.Status != domain.JobQueued || repo.enqueued.IdempotencyKey != "request-1" {
		t.Fatalf("unexpected job: %+v", job)
	}
}

func TestJobServiceRunOnceExecutesClaimedEvaluation(t *testing.T) {
	job := domain.EvaluationJob{
		ID: "job-1", Type: domain.JobTypeEvalRun, Status: domain.JobRunning,
		Payload: domain.EvalRunJobPayload{
			Resource:        domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "version-2"},
			SuiteRevisionID: "suite-revision-1",
		},
	}
	repo := &fakeJobRepo{claimed: &job}
	runner := &fakeStoredRunner{}
	svc := NewJobService(repo, runner)

	worked, err := svc.RunOnce(context.Background(), "tenant-1", "worker-1", time.Minute)
	if err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if !worked || repo.completedID != "job-1" || repo.completedResultID != "run-1" || runner.suiteRevisionID != "suite-revision-1" {
		t.Fatalf("job not completed: worked=%v completed=%q result=%q runner=%+v",
			worked, repo.completedID, repo.completedResultID, runner)
	}
}

type fakeJobRepo struct {
	enqueued          domain.EvaluationJob
	claimed           *domain.EvaluationJob
	completedID       string
	completedResultID string
}

func (f *fakeJobRepo) Enqueue(_ context.Context, _ string, job domain.EvaluationJob) (domain.EvaluationJob, error) {
	f.enqueued = job
	return job, nil
}

func (f *fakeJobRepo) Get(_ context.Context, _ string, jobID string) (domain.EvaluationJob, bool, error) {
	if f.enqueued.ID == jobID {
		return f.enqueued, true, nil
	}
	return domain.EvaluationJob{}, false, nil
}

func (f *fakeJobRepo) Claim(_ context.Context, _ string, _ string, _ time.Duration) (*domain.EvaluationJob, error) {
	return f.claimed, nil
}

func (f *fakeJobRepo) Complete(_ context.Context, _ string, jobID, resultID string) error {
	f.completedID = jobID
	f.completedResultID = resultID
	return nil
}

func (f *fakeJobRepo) Fail(_ context.Context, _ string, _ string, _ string) error { return nil }

type fakeStoredRunner struct{ suiteRevisionID string }

func (f *fakeStoredRunner) RunStored(_ context.Context, _ string, _ domain.ResourceRef, suiteRevisionID string) (domain.EvalRun, error) {
	f.suiteRevisionID = suiteRevisionID
	return domain.EvalRun{ID: "run-1"}, nil
}
