package application

import (
	"context"
	"errors"
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
		IdempotencyKey:   "request-1",
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

func TestOptimizationServiceReplaysSameKeyAndPayloadWithoutCreatingCandidate(t *testing.T) {
	existingJob := domain.OptimizationJob{ID: "existing-job"}
	existingCandidates := []domain.OptimizationCandidate{{ID: "existing-candidate"}}
	repo := &fakeOptimizationRepo{existingJob: existingJob, existingCandidates: existingCandidates}
	creator := &fakeCandidateCreator{}
	svc := NewOptimizationService(creator, fakePromptRewriter{}, repo)
	input := optimizationInput("request-1")

	job, candidates, err := svc.Generate(context.Background(), "tenant-1", input)
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != existingJob.ID || candidates[0].ID != existingCandidates[0].ID || creator.calls != 0 {
		t.Fatalf("replay created side effects: job=%+v candidates=%+v calls=%d", job, candidates, creator.calls)
	}
}

func TestOptimizationServiceRejectsSameKeyWithDifferentPayload(t *testing.T) {
	repo := &fakeOptimizationRepo{existingJob: domain.OptimizationJob{ID: "existing-job"}, existingFingerprint: "different"}
	svc := NewOptimizationService(&fakeCandidateCreator{}, fakePromptRewriter{}, repo)

	if _, _, err := svc.Generate(context.Background(), "tenant-1", optimizationInput("request-1")); !errors.Is(err, domain.ErrOptimizationIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
}

func TestOptimizationServiceRollbackDoesNotLeaveCandidate(t *testing.T) {
	creator := &fakeCandidateCreator{}
	repo := &fakeOptimizationRepo{saveErr: errors.New("save failed"), creator: creator}
	svc := NewOptimizationService(creator, fakePromptRewriter{patches: []domain.CandidatePatch{{
		PromptPatch: map[string]any{"instructions": "updated"},
	}}}, repo)

	if _, _, err := svc.Generate(context.Background(), "tenant-1", optimizationInput("request-1")); err == nil {
		t.Fatal("expected persistence failure")
	}
	if creator.persisted != 0 {
		t.Fatalf("failed transaction left %d candidate revisions", creator.persisted)
	}
}

func TestOptimizationFingerprintIsCanonicalAndTenantScoped(t *testing.T) {
	left := optimizationInput("")
	left.SearchSpace = map[string][]any{"temperature": {0.1}, "max_tokens": {128.0}}
	right := optimizationInput("")
	right.SearchSpace = map[string][]any{"max_tokens": {128.0}, "temperature": {0.1}}
	leftHash, err := OptimizationFingerprint("tenant-1", left)
	if err != nil {
		t.Fatal(err)
	}
	rightHash, err := OptimizationFingerprint("tenant-1", right)
	if err != nil {
		t.Fatal(err)
	}
	otherTenantHash, err := OptimizationFingerprint("tenant-2", right)
	if err != nil {
		t.Fatal(err)
	}
	if leftHash != rightHash || leftHash == otherTenantHash {
		t.Fatalf("fingerprints not canonical/tenant-scoped: %q %q %q", leftHash, rightHash, otherTenantHash)
	}
}

func optimizationInput(key string) GenerateCandidatesInput {
	return GenerateCandidatesInput{
		IdempotencyKey:  key,
		Baseline:        domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "version-1"},
		SuiteRevisionID: "suite-revision-1", SearchSpace: map[string][]any{}, FailureSummaries: []string{"failure"},
	}
}

type fakeCandidateCreator struct {
	calls     int
	persisted int
}

func (f *fakeCandidateCreator) LoadOptimizableSnapshot(
	_ context.Context, _ string, _ domain.ResourceRef,
) (map[string]any, error) {
	return map[string]any{"instructions": "分类输入"}, nil
}

func (f *fakeCandidateCreator) CreateCandidate(
	_ context.Context, _ string, baseline domain.ResourceRef, patch domain.CandidatePatch,
) (domain.ResourceRef, error) {
	f.calls++
	f.persisted++
	return domain.ResourceRef{Kind: baseline.Kind, ResourceID: baseline.ResourceID, RevisionID: "candidate-" + patch.Source + "-" + string(rune(f.calls+'0'))}, nil
}

type fakePromptRewriter struct{ patches []domain.CandidatePatch }

func (f fakePromptRewriter) Rewrite(_ context.Context, _ PromptRewriteRequest) ([]domain.CandidatePatch, error) {
	return f.patches, nil
}

type fakeOptimizationRepo struct {
	savedCandidates     []domain.OptimizationCandidate
	existingJob         domain.OptimizationJob
	existingCandidates  []domain.OptimizationCandidate
	existingFingerprint string
	saveErr             error
	creator             *fakeCandidateCreator
}

func (f *fakeOptimizationRepo) WithinTransaction(ctx context.Context, _ string, fn func(context.Context) error) error {
	before := 0
	if f.creator != nil {
		before = f.creator.persisted
	}
	err := fn(ctx)
	if err != nil && f.creator != nil {
		f.creator.persisted = before
	}
	return err
}

func (f *fakeOptimizationRepo) GetByIdempotencyKey(
	_ context.Context, _ string, _ string,
) (domain.OptimizationJob, []domain.OptimizationCandidate, string, bool, error) {
	if f.existingJob.ID == "" {
		return domain.OptimizationJob{}, nil, "", false, nil
	}
	fingerprint := f.existingFingerprint
	if fingerprint == "" {
		fingerprint, _ = OptimizationFingerprint("tenant-1", optimizationInput("request-1"))
	}
	return f.existingJob, f.existingCandidates, fingerprint, true, nil
}

func (f *fakeOptimizationRepo) SaveJobWithCandidates(
	_ context.Context, _ string, _ domain.OptimizationJob, candidates []domain.OptimizationCandidate,
	_, _ string,
) (bool, error) {
	if f.saveErr != nil {
		return false, f.saveErr
	}
	f.savedCandidates = candidates
	return true, nil
}
