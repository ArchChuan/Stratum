package application

import (
	"context"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/google/uuid"
)

type PromptRewriteRequest struct {
	TenantID         string
	Baseline         domain.ResourceRef
	BaselineSnapshot map[string]any
	FailureSummaries []string
}

type PromptRewriter interface {
	Rewrite(ctx context.Context, request PromptRewriteRequest) ([]domain.CandidatePatch, error)
}

type GenerateCandidatesInput struct {
	Baseline         domain.ResourceRef
	SuiteRevisionID  string
	SearchSpace      map[string][]any
	FailureSummaries []string
}

type OptimizationService struct {
	creator  port.CandidateCreator
	rewriter PromptRewriter
	repo     port.OptimizationRepository
}

func NewOptimizationService(
	creator port.CandidateCreator,
	rewriter PromptRewriter,
	repo port.OptimizationRepository,
) *OptimizationService {
	return &OptimizationService{creator: creator, rewriter: rewriter, repo: repo}
}

func (s *OptimizationService) Generate(
	ctx context.Context,
	tenantID string,
	input GenerateCandidatesInput,
) (domain.OptimizationJob, []domain.OptimizationCandidate, error) {
	if err := input.Baseline.Validate(); err != nil {
		return domain.OptimizationJob{}, nil, err
	}
	parameterPatches, err := domain.GenerateParameterPatches(input.SearchSpace)
	if err != nil {
		return domain.OptimizationJob{}, nil, err
	}
	patches := make([]domain.CandidatePatch, 0, len(parameterPatches))
	for _, patch := range parameterPatches {
		patches = append(patches, domain.CandidatePatch{Source: "parameter_search", ParameterPatch: patch})
	}
	if s.rewriter != nil && len(input.FailureSummaries) > 0 {
		snapshot, err := s.creator.LoadOptimizableSnapshot(ctx, tenantID, input.Baseline)
		if err != nil {
			return domain.OptimizationJob{}, nil, err
		}
		rewrites, err := s.rewriter.Rewrite(ctx, PromptRewriteRequest{
			TenantID: tenantID, Baseline: input.Baseline, BaselineSnapshot: snapshot,
			FailureSummaries: input.FailureSummaries,
		})
		if err != nil {
			return domain.OptimizationJob{}, nil, err
		}
		for _, rewrite := range rewrites {
			if err := domain.ValidatePromptPatch(rewrite.PromptPatch); err != nil {
				return domain.OptimizationJob{}, nil, err
			}
			rewrite.Source = "llm_rewrite"
			patches = append(patches, rewrite)
		}
	}
	now := time.Now().UTC()
	job := domain.OptimizationJob{
		ID: uuid.Must(uuid.NewV7()).String(), Baseline: input.Baseline,
		SuiteRevisionID: input.SuiteRevisionID, Status: domain.JobSucceeded,
		SearchSpace: input.SearchSpace, FailureSummaries: input.FailureSummaries, CreatedAt: now,
	}
	candidates := make([]domain.OptimizationCandidate, 0, len(patches))
	for _, patch := range patches {
		revision, err := s.creator.CreateCandidate(ctx, tenantID, input.Baseline, patch)
		if err != nil {
			return domain.OptimizationJob{}, nil, err
		}
		candidates = append(candidates, domain.OptimizationCandidate{
			ID: uuid.Must(uuid.NewV7()).String(), OptimizationJobID: job.ID, Revision: revision,
			ParentRevisionID: input.Baseline.RevisionID, Source: patch.Source,
			Rationale: patch.Rationale, CreatedAt: now,
		})
	}
	if err := s.repo.SaveJobWithCandidates(ctx, tenantID, job, candidates); err != nil {
		return domain.OptimizationJob{}, nil, err
	}
	return job, candidates, nil
}
