package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

func TestOptimizationServiceCreatesOnlyInstructionCandidates(t *testing.T) {
	creator := &fakeCandidateCreator{}
	rewriter := fakePromptRewriter{patches: []domain.CandidatePatch{{
		Source: "llm_rewrite", PromptPatch: map[string]any{"instructions": "更准确地分类输入"}, Rationale: "修复漏分类",
	}}}
	repo := &fakeOptimizationRepo{}
	svc := NewOptimizationService(creator, rewriter, repo)

	job, candidates, err := svc.Generate(context.Background(), "tenant-1", GenerateCandidatesInput{
		Baseline:         domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "version-1"},
		SuiteRevisionID:  "suite-revision-1",
		SearchSpace:      map[string][]any{},
		FailureSummaries: []string{"物流问题被分成其他"},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if job.ID == "" || len(candidates) != 1 {
		t.Fatalf("unexpected generation result: job=%+v candidates=%+v", job, candidates)
	}
	if candidates[0].Source != "llm_rewrite" {
		t.Fatalf("unexpected candidate sources: %+v", candidates)
	}
	if len(repo.savedCandidates) != 1 || creator.calls != 1 {
		t.Fatalf("candidates not persisted/created: saved=%d calls=%d", len(repo.savedCandidates), creator.calls)
	}
}

type fakeCandidateCreator struct{ calls int }

func (f *fakeCandidateCreator) LoadOptimizableSnapshot(
	_ context.Context, _ string, _ domain.ResourceRef,
) (map[string]any, error) {
	return map[string]any{"instructions": "分类输入"}, nil
}

func (f *fakeCandidateCreator) CreateCandidate(
	_ context.Context, _ string, baseline domain.ResourceRef, patch domain.CandidatePatch,
) (domain.ResourceRef, error) {
	f.calls++
	return domain.ResourceRef{Kind: baseline.Kind, ResourceID: baseline.ResourceID, RevisionID: "candidate-" + patch.Source + "-" + string(rune(f.calls+'0'))}, nil
}

type fakePromptRewriter struct{ patches []domain.CandidatePatch }

func (f fakePromptRewriter) Rewrite(_ context.Context, _ PromptRewriteRequest) ([]domain.CandidatePatch, error) {
	return f.patches, nil
}

type fakeOptimizationRepo struct {
	savedCandidates []domain.OptimizationCandidate
}

func (f *fakeOptimizationRepo) SaveJobWithCandidates(
	_ context.Context, _ string, _ domain.OptimizationJob, candidates []domain.OptimizationCandidate,
) error {
	f.savedCandidates = candidates
	return nil
}
