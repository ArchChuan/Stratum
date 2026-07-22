package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	IdempotencyKey   string
	Baseline         domain.ResourceRef
	SuiteRevisionID  string
	SearchSpace      map[string][]any
	FailureSummaries []string
}

var errOptimizationReplay = errors.New("optimization replay")

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
	fingerprint, err := OptimizationFingerprint(tenantID, input)
	if err != nil {
		return domain.OptimizationJob{}, nil, err
	}
	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)
	if idempotencyKey == "" {
		idempotencyKey = "legacy-optimization-" + fingerprint
	}
	if job, candidates, storedFingerprint, found, err := s.repo.GetByIdempotencyKey(ctx, tenantID, idempotencyKey); err != nil {
		return domain.OptimizationJob{}, nil, err
	} else if found {
		if storedFingerprint != fingerprint {
			return domain.OptimizationJob{}, nil, domain.ErrOptimizationIdempotencyConflict
		}
		return job, candidates, nil
	}
	patches := make([]domain.CandidatePatch, 0, 3)
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
	if len(patches) == 0 {
		return domain.OptimizationJob{}, nil, errors.New("instruction optimization requires failure summaries and a prompt rewriter")
	}
	now := time.Now().UTC()
	job := domain.OptimizationJob{
		ID: uuid.Must(uuid.NewV7()).String(), Baseline: input.Baseline,
		SuiteRevisionID: input.SuiteRevisionID, Status: domain.JobSucceeded,
		SearchSpace: input.SearchSpace, FailureSummaries: input.FailureSummaries, CreatedAt: now,
	}
	candidates := make([]domain.OptimizationCandidate, 0, len(patches))
	err = s.repo.WithinTransaction(ctx, tenantID, func(txCtx context.Context) error {
		for _, patch := range patches {
			revision, err := s.creator.CreateCandidate(txCtx, tenantID, input.Baseline, patch)
			if err != nil {
				return err
			}
			candidates = append(candidates, domain.OptimizationCandidate{
				ID: uuid.Must(uuid.NewV7()).String(), OptimizationJobID: job.ID, Revision: revision,
				ParentRevisionID: input.Baseline.RevisionID, Source: patch.Source,
				Rationale: patch.Rationale, CreatedAt: now,
			})
		}
		created, err := s.repo.SaveJobWithCandidates(
			txCtx, tenantID, job, candidates, idempotencyKey, fingerprint,
		)
		if err != nil {
			return err
		}
		if !created {
			return errOptimizationReplay
		}
		return nil
	})
	if errors.Is(err, errOptimizationReplay) {
		storedJob, storedCandidates, storedFingerprint, found, loadErr :=
			s.repo.GetByIdempotencyKey(ctx, tenantID, idempotencyKey)
		if loadErr != nil {
			return domain.OptimizationJob{}, nil, loadErr
		}
		if !found || storedFingerprint != fingerprint {
			return domain.OptimizationJob{}, nil, domain.ErrOptimizationIdempotencyConflict
		}
		return storedJob, storedCandidates, nil
	}
	if err != nil {
		return domain.OptimizationJob{}, nil, err
	}
	return job, candidates, nil
}

func OptimizationFingerprint(tenantID string, input GenerateCandidatesInput) (string, error) {
	payload := struct {
		Version          string             `json:"version"`
		TenantID         string             `json:"tenant_id"`
		Baseline         domain.ResourceRef `json:"baseline"`
		SuiteRevisionID  string             `json:"suite_revision_id"`
		SearchSpace      map[string][]any   `json:"search_space"`
		FailureSummaries []string           `json:"failure_summaries"`
	}{
		Version: "optimization-request:v1", TenantID: tenantID, Baseline: input.Baseline,
		SuiteRevisionID: input.SuiteRevisionID, SearchSpace: input.SearchSpace,
		FailureSummaries: input.FailureSummaries,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("fingerprint optimization request: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
