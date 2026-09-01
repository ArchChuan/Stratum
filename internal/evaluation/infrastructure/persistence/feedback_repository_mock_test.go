package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

func TestPgFeedbackRepository_Record_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgFeedbackRepository{pool: mock}
	now := time.Now()
	input := domain.FeedbackRequest{
		TraceID:        "tr-1",
		ResourceKind:   domain.ResourceKind("prompt"),
		ResourceID:     "r-1",
		RevisionID:     "rev-1",
		ExperimentID:   "exp-1",
		Variant:        "stable",
		Score:          0.9,
		Outcome:        map[string]any{"ok": true, "password": "secret"},
		IdempotencyKey: "key-1",
	}

	expectTenantTx(mock)
	mock.ExpectQuery("INSERT INTO evaluation_feedback").
		WithArgs(pgxmock.AnyArg(), "tr-1", "prompt", "r-1", "rev-1", "exp-1", "stable", 0.9,
			`{"ok":true,"password":"[REDACTED]"}`, "key-1", "").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "trace_id", "resource_kind", "resource_id", "revision_id", "experiment_id", "variant",
			"score", "outcome", "idempotency_key", "created_by", "created_at",
		}).AddRow("fb-1", "tr-1", "prompt", "r-1", "rev-1", "exp-1", "stable", 0.9,
			[]byte(`{"ok":true,"password":"[REDACTED]"}`), "key-1", "", now))
	mock.ExpectCommit()

	feedback, err := repo.Record(context.Background(), "t1", input)
	require.NoError(t, err)
	require.Equal(t, "fb-1", feedback.ID)
	require.Equal(t, "prompt", string(feedback.ResourceKind))
	require.Equal(t, "[REDACTED]", feedback.Outcome["password"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgFeedbackRepository_Record_securityViolationAdded(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgFeedbackRepository{pool: mock}
	now := time.Now()
	input := domain.FeedbackRequest{
		TraceID:           "tr-1",
		ResourceKind:      domain.ResourceKind("prompt"),
		ResourceID:        "r-1",
		SecurityViolation: true,
		Outcome:           map[string]any{},
	}

	expectTenantTx(mock)
	mock.ExpectQuery("INSERT INTO evaluation_feedback").
		WithArgs(pgxmock.AnyArg(), "tr-1", "prompt", "r-1", "", "", "", 0.0,
			`{"security_violation":true}`, "", "").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "trace_id", "resource_kind", "resource_id", "revision_id", "experiment_id", "variant",
			"score", "outcome", "idempotency_key", "created_by", "created_at",
		}).AddRow("fb-1", "tr-1", "prompt", "r-1", "", "", "", 0.0,
			[]byte(`{"security_violation":true}`), "", "", now))
	mock.ExpectCommit()

	feedback, err := repo.Record(context.Background(), "t1", input)
	require.NoError(t, err)
	require.True(t, feedback.Outcome["security_violation"].(bool))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgFeedbackRepository_Record_idempotentConflict(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgFeedbackRepository{pool: mock}
	input := domain.FeedbackRequest{
		TraceID:        "tr-1",
		ResourceKind:   domain.ResourceKind("prompt"),
		ResourceID:     "r-1",
		IdempotencyKey: "key-1",
	}

	expectTenantTx(mock)
	mock.ExpectQuery("INSERT INTO evaluation_feedback").
		WithArgs(pgxmock.AnyArg(), "tr-1", "prompt", "r-1", "", "", "", 0.0, `{}`, "key-1", "").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("SELECT COUNT\\(\\*\\), COUNT\\(\\*\\) FILTER").
		WithArgs("tr-1", "prompt", "r-1", "", "", "", 0.0, `{}`, "key-1").
		WillReturnRows(pgxmock.NewRows([]string{"matches", "exact"}).AddRow(2, 1))
	mock.ExpectRollback()

	_, err := repo.Record(context.Background(), "t1", input)
	require.ErrorIs(t, err, domain.ErrFeedbackIdempotencyConflict)
}

func TestPgFeedbackRepository_Record_existingRowReturned(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgFeedbackRepository{pool: mock}
	now := time.Now()
	input := domain.FeedbackRequest{
		TraceID:        "tr-1",
		ResourceKind:   domain.ResourceKind("prompt"),
		ResourceID:     "r-1",
		IdempotencyKey: "key-1",
	}

	expectTenantTx(mock)
	mock.ExpectQuery("INSERT INTO evaluation_feedback").
		WithArgs(pgxmock.AnyArg(), "tr-1", "prompt", "r-1", "", "", "", 0.0, `{}`, "key-1", "").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("SELECT COUNT\\(\\*\\), COUNT\\(\\*\\) FILTER").
		WithArgs("tr-1", "prompt", "r-1", "", "", "", 0.0, `{}`, "key-1").
		WillReturnRows(pgxmock.NewRows([]string{"matches", "exact"}).AddRow(1, 1))
	mock.ExpectQuery("SELECT id, trace_id, resource_kind, resource_id, revision_id").
		WithArgs("key-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "trace_id", "resource_kind", "resource_id", "revision_id", "experiment_id", "variant",
			"score", "outcome", "idempotency_key", "created_by", "created_at",
		}).AddRow("fb-1", "tr-1", "prompt", "r-1", "", "", "", 0.0,
			[]byte(`{"ok":true}`), "key-1", "", now))
	mock.ExpectCommit()

	feedback, err := repo.Record(context.Background(), "t1", input)
	require.NoError(t, err)
	require.Equal(t, "fb-1", feedback.ID)
	require.Equal(t, "key-1", feedback.IdempotencyKey)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgFeedbackRepository_ActiveExperiment_found(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgFeedbackRepository{pool: mock}

	// deployment lookup transaction
	expectTenantTx(mock)
	mock.ExpectQuery("SELECT experiment_id FROM evaluation_deployments").
		WithArgs("prompt", "r-1").
		WillReturnRows(pgxmock.NewRows([]string{"experiment_id"}).AddRow("exp-1"))
	mock.ExpectCommit()
	// follow-up experiment Get transaction
	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind, resource_id, stable_revision_id, canary_revision_id").
		WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "stable_revision_id", "canary_revision_id",
			"suite_revision_id", "status", "stage_percent", "policy", "state_version", "recommendation", "safety_stopped", "created_by",
		}).AddRow("exp-1", "prompt", "r-1", "stable-1", "canary-1", "suite-1", "running", 10,
			[]byte(`{"auto_promote":false}`), int64(1), domain.Decision("hold"), false, ""))
	mock.ExpectCommit()

	experiment, found, err := repo.ActiveExperiment(context.Background(), "t1", "prompt", "r-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "exp-1", experiment.ID)
	require.Equal(t, "stable-1", experiment.StableRevisionID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgFeedbackRepository_ActiveExperiment_notFound(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgFeedbackRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT experiment_id FROM evaluation_deployments").
		WithArgs("prompt", "r-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectCommit()

	_, found, err := repo.ActiveExperiment(context.Background(), "t1", "prompt", "r-1")
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgFeedbackRepository_StageFeedback_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgFeedbackRepository{pool: mock}
	stageStartedAt := time.Now().Add(-5 * time.Minute)
	experiment := domain.Experiment{
		ID: "exp-1", ResourceKind: domain.ResourceKind("prompt"), ResourceID: "r-1",
		StableRevisionID: "stable-1", CanaryRevisionID: "canary-1",
	}

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT stage_started_at FROM evaluation_experiments").
		WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{"stage_started_at"}).AddRow(stageStartedAt))
	mock.ExpectQuery("SELECT id, trace_id, resource_kind, resource_id, revision_id, experiment_id, variant").
		WithArgs("prompt", "r-1", "exp-1", stageStartedAt, "stable-1", "canary-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "trace_id", "resource_kind", "resource_id", "revision_id", "experiment_id", "variant",
			"score", "outcome", "idempotency_key", "created_at",
		}).AddRow("fb-1", "tr-1", "prompt", "r-1", "stable-1", "exp-1", "stable", 0.9,
			[]byte(`{"ok":true}`), "key-1", time.Now()).
			AddRow("fb-2", "tr-2", "prompt", "r-1", "canary-1", "exp-1", "canary", 0.8,
				[]byte(`{"ok":false}`), "key-2", time.Now()))
	mock.ExpectCommit()

	feedback, minutes, err := repo.StageFeedback(context.Background(), "t1", experiment)
	require.NoError(t, err)
	require.GreaterOrEqual(t, minutes, 4)
	require.Len(t, feedback, 2)
	require.Equal(t, "fb-1", feedback[0].ID)
	require.Equal(t, "canary", feedback[1].Variant)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgFeedbackRepository_StageFeedback_noStage(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgFeedbackRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT stage_started_at FROM evaluation_experiments").
		WithArgs("exp-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	_, _, err := repo.StageFeedback(context.Background(), "t1", domain.Experiment{ID: "exp-1"})
	require.Error(t, err)
}
