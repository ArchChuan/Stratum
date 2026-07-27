package persistence

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

func TestResourceChangeProposalCreateIsAtomic(t *testing.T) {
	pool, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectExec("INSERT INTO resource_change_proposals").
		WithArgs(anyArgs(18)...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectExec("INSERT INTO resource_change_proposal_events").
		WithArgs(anyArgs(7)...).
		WillReturnError(assertionError("event failed"))
	pool.ExpectRollback()

	repo := &PgResourceChangeProposalRepo{pool: pool}
	proposal := testResourceProposal("proposal-1")
	err = repo.Create(tenantCtx("t1"), proposal, domain.ProposalEvent{ID: "event-1", ToStatus: domain.StatusDraft})
	require.ErrorContains(t, err, "event failed")
	require.NoError(t, pool.ExpectationsWereMet())
}

func TestResourceChangeProposalCancelRecordsSourceStatus(t *testing.T) {
	pool, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer pool.Close()

	now := time.Now().UTC()
	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("WITH candidate AS").
		WithArgs("proposal-1", domain.StatusCancelled, now).
		WillReturnRows(pgxmock.NewRows([]string{"status"}).AddRow(domain.StatusReadyForReview))
	pool.ExpectExec("INSERT INTO resource_change_proposal_events").
		WithArgs("", "proposal-1", "admin", domain.StatusReadyForReview, domain.StatusCancelled, "{}", now).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectCommit()

	repo := &PgResourceChangeProposalRepo{pool: pool}
	require.NoError(t, repo.Cancel(tenantCtx("t1"), "proposal-1", "admin", now))
	require.NoError(t, pool.ExpectationsWereMet())
}

func TestResourceChangeProposalConfirmPersistsExpiration(t *testing.T) {
	pool, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer pool.Close()

	now := time.Now().UTC()
	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectExec("UPDATE resource_change_proposals").
		WithArgs("proposal-1", "admin", now).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	pool.ExpectQuery("SELECT status, expires_at FROM resource_change_proposals").
		WithArgs("proposal-1").
		WillReturnRows(pgxmock.NewRows([]string{"status", "expires_at"}).AddRow(domain.StatusReadyForReview, now.Add(-time.Minute)))
	pool.ExpectExec("UPDATE resource_change_proposals").
		WithArgs("proposal-1", now, domain.StatusReadyForReview).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	pool.ExpectExec("INSERT INTO resource_change_proposal_events").
		WithArgs("", "proposal-1", "", domain.StatusReadyForReview, domain.StatusExpired, "{}", now).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectCommit()

	repo := &PgResourceChangeProposalRepo{pool: pool}
	err = repo.Confirm(tenantCtx("t1"), "proposal-1", "admin", now)
	require.ErrorIs(t, err, domain.ErrProposalExpired)
	require.NoError(t, pool.ExpectationsWereMet())
}

func TestResourceChangeProposalUpdateDraftPersistsExpiration(t *testing.T) {
	pool, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer pool.Close()

	now := time.Now().UTC()
	proposal := testResourceProposal("proposal-1")
	proposal.UpdatedAt = now
	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectExec("UPDATE resource_change_proposals").
		WithArgs("proposal-1", "", "", pgxmock.AnyArg(), pgxmock.AnyArg(), domain.StatusReadyForReview, now).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	pool.ExpectQuery("SELECT status, expires_at FROM resource_change_proposals").
		WithArgs("proposal-1").
		WillReturnRows(pgxmock.NewRows([]string{"status", "expires_at"}).AddRow(domain.StatusReadyForReview, now.Add(-time.Minute)))
	pool.ExpectExec("UPDATE resource_change_proposals").
		WithArgs("proposal-1", now, domain.StatusReadyForReview).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	pool.ExpectExec("INSERT INTO resource_change_proposal_events").
		WithArgs("", "proposal-1", "", domain.StatusReadyForReview, domain.StatusExpired, "{}", now).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectCommit()

	repo := &PgResourceChangeProposalRepo{pool: pool}
	err = repo.UpdateDraft(tenantCtx("t1"), proposal, domain.ProposalEvent{})
	require.ErrorIs(t, err, domain.ErrProposalExpired)
	require.NoError(t, pool.ExpectationsWereMet())
}

func testResourceProposal(id string) domain.ResourceChangeProposal {
	return domain.ResourceChangeProposal{
		ID: id, TenantID: "t1", ProposerID: "u1", ResourceKind: domain.ResourceAgent,
		Operation: domain.OperationCreate, Payload: json.RawMessage(`{"name":"agent"}`),
		Summary: "create agent", Status: domain.StatusReadyForReview,
		ExpiresAt: time.Now().UTC().Add(time.Hour), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
}

type assertionError string

func (e assertionError) Error() string { return string(e) }

func anyArgs(count int) []any {
	args := make([]any, count)
	for i := range args {
		args[i] = pgxmock.AnyArg()
	}
	return args
}
