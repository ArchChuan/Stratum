package application

import (
	"context"
	"errors"
	"strings"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/google/uuid"
)

var (
	ErrSuiteNameRequired  = errors.New("evaluation suite name required")
	ErrSuiteCasesRequired = errors.New("evaluation suite requires at least one enabled case")
	ErrSuiteNotFound      = errors.New("evaluation suite not found")
)

type CreateSuiteInput struct {
	Name         string
	Description  string
	ResourceKind domain.ResourceKind
	Cases        []domain.EvalCase
}

type SuiteService struct {
	repo port.SuiteRepository
}

func NewSuiteService(repo port.SuiteRepository) *SuiteService {
	return &SuiteService{repo: repo}
}

func (s *SuiteService) Create(ctx context.Context, tenantID string, input CreateSuiteInput) (domain.EvalSuite, domain.EvalSuiteRevision, error) {
	if strings.TrimSpace(input.Name) == "" {
		return domain.EvalSuite{}, domain.EvalSuiteRevision{}, ErrSuiteNameRequired
	}
	hasEnabled := false
	for i := range input.Cases {
		if input.Cases[i].ID == "" {
			input.Cases[i].ID = uuid.Must(uuid.NewV7()).String()
		}
		hasEnabled = hasEnabled || input.Cases[i].Enabled
	}
	if !hasEnabled {
		return domain.EvalSuite{}, domain.EvalSuiteRevision{}, ErrSuiteCasesRequired
	}
	suiteID := uuid.Must(uuid.NewV7()).String()
	revisionID := uuid.Must(uuid.NewV7()).String()
	suite := domain.EvalSuite{
		ID: suiteID, Name: input.Name, Description: input.Description, DraftRevisionID: revisionID,
	}
	revision := domain.EvalSuiteRevision{
		ID: revisionID, SuiteID: suiteID, Status: domain.SuiteRevisionDraft,
		ResourceKind: input.ResourceKind, Cases: input.Cases,
	}
	if err := s.repo.CreateSuite(ctx, tenantID, suite, revision); err != nil {
		return domain.EvalSuite{}, domain.EvalSuiteRevision{}, err
	}
	return suite, revision, nil
}

func (s *SuiteService) Publish(ctx context.Context, tenantID, suiteID string) (domain.EvalSuiteRevision, error) {
	revision, ok, err := s.repo.GetDraftRevision(ctx, tenantID, suiteID)
	if err != nil {
		return domain.EvalSuiteRevision{}, err
	}
	if !ok {
		return domain.EvalSuiteRevision{}, ErrSuiteNotFound
	}
	next, err := s.repo.NextVersionNo(ctx, tenantID, suiteID)
	if err != nil {
		return domain.EvalSuiteRevision{}, err
	}
	return s.repo.PublishRevision(ctx, tenantID, suiteID, revision.ID, next)
}

func (s *SuiteService) GetRevision(ctx context.Context, tenantID, revisionID string) (domain.EvalSuiteRevision, error) {
	revision, ok, err := s.repo.GetRevision(ctx, tenantID, revisionID)
	if err != nil {
		return domain.EvalSuiteRevision{}, err
	}
	if !ok {
		return domain.EvalSuiteRevision{}, ErrSuiteNotFound
	}
	return revision, nil
}
