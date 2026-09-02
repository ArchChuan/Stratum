package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
)

func TestJobServiceEnqueuesIdempotentEvaluationRun(t *testing.T) {
	repo := &fakeJobRepo{}
	svc := NewJobService(repo, nil, &fakeCapturer{snapshot: snapFixture()})

	job, err := svc.EnqueueRun(context.Background(), "tenant-1", EnqueueRunInput{
		Resource:        domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "version-2"},
		SuiteRevisionID: "suite-revision-1",
		IdempotencyKey:  "request-1",
		RequestedBy:     "user-1",
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
			RequestedBy:     "user-1",
		},
	}
	repo := &fakeJobRepo{claimed: &job}
	runner := &fakeStoredRunner{}
	svc := NewJobService(repo, runner, nil)

	worked, err := svc.RunOnce(context.Background(), "tenant-1", "worker-1", time.Minute)
	if err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if !worked || repo.completedID != "job-1" || repo.completedResultID != "run-1" ||
		runner.suiteRevisionID != "suite-revision-1" || runner.requestedBy != "user-1" {
		t.Fatalf("job not completed: worked=%v completed=%q result=%q runner=%+v",
			worked, repo.completedID, repo.completedResultID, runner)
	}
}

func TestJobServiceRunOnceRejectsLegacyJobWithoutRequestIdentity(t *testing.T) {
	job := domain.EvaluationJob{
		ID: "job-legacy", Type: domain.JobTypeEvalRun, Status: domain.JobRunning,
		Payload: domain.EvalRunJobPayload{
			Resource: domain.ResourceRef{
				Kind: domain.ResourceKindAgent, ResourceID: "agent-1", RevisionID: "revision-1",
			},
			SuiteRevisionID: "suite-revision-1",
		},
	}
	repo := &fakeJobRepo{claimed: &job}
	runner := &fakeStoredRunner{}
	svc := NewJobService(repo, runner, nil)

	worked, err := svc.RunOnce(context.Background(), "tenant-1", "worker-1", time.Minute)
	if !worked || err == nil || repo.failedID != "job-legacy" || repo.failedMessage == "" {
		t.Fatalf("legacy job was not rejected explicitly: worked=%v err=%v repo=%+v", worked, err, repo)
	}
	if runner.suiteRevisionID != "" {
		t.Fatalf("legacy job reached runner: %+v", runner)
	}
}

func TestJobServiceRunOnceExposesLegacyJobFailureWriteError(t *testing.T) {
	wantErr := errors.New("write failed status")
	job := domain.EvaluationJob{
		ID: "job-legacy", Type: domain.JobTypeEvalRun, Status: domain.JobRunning,
		Payload: domain.EvalRunJobPayload{
			Resource: domain.ResourceRef{
				Kind: domain.ResourceKindAgent, ResourceID: "agent-1", RevisionID: "revision-1",
			},
			SuiteRevisionID: "suite-revision-1",
		},
	}
	repo := &fakeJobRepo{claimed: &job, failErr: wantErr}
	svc := NewJobService(repo, &fakeStoredRunner{}, nil)

	worked, err := svc.RunOnce(context.Background(), "tenant-1", "worker-1", time.Minute)
	if !worked || !errors.Is(err, wantErr) {
		t.Fatalf("failure write error not exposed: worked=%v err=%v", worked, err)
	}
}

func TestJobServiceRunOncePassesSnapshotToRunner(t *testing.T) {
	job := domain.EvaluationJob{
		ID: "job-1", Type: domain.JobTypeEvalRun, Status: domain.JobRunning,
		Payload: domain.EvalRunJobPayload{
			Resource:        domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "version-2"},
			SuiteRevisionID: "suite-revision-1",
			RequestedBy:     "user-1",
			Snapshot:        snapFixture(),
		},
	}
	repo := &fakeJobRepo{claimed: &job}
	runner := &fakeStoredRunner{}
	svc := NewJobService(repo, runner, nil)

	worked, err := svc.RunOnce(context.Background(), "tenant-1", "worker-1", time.Minute)
	if err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if !worked || runner.snapshot == nil || runner.snapshot.SchemaVersion != domain.SnapshotSchemaVersion {
		t.Fatalf("runner did not receive job snapshot: worked=%v runner=%+v", worked, runner)
	}
}

func TestEnqueueRunSnapshotCapturesIntoJobPayload(t *testing.T) {
	repo := &fakeJobRepo{}
	capturer := &fakeCapturer{snapshot: snapFixture()}
	svc := NewJobService(repo, nil, capturer)

	job, err := svc.EnqueueRun(context.Background(), "tenant-1", EnqueueRunInput{
		Resource:        domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "version-2"},
		SuiteRevisionID: "suite-revision-1",
		IdempotencyKey:  "request-1",
		RequestedBy:     "user-1",
	})
	if err != nil {
		t.Fatalf("EnqueueRun returned error: %v", err)
	}
	if capturer.calls != 1 {
		t.Fatalf("capturer calls = %d, want 1", capturer.calls)
	}
	if job.Payload.Snapshot == nil || job.Payload.Snapshot.SchemaVersion != domain.SnapshotSchemaVersion {
		t.Fatalf("expected captured snapshot in job payload, got %+v", job.Payload.Snapshot)
	}
	if repo.enqueued.Payload.Snapshot == nil {
		t.Fatal("snapshot was not persisted with the enqueued job")
	}
}

func TestEnqueueRunSnapshotFailsClosedWhenCapturerErrors(t *testing.T) {
	repo := &fakeJobRepo{}
	capturer := &fakeCapturer{err: errors.New("capture down")}
	svc := NewJobService(repo, nil, capturer)

	_, err := svc.EnqueueRun(context.Background(), "tenant-1", EnqueueRunInput{
		Resource:        domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "version-2"},
		SuiteRevisionID: "suite-revision-1",
		IdempotencyKey:  "request-1",
		RequestedBy:     "user-1",
	})
	if err == nil {
		t.Fatal("EnqueueRun must reject creation when capture fails")
	}
	if repo.enqueued.ID != "" {
		t.Fatalf("job must not be enqueued on capture failure: %+v", repo.enqueued)
	}
}

func TestEnqueueRunSnapshotFailsClosedWhenCapturerNil(t *testing.T) {
	repo := &fakeJobRepo{}
	svc := NewJobService(repo, nil, nil)

	_, err := svc.EnqueueRun(context.Background(), "tenant-1", EnqueueRunInput{
		Resource:        domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "version-2"},
		SuiteRevisionID: "suite-revision-1",
		IdempotencyKey:  "request-1",
		RequestedBy:     "user-1",
	})
	if err == nil {
		t.Fatal("EnqueueRun must reject creation when capturer not configured")
	}
	if repo.enqueued.ID != "" {
		t.Fatalf("job must not be enqueued without a capturer: %+v", repo.enqueued)
	}
}

type fakeJobRepo struct {
	enqueued          domain.EvaluationJob
	claimed           *domain.EvaluationJob
	completedID       string
	completedResultID string
	failedID          string
	failedMessage     string
	failErr           error
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

func (f *fakeJobRepo) Fail(_ context.Context, _ string, jobID, message string) error {
	f.failedID, f.failedMessage = jobID, message
	return f.failErr
}

type fakeStoredRunner struct {
	suiteRevisionID, requestedBy string
	snapshot                     *domain.EvaluationContextSnapshot
}

func (f *fakeStoredRunner) RunStored(
	_ context.Context, _, requestedBy string, _ domain.ResourceRef, suiteRevisionID string,
	snapshot *domain.EvaluationContextSnapshot,
) (domain.EvalRun, error) {
	f.suiteRevisionID = suiteRevisionID
	f.requestedBy = requestedBy
	f.snapshot = snapshot
	return domain.EvalRun{ID: "run-1"}, nil
}

// fakeCapturer 是 port.SnapshotCapturer 的桩：返回固定快照或 error。
type fakeCapturer struct {
	snapshot *domain.EvaluationContextSnapshot
	err      error
	calls    int
}

func (f *fakeCapturer) Capture(_ context.Context, _ string, _ port.CaptureInput) (*domain.EvaluationContextSnapshot, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.snapshot, nil
}
