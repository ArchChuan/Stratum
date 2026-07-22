package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

func TestCandidateCommandServiceRejectValidatesAndPropagatesTenant(t *testing.T) {
	repo := &candidateCommandRepoStub{}
	svc := NewCandidateCommandService(repo)
	got, err := svc.Reject(context.Background(), "tenant-1", "candidate-1", CandidateCommandInput{
		ActorID: "admin-1", Reason: "unsafe", IdempotencyKey: "request-1", ExpectedStateVersion: 1,
	})
	if err != nil || got.Status != "rejected" {
		t.Fatalf("result=%+v err=%v", got, err)
	}
	if repo.tenantID != "tenant-1" || repo.candidateID != "candidate-1" || repo.command.ActorType != domain.ActorTypeAdmin {
		t.Fatalf("repository call=%+v", repo)
	}
}

type candidateCommandRepoStub struct {
	tenantID, candidateID string
	command               domain.CandidateCommand
}

func (r *candidateCommandRepoStub) Reject(_ context.Context, tenantID, candidateID string, command domain.CandidateCommand) (domain.CandidateSummary, error) {
	r.tenantID, r.candidateID, r.command = tenantID, candidateID, command
	return domain.CandidateSummary{ID: candidateID, Status: "rejected"}, nil
}
