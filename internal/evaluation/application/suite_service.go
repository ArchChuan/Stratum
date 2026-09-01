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
	ActorID      string
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
		CreatedBy: input.ActorID,
	}
	revision := domain.EvalSuiteRevision{
		ID: revisionID, SuiteID: suiteID, Status: domain.SuiteRevisionDraft,
		ResourceKind: input.ResourceKind, Cases: input.Cases, CreatedBy: input.ActorID,
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

// GetDraft returns the suite's current draft revision (the review queue for
// generated cases) or ErrSuiteNotFound when none exists.
func (s *SuiteService) GetDraft(ctx context.Context, tenantID, suiteID string) (domain.EvalSuiteRevision, error) {
	revision, ok, err := s.repo.GetDraftRevision(ctx, tenantID, suiteID)
	if err != nil {
		return domain.EvalSuiteRevision{}, err
	}
	if !ok {
		return domain.EvalSuiteRevision{}, ErrSuiteNotFound
	}
	return revision, nil
}

// UpdateDraftCase applies an approve (Enabled=true), reject (Enabled=false)
// or edit (full field replacement) to one draft case, then returns the
// updated case read back from the persisted draft.
func (s *SuiteService) UpdateDraftCase(ctx context.Context, tenantID, suiteID, caseID string, testCase domain.EvalCase) (domain.EvalCase, error) {
	revision, ok, err := s.repo.GetDraftRevision(ctx, tenantID, suiteID)
	if err != nil {
		return domain.EvalCase{}, err
	}
	if !ok {
		return domain.EvalCase{}, ErrSuiteNotFound
	}
	if err := s.repo.UpdateDraftCase(ctx, tenantID, revision.ID, testCase); err != nil {
		return domain.EvalCase{}, err
	}
	updated, ok, err := s.repo.GetDraftRevision(ctx, tenantID, suiteID)
	if err != nil {
		return domain.EvalCase{}, err
	}
	if !ok {
		return domain.EvalCase{}, ErrSuiteNotFound
	}
	for i := range updated.Cases {
		if updated.Cases[i].ID == caseID {
			return updated.Cases[i], nil
		}
	}
	return domain.EvalCase{}, ErrSuiteNotFound
}

// GetActiveRevision 返回套件当前已发布 revision；套件不存在或从未发布
// 时返回 ErrSuiteNotFound。矩阵评测 seed 用：已有发布基准集直接复用。
func (s *SuiteService) GetActiveRevision(ctx context.Context, tenantID, suiteID string) (domain.EvalSuiteRevision, error) {
	revision, ok, err := s.repo.GetActiveRevision(ctx, tenantID, suiteID)
	if err != nil {
		return domain.EvalSuiteRevision{}, err
	}
	if !ok {
		return domain.EvalSuiteRevision{}, ErrSuiteNotFound
	}
	return revision, nil
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
