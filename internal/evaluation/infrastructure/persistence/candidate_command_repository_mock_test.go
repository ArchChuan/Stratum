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

var candidateCommandColumns = []string{
	"id", "resource_kind", "resource_id", "revision_id", "parent_revision_id",
	"source", "status", "rank", "created_at", "rejection_key",
	"rejection_fingerprint", "state_version", "parent_summary", "parent_exists", "candidate_summary",
}

func candidateRow(status string, stateVersion int64, key, fingerprint string) []any {
	rank := 3
	return []any{
		"cand-1", domain.ResourceKind("prompt"), "r-1", "rev-2", "rev-1",
		"optimization", status, &rank, time.Now(), key,
		fingerprint, stateVersion, []byte(`{"name":"v1"}`), true, []byte(`{"name":"v2"}`),
	}
}

func rejectCommand() domain.CandidateCommand {
	return domain.CandidateCommand{
		ActorID:              "admin-1",
		ActorType:            domain.ActorTypeAdmin,
		Reason:               "bad result",
		IdempotencyKey:       "key-1",
		ExpectedStateVersion: 3,
	}
}

func TestPgCandidateCommandRepository_Reject_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgCandidateCommandRepository{pool: mock}
	command := rejectCommand()

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT c.id,j.resource_kind,j.resource_id,c.revision_id").
		WithArgs("cand-1").
		WillReturnRows(pgxmock.NewRows(candidateCommandColumns).AddRow(candidateRow("proposed", 3, "", "")...))
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs(domain.ResourceKind("prompt"), "r-1", "rev-2").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec("UPDATE optimization_candidates SET status='rejected'").
		WithArgs("cand-1", "bad result", "admin-1", "key-1", command.Fingerprint()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	result, err := repo.Reject(context.Background(), "t1", "cand-1", command)
	require.NoError(t, err)
	require.Equal(t, "rejected", result.Status)
	require.Equal(t, int64(4), result.StateVersion)
	require.Equal(t, "prompt", string(result.ResourceKind))
	require.NotEmpty(t, result.SafeDiff.ChangedFields)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgCandidateCommandRepository_Reject_idempotentReject(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgCandidateCommandRepository{pool: mock}
	command := rejectCommand()

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT c.id,j.resource_kind,j.resource_id,c.revision_id").
		WithArgs("cand-1").
		WillReturnRows(pgxmock.NewRows(candidateCommandColumns).AddRow(candidateRow("rejected", 3, "key-1", command.Fingerprint())...))
	mock.ExpectCommit()

	result, err := repo.Reject(context.Background(), "t1", "cand-1", command)
	require.NoError(t, err)
	require.Equal(t, "rejected", result.Status)
	require.Equal(t, int64(3), result.StateVersion)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgCandidateCommandRepository_Reject_rejectConflict(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgCandidateCommandRepository{pool: mock}
	command := rejectCommand()

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT c.id,j.resource_kind,j.resource_id,c.revision_id").
		WithArgs("cand-1").
		WillReturnRows(pgxmock.NewRows(candidateCommandColumns).AddRow(candidateRow("rejected", 3, "other-key", "other-fp")...))
	mock.ExpectRollback()

	_, err := repo.Reject(context.Background(), "t1", "cand-1", command)
	require.ErrorIs(t, err, domain.ErrCandidateCommandConflict)
}

func TestPgCandidateCommandRepository_Reject_notAllowedStatus(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgCandidateCommandRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT c.id,j.resource_kind,j.resource_id,c.revision_id").
		WithArgs("cand-1").
		WillReturnRows(pgxmock.NewRows(candidateCommandColumns).AddRow(candidateRow("applied", 3, "", "")...))
	mock.ExpectRollback()

	_, err := repo.Reject(context.Background(), "t1", "cand-1", rejectCommand())
	require.ErrorIs(t, err, domain.ErrCandidateCommandNotAllowed)
}

func TestPgCandidateCommandRepository_Reject_stateConflict(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgCandidateCommandRepository{pool: mock}
	command := rejectCommand()
	command.ExpectedStateVersion = 99

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT c.id,j.resource_kind,j.resource_id,c.revision_id").
		WithArgs("cand-1").
		WillReturnRows(pgxmock.NewRows(candidateCommandColumns).AddRow(candidateRow("proposed", 3, "", "")...))
	mock.ExpectRollback()

	_, err := repo.Reject(context.Background(), "t1", "cand-1", command)
	require.ErrorIs(t, err, domain.ErrCandidateStateConflict)
}

func TestPgCandidateCommandRepository_Reject_activeExperimentBlocks(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgCandidateCommandRepository{pool: mock}
	command := rejectCommand()

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT c.id,j.resource_kind,j.resource_id,c.revision_id").
		WithArgs("cand-1").
		WillReturnRows(pgxmock.NewRows(candidateCommandColumns).AddRow(candidateRow("proposed", 3, "", "")...))
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs(domain.ResourceKind("prompt"), "r-1", "rev-2").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()

	_, err := repo.Reject(context.Background(), "t1", "cand-1", command)
	require.ErrorIs(t, err, domain.ErrCandidateCommandNotAllowed)
}

func TestPgCandidateCommandRepository_Reject_notFound(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgCandidateCommandRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT c.id,j.resource_kind,j.resource_id,c.revision_id").
		WithArgs("missing").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	_, err := repo.Reject(context.Background(), "t1", "missing", rejectCommand())
	require.ErrorIs(t, err, domain.ErrCandidateNotFound)
}

func TestPgCandidateCommandRepository_Reject_updateFails(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgCandidateCommandRepository{pool: mock}
	command := rejectCommand()

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT c.id,j.resource_kind,j.resource_id,c.revision_id").
		WithArgs("cand-1").
		WillReturnRows(pgxmock.NewRows(candidateCommandColumns).AddRow(candidateRow("proposed", 3, "", "")...))
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs(domain.ResourceKind("prompt"), "r-1", "rev-2").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec("UPDATE optimization_candidates SET status='rejected'").
		WillReturnError(assertionErr())
	mock.ExpectRollback()

	_, err := repo.Reject(context.Background(), "t1", "cand-1", command)
	require.Error(t, err)
}

func assertionErr() error { return context.Canceled }
