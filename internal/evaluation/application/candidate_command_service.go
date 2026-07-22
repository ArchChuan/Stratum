package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
)

type CandidateCommandInput struct {
	ActorID              string
	Reason               string
	IdempotencyKey       string
	ExpectedStateVersion int64
}

type CandidateCommandService struct {
	repo port.CandidateCommandRepository
}

func NewCandidateCommandService(repo port.CandidateCommandRepository) *CandidateCommandService {
	return &CandidateCommandService{repo: repo}
}

func (s *CandidateCommandService) Reject(
	ctx context.Context, tenantID, candidateID string, input CandidateCommandInput,
) (domain.CandidateSummary, error) {
	command := domain.CandidateCommand{
		ActorID: input.ActorID, ActorType: domain.ActorTypeAdmin, Reason: input.Reason,
		IdempotencyKey: input.IdempotencyKey, ExpectedStateVersion: input.ExpectedStateVersion,
	}
	if err := command.Validate(); err != nil {
		return domain.CandidateSummary{}, err
	}
	return s.repo.Reject(ctx, tenantID, candidateID, command)
}
