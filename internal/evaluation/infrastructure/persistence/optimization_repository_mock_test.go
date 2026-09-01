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

func TestPgOptimizationRepository_WithinTransaction_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgOptimizationRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectCommit()

	err := repo.WithinTransaction(context.Background(), "t1", func(ctx context.Context) error {
		return nil
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOptimizationRepository_WithinTransaction_fnErrorRollsBack(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgOptimizationRepository{pool: mock}
	boom := errors.New("boom")

	expectTenantTx(mock)
	mock.ExpectRollback()

	err := repo.WithinTransaction(context.Background(), "t1", func(ctx context.Context) error {
		return boom
	})
	require.ErrorIs(t, err, boom)
}

func TestPgOptimizationRepository_GetByIdempotencyKey_found(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgOptimizationRepository{pool: mock}
	now := time.Now()

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind, resource_id, baseline_revision_id").
		WithArgs("key-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "baseline_revision_id", "suite_revision_id",
			"status", "search_space", "rewrite_config", "request_fingerprint", "created_by", "created_at",
		}).AddRow("job-1", "prompt", "r-1", "rev-1", "suite-1", domain.JobStatus("queued"),
			`{"param":["a","b"]}`, `{"failure_summaries":["f1"]}`, "fp-1", "creator-1", now))
	mock.ExpectQuery("SELECT id, revision_id, parent_revision_id, source, rationale").
		WithArgs("job-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "revision_id", "parent_revision_id", "source", "rationale",
			"generation_metadata", "eval_run_id", "rank", "created_at",
		}).AddRow("cand-1", "rev-2", "rev-1", "optimization", "why",
			`{"temperature":0.5}`, "run-1", 1, now))
	mock.ExpectCommit()

	job, candidates, fingerprint, found, err := repo.GetByIdempotencyKey(context.Background(), "t1", "key-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "job-1", job.ID)
	require.Equal(t, []string{"f1"}, job.FailureSummaries)
	require.Equal(t, "fp-1", fingerprint)
	require.Len(t, candidates, 1)
	require.Equal(t, "cand-1", candidates[0].ID)
	require.Equal(t, 0.5, candidates[0].GenerationMetadata["temperature"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOptimizationRepository_GetByIdempotencyKey_notFound(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgOptimizationRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind, resource_id, baseline_revision_id").
		WithArgs("missing").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectCommit()

	_, _, _, found, err := repo.GetByIdempotencyKey(context.Background(), "t1", "missing")
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOptimizationRepository_GetByIdempotencyKey_badSearchSpace(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgOptimizationRepository{pool: mock}
	now := time.Now()

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind, resource_id, baseline_revision_id").
		WithArgs("key-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "baseline_revision_id", "suite_revision_id",
			"status", "search_space", "rewrite_config", "request_fingerprint", "created_by", "created_at",
		}).AddRow("job-1", "prompt", "r-1", "rev-1", "suite-1", domain.JobStatus("queued"),
			`{bad`, `{}`, "fp-1", "creator-1", now))
	mock.ExpectRollback()

	_, _, _, _, err := repo.GetByIdempotencyKey(context.Background(), "t1", "key-1")
	require.Error(t, err)
	require.ErrorContains(t, err, "decode search space")
}

func TestPgOptimizationRepository_SaveJobWithCandidates_created(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgOptimizationRepository{pool: mock}
	now := time.Now()
	job := domain.OptimizationJob{
		ID:               "job-1",
		Baseline:         domain.ResourceRef{Kind: "prompt", ResourceID: "r-1", RevisionID: "rev-1"},
		SuiteRevisionID:  "suite-1",
		Status:           domain.JobQueued,
		SearchSpace:      map[string][]any{"param": {"a", "b"}},
		FailureSummaries: []string{"f1"},
		CreatedAt:        now,
	}
	candidates := []domain.OptimizationCandidate{{
		ID: "cand-1", OptimizationJobID: "job-1",
		Revision: domain.ResourceRef{RevisionID: "rev-2"}, ParentRevisionID: "rev-1",
		Source: "optimization", Rationale: "why",
		GenerationMetadata: map[string]any{"temperature": 0.5},
		EvalRunID:          "run-1", Rank: 1, CreatedAt: now,
	}}

	expectTenantTx(mock)
	mock.ExpectExec("INSERT INTO optimization_jobs").
		WithArgs("job-1", "prompt", "r-1", "rev-1", "suite-1", "queued",
			`{"param":["a","b"]}`, `{"failure_summaries":["f1"]}`, "", now, "key-1", "fp-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO optimization_candidates").
		WithArgs("cand-1", "job-1", "rev-2", "rev-1", "optimization", "why",
			`{"temperature":0.5}`, "run-1", 1, now).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	created, err := repo.SaveJobWithCandidates(context.Background(), "t1", job, candidates, "key-1", "fp-1")
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOptimizationRepository_SaveJobWithCandidates_idempotentHit(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgOptimizationRepository{pool: mock}
	now := time.Now()
	job := domain.OptimizationJob{ID: "job-1", Baseline: domain.ResourceRef{RevisionID: "rev-1"}, CreatedAt: now}

	expectTenantTx(mock)
	mock.ExpectExec("INSERT INTO optimization_jobs").
		WithArgs("job-1", "", "", "rev-1", "", "", `null`, `{"failure_summaries":null}`, "", now, "key-1", "fp-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 0))
	mock.ExpectQuery("SELECT request_fingerprint FROM optimization_jobs").
		WithArgs("key-1").
		WillReturnRows(pgxmock.NewRows([]string{"request_fingerprint"}).AddRow("fp-1"))
	mock.ExpectCommit()

	created, err := repo.SaveJobWithCandidates(context.Background(), "t1", job, nil, "key-1", "fp-1")
	require.NoError(t, err)
	require.False(t, created)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOptimizationRepository_SaveJobWithCandidates_fingerprintMismatch(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgOptimizationRepository{pool: mock}
	now := time.Now()
	job := domain.OptimizationJob{ID: "job-1", Baseline: domain.ResourceRef{RevisionID: "rev-1"}, CreatedAt: now}

	expectTenantTx(mock)
	mock.ExpectExec("INSERT INTO optimization_jobs").
		WithArgs("job-1", "", "", "rev-1", "", "", `null`, `{"failure_summaries":null}`, "", now, "key-1", "fp-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 0))
	mock.ExpectQuery("SELECT request_fingerprint FROM optimization_jobs").
		WithArgs("key-1").
		WillReturnRows(pgxmock.NewRows([]string{"request_fingerprint"}).AddRow("other-fp"))
	mock.ExpectRollback()

	_, err := repo.SaveJobWithCandidates(context.Background(), "t1", job, nil, "key-1", "fp-1")
	require.ErrorIs(t, err, domain.ErrOptimizationIdempotencyConflict)
}

func TestPgOptimizationRepository_SaveJobWithCandidates_marshalSearchSpaceFails(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgOptimizationRepository{pool: mock}
	job := domain.OptimizationJob{SearchSpace: map[string][]any{"bad": {make(chan int)}}}

	_, err := repo.SaveJobWithCandidates(context.Background(), "t1", job, nil, "key-1", "fp-1")
	require.Error(t, err)
	require.ErrorContains(t, err, "marshal search space")
}

func TestPgOptimizationRepository_SaveJobWithCandidates_candidateMarshalFails(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgOptimizationRepository{pool: mock}
	now := time.Now()
	job := domain.OptimizationJob{ID: "job-1", Baseline: domain.ResourceRef{RevisionID: "rev-1"}, CreatedAt: now}
	candidates := []domain.OptimizationCandidate{{
		ID: "cand-1", GenerationMetadata: map[string]any{"bad": make(chan int)},
	}}

	expectTenantTx(mock)
	mock.ExpectExec("INSERT INTO optimization_jobs").
		WithArgs("job-1", "", "", "rev-1", "", "", `null`, `{"failure_summaries":null}`, "", now, "key-1", "fp-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectRollback()

	_, err := repo.SaveJobWithCandidates(context.Background(), "t1", job, candidates, "key-1", "fp-1")
	require.Error(t, err)
	require.ErrorContains(t, err, "marshal candidate metadata")
}
