package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

func TestPgJobRepository_Enqueue_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgJobRepository{pool: mock}
	now := time.Now()
	job := domain.EvaluationJob{
		ID:             "job-1",
		Type:           domain.JobTypeEvalRun,
		Payload:        domain.EvalRunJobPayload{RequestedBy: "user-1"},
		Status:         domain.JobQueued,
		IdempotencyKey: "key-1",
		CreatedAt:      now,
	}

	expectTenantTx(mock)
	mock.ExpectQuery("INSERT INTO evaluation_jobs").
		WithArgs("job-1", domain.JobTypeEvalRun, `{"resource":{"kind":"","resource_id":"","revision_id":""},"suite_revision_id":"","requested_by":"user-1"}`, "queued", "key-1", now).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "job_type", "payload", "status", "attempts", "idempotency_key", "error_message", "result_id", "created_at",
		}).AddRow("job-1", domain.JobTypeEvalRun, []byte(`{"requested_by":"user-1"}`), "queued", 0, "key-1", "", "", now))
	mock.ExpectCommit()

	saved, err := repo.Enqueue(context.Background(), "t1", job)
	require.NoError(t, err)
	require.Equal(t, "job-1", saved.ID)
	require.Equal(t, domain.JobQueued, saved.Status)
	require.Equal(t, "user-1", saved.Payload.RequestedBy)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgJobRepository_Enqueue_unmarshalPayloadFails(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgJobRepository{pool: mock}
	now := time.Now()

	expectTenantTx(mock)
	mock.ExpectQuery("INSERT INTO evaluation_jobs").
		WithArgs("job-1", "", pgxmock.AnyArg(), "", "", now).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "job_type", "payload", "status", "attempts", "idempotency_key", "error_message", "result_id", "created_at",
		}).AddRow("job-1", "eval_run", []byte(`{not-json`), "queued", 0, "", "", "", now))
	mock.ExpectRollback()

	_, err := repo.Enqueue(context.Background(), "t1", domain.EvaluationJob{ID: "job-1", CreatedAt: now})
	require.Error(t, err)
	require.ErrorContains(t, err, "unmarshal payload")
}

func TestPgJobRepository_Get_found(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgJobRepository{pool: mock}
	now := time.Now()

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, job_type, payload, status, attempts, idempotency_key, error_message, result_id, created_at").
		WithArgs("job-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "job_type", "payload", "status", "attempts", "idempotency_key", "error_message", "result_id", "created_at",
		}).AddRow("job-1", "eval_run", []byte(`{"requested_by":"u"}`), "failed", 2, "key-1", "boom", "res-1", now))
	mock.ExpectCommit()

	job, found, err := repo.Get(context.Background(), "t1", "job-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, domain.JobFailed, job.Status)
	require.Equal(t, 2, job.Attempts)
	require.Equal(t, "boom", job.ErrorMessage)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgJobRepository_Get_notFound(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgJobRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, job_type, payload").
		WithArgs("missing").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectCommit()

	_, found, err := repo.Get(context.Background(), "t1", "missing")
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgJobRepository_Claim_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgJobRepository{pool: mock}
	now := time.Now()
	lease := 30 * time.Second

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, job_type, payload, status, attempts").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "job_type", "payload", "status", "attempts", "idempotency_key", "error_message", "result_id", "created_at",
		}).AddRow("job-1", "eval_run", []byte(`{"requested_by":"u"}`), "queued", 1, "key-1", "", "", now))
	mock.ExpectExec("UPDATE evaluation_jobs").
		WithArgs("job-1", "worker-1", float64(30)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	job, err := repo.Claim(context.Background(), "t1", "worker-1", lease)
	require.NoError(t, err)
	require.NotNil(t, job)
	require.Equal(t, domain.JobRunning, job.Status)
	require.Equal(t, 2, job.Attempts)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgJobRepository_Claim_noJob(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgJobRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, job_type, payload, status, attempts").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectCommit()

	job, err := repo.Claim(context.Background(), "t1", "worker-1", 30*time.Second)
	require.NoError(t, err)
	require.Nil(t, job)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgJobRepository_Complete_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgJobRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectExec("UPDATE evaluation_jobs").
		WithArgs("job-1", "succeeded", "res-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Complete(context.Background(), "t1", "job-1", "res-1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgJobRepository_Complete_updateFails(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgJobRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectExec("UPDATE evaluation_jobs").
		WithArgs("job-1", "succeeded", "res-1").
		WillReturnError(errors.New("connection lost"))
	mock.ExpectRollback()

	err := repo.Complete(context.Background(), "t1", "job-1", "res-1")
	require.Error(t, err)
	require.ErrorContains(t, err, "connection lost")
}

func TestPgJobRepository_Fail_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgJobRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectExec("UPDATE evaluation_jobs").
		WithArgs("job-1", "failed", "timeout").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Fail(context.Background(), "t1", "job-1", "timeout"))
	require.NoError(t, mock.ExpectationsWereMet())
}
