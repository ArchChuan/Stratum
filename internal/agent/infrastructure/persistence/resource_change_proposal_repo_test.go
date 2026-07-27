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
