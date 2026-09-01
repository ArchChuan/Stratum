package persistence

import (
	"context"
	"encoding/json"
	"testing"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

// deleteAudit 构造与 newEvaluationDeleteAudit 同构的删除审计事件。
func deleteAudit(resourceID string) *auditdomain.ResourceChangeAuditEvent {
	return &auditdomain.ResourceChangeAuditEvent{
		ResourceKind: auditdomain.ResourceKindEvaluation,
		ResourceID:   resourceID,
		Operation:    auditdomain.ChangeOpDelete,
		ActorID:      "creator-1",
		ActorType:    auditdomain.ChangeActorUser,
		Source:       auditdomain.ChangeSourceAPI,
	}
}

// expectAuditInsert 期望同事务写一条删除变更审计（InsertChangeAudit 的 11 参）。
// Normalized 会把空 Before/After 填为 {}，因此末两参为 json.RawMessage("{}")。
func expectAuditInsert(mock pgxmock.PgxPoolIface, resourceID string) {
	mock.ExpectExec("INSERT INTO resource_change_audits").
		WithArgs(pgxmock.AnyArg(), "t1", "evaluation", resourceID, "delete", "creator-1", "user", "api", "",
			json.RawMessage("{}"), json.RawMessage("{}")).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
}

func TestPgSuiteRepository_DeleteSuite_referenced(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgSuiteRepository{pool: mock}
	expectTenantTx(mock)
	mock.ExpectQuery("SELECT EXISTS").WithArgs("suite-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()

	err := repo.DeleteSuite(context.Background(), "t1", "suite-1", deleteAudit("suite-1"))
	require.ErrorIs(t, err, domain.ErrEntityReferenced)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSuiteRepository_DeleteSuite_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgSuiteRepository{pool: mock}
	expectTenantTx(mock)
	mock.ExpectQuery("SELECT EXISTS").WithArgs("suite-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec("DELETE FROM eval_suites").WithArgs("suite-1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	expectAuditInsert(mock, "suite-1")
	mock.ExpectCommit()

	err := repo.DeleteSuite(context.Background(), "t1", "suite-1", deleteAudit("suite-1"))
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgRunRepository_DeleteRun_referencedByCandidate(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgRunRepository{pool: mock}
	expectTenantTx(mock)
	mock.ExpectQuery("SELECT EXISTS").WithArgs("run-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()

	err := repo.DeleteRun(context.Background(), "t1", "run-1", deleteAudit("run-1"))
	require.ErrorIs(t, err, domain.ErrEntityReferenced)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgRunRepository_DeleteRun_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgRunRepository{pool: mock}
	expectTenantTx(mock)
	mock.ExpectQuery("SELECT EXISTS").WithArgs("run-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec("DELETE FROM eval_runs").WithArgs("run-1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	expectAuditInsert(mock, "run-1")
	mock.ExpectCommit()

	err := repo.DeleteRun(context.Background(), "t1", "run-1", deleteAudit("run-1"))
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgJobRepository_DeleteJob_runningRejected(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgJobRepository{pool: mock}
	expectTenantTx(mock)
	mock.ExpectQuery("SELECT status FROM evaluation_jobs").WithArgs("job-1").
		WillReturnRows(pgxmock.NewRows([]string{"status"}).AddRow("running"))
	mock.ExpectRollback()

	err := repo.DeleteJob(context.Background(), "t1", "job-1", deleteAudit("job-1"))
	require.ErrorIs(t, err, domain.ErrEntityReferenced)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgJobRepository_DeleteJob_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgJobRepository{pool: mock}
	expectTenantTx(mock)
	mock.ExpectQuery("SELECT status FROM evaluation_jobs").WithArgs("job-1").
		WillReturnRows(pgxmock.NewRows([]string{"status"}).AddRow("succeeded"))
	mock.ExpectExec("DELETE FROM evaluation_jobs").WithArgs("job-1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	expectAuditInsert(mock, "job-1")
	mock.ExpectCommit()

	err := repo.DeleteJob(context.Background(), "t1", "job-1", deleteAudit("job-1"))
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgExperimentRepository_DeleteExperiment_notCompletedRejected(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}
	expectTenantTx(mock)
	mock.ExpectQuery("SELECT status FROM evaluation_experiments").WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{"status"}).AddRow("running"))
	mock.ExpectRollback()

	err := repo.DeleteExperiment(context.Background(), "t1", "exp-1", deleteAudit("exp-1"))
	require.ErrorIs(t, err, domain.ErrEntityReferenced)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgExperimentRepository_DeleteExperiment_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}
	expectTenantTx(mock)
	mock.ExpectQuery("SELECT status FROM evaluation_experiments").WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{"status"}).AddRow("completed"))
	mock.ExpectQuery("SELECT EXISTS").WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec("DELETE FROM evaluation_experiments").WithArgs("exp-1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	expectAuditInsert(mock, "exp-1")
	mock.ExpectCommit()

	err := repo.DeleteExperiment(context.Background(), "t1", "exp-1", deleteAudit("exp-1"))
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgOptimizationRepository_DeleteCandidate_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgOptimizationRepository{pool: mock}
	expectTenantTx(mock)
	mock.ExpectExec("DELETE FROM optimization_candidates").WithArgs("cand-1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	expectAuditInsert(mock, "cand-1")
	mock.ExpectCommit()

	err := repo.DeleteCandidate(context.Background(), "t1", "cand-1", deleteAudit("cand-1"))
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgReviewRepository_DeleteReviewItem_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgReviewRepository{pool: mock}
	expectTenantTx(mock)
	mock.ExpectExec("DELETE FROM eval_review_items").WithArgs("rev-1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	expectAuditInsert(mock, "rev-1")
	mock.ExpectCommit()

	err := repo.DeleteReviewItem(context.Background(), "t1", "rev-1", deleteAudit("rev-1"))
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgFeedbackRepository_DeleteFeedback_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgFeedbackRepository{pool: mock}
	expectTenantTx(mock)
	mock.ExpectExec("DELETE FROM evaluation_feedback").WithArgs("fb-1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	expectAuditInsert(mock, "fb-1")
	mock.ExpectCommit()

	err := repo.DeleteFeedback(context.Background(), "t1", "fb-1", deleteAudit("fb-1"))
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSuiteRepository_GetSuiteCreatedBy_found(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgSuiteRepository{pool: mock}
	expectTenantTx(mock)
	mock.ExpectQuery("SELECT created_by FROM eval_suites").WithArgs("suite-1").
		WillReturnRows(pgxmock.NewRows([]string{"created_by"}).AddRow("creator-1"))
	mock.ExpectCommit()

	createdBy, found, err := repo.GetSuiteCreatedBy(context.Background(), "t1", "suite-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "creator-1", createdBy)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSuiteRepository_GetSuiteCreatedBy_notFound(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgSuiteRepository{pool: mock}
	expectTenantTx(mock)
	mock.ExpectQuery("SELECT created_by FROM eval_suites").WithArgs("suite-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectCommit()

	_, found, err := repo.GetSuiteCreatedBy(context.Background(), "t1", "suite-1")
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, mock.ExpectationsWereMet())
}

// GetCandidateCreatedBy 走 JOIN optimization_jobs，单独覆盖。
func TestPgOptimizationRepository_GetCandidateCreatedBy_join(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgOptimizationRepository{pool: mock}
	expectTenantTx(mock)
	mock.ExpectQuery("SELECT j.created_by").WithArgs("cand-1").
		WillReturnRows(pgxmock.NewRows([]string{"created_by"}).AddRow("creator-1"))
	mock.ExpectCommit()

	createdBy, found, err := repo.GetCandidateCreatedBy(context.Background(), "t1", "cand-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "creator-1", createdBy)
	require.NoError(t, mock.ExpectationsWereMet())
}
