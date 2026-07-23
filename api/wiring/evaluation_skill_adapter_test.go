package wiring

import (
	"context"
	"testing"

	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	skilldomain "github.com/byteBuilderX/stratum/internal/skill/domain"
	skillport "github.com/byteBuilderX/stratum/internal/skill/domain/port"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"go.uber.org/zap"
)

func TestEvaluationSkillAdapterResolvesPublishedRevisionByTenantResourceAndRevision(t *testing.T) {
	repo := &evaluationSkillVersionRepo{revision: evaluationPublishedSkillRevision()}
	manager := skillCandidateManager{versions: skillapp.NewVersionService(repo, zap.NewNop())}
	ref := evaldomain.ResourceRef{Kind: evaldomain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "revision-1"}

	resolved, err := manager.ResolveRevision(context.Background(), "tenant-1", ref)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ID != ref.RevisionID || resolved.ResourceID != ref.ResourceID || repo.tenantID != "tenant-1" {
		t.Fatalf("unexpected resolution: revision=%#v tenant=%q", resolved, repo.tenantID)
	}
}

func TestEvaluationSkillAdapterCreatesBaselineFromActivePublishedRevision(t *testing.T) {
	repo := &evaluationSkillVersionRepo{revision: evaluationPublishedSkillRevision()}
	manager := skillCandidateManager{versions: skillapp.NewVersionService(repo, zap.NewNop())}

	ref, err := manager.CreatePublishedBaseline(context.Background(), "tenant-1", "skill-1")
	if err != nil {
		t.Fatal(err)
	}
	if ref != (evaldomain.ResourceRef{Kind: evaldomain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "revision-1"}) {
		t.Fatalf("unexpected baseline: %#v", ref)
	}
	if repo.tenantID != "tenant-1" {
		t.Fatalf("tenant context = %q, want tenant-1", repo.tenantID)
	}
}

func TestEvaluationBaselineServiceIncludesSkillWithoutSharedRevisionProviders(t *testing.T) {
	repo := &evaluationSkillVersionRepo{revision: evaluationPublishedSkillRevision()}
	manager := skillCandidateManager{versions: skillapp.NewVersionService(repo, zap.NewNop())}
	service := newEvaluationBaselineService(manager, nil, nil, nil)

	ref, err := service.CreatePublishedBaseline(
		context.Background(), "tenant-1", evaldomain.ResourceKindSkill, "skill-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if ref.RevisionID != "revision-1" {
		t.Fatalf("unexpected skill baseline: %#v", ref)
	}
}

func TestEvaluationSkillAdapterRejectsInvalidPublishedRevision(t *testing.T) {
	tests := []struct {
		name     string
		tenantID string
		kind     evaldomain.ResourceKind
		status   skilldomain.VersionStatus
	}{
		{name: "missing tenant", kind: evaldomain.ResourceKindSkill, status: skilldomain.VersionStatusPublished},
		{name: "wrong kind", tenantID: "tenant-1", kind: evaldomain.ResourceKindAgent, status: skilldomain.VersionStatusPublished},
		{name: "draft revision", tenantID: "tenant-1", kind: evaldomain.ResourceKindSkill, status: skilldomain.VersionStatusDraft},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			revision := evaluationPublishedSkillRevision()
			revision.Status = tt.status
			manager := skillCandidateManager{versions: skillapp.NewVersionService(&evaluationSkillVersionRepo{revision: revision}, zap.NewNop())}
			ref := evaldomain.ResourceRef{Kind: tt.kind, ResourceID: "skill-1", RevisionID: "revision-1"}
			if _, err := manager.ResolveRevision(context.Background(), tt.tenantID, ref); err == nil {
				t.Fatal("expected resolution failure")
			}
		})
	}
}

func TestEvaluationSkillCandidatePatchIsBoundedAndSummaryIsSafe(t *testing.T) {
	tests := []struct {
		name  string
		patch map[string]any
		ok    bool
	}{
		{name: "instructions", patch: map[string]any{"instructions": "clearer instructions"}, ok: true},
		{name: "requirements", patch: map[string]any{"requirements": map[string]any{}}, ok: false},
		{name: "permissions", patch: map[string]any{"permissions": []string{"admin"}}, ok: false},
		{name: "secret", patch: map[string]any{"secret": "synthetic"}, ok: false},
		{name: "destination", patch: map[string]any{"destination": "https://example.invalid"}, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &evaluationSkillVersionRepo{revision: evaluationPublishedSkillRevision()}
			manager := skillCandidateManager{versions: skillapp.NewVersionService(repo, zap.NewNop())}
			ref := evaldomain.ResourceRef{Kind: evaldomain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "revision-1"}
			_, err := manager.CreateCandidate(context.Background(), "tenant-1", ref, evaldomain.CandidatePatch{Source: "llm_rewrite", PromptPatch: tt.patch})
			if (err == nil) != tt.ok {
				t.Fatalf("CreateCandidate() error = %v, want success %v", err, tt.ok)
			}
		})
	}

	manager := skillCandidateManager{versions: skillapp.NewVersionService(&evaluationSkillVersionRepo{revision: evaluationPublishedSkillRevision()}, zap.NewNop())}
	summary, err := manager.SafeSummary(context.Background(), "tenant-1", evaldomain.ResourceRef{Kind: evaldomain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "revision-1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"secret", "token", "api_key", "requirements", "destination", "instructions"} {
		if _, ok := summary[key]; ok {
			t.Fatalf("safe summary contains %q: %#v", key, summary)
		}
	}
}

type evaluationSkillVersionRepo struct {
	revision skilldomain.SkillRevision
	tenantID string
}

var _ skillport.VersionRepo = (*evaluationSkillVersionRepo)(nil)

func (r *evaluationSkillVersionRepo) GetRevision(ctx context.Context, skillID, revisionID string) (skilldomain.SkillRevision, bool, error) {
	tenant, ok := postgres.FromContext(ctx)
	if ok {
		r.tenantID = tenant.TenantID
	}
	return r.revision, r.revision.SkillID == skillID && r.revision.ID == revisionID, nil
}
func (r *evaluationSkillVersionRepo) InsertCandidate(_ context.Context, candidate skilldomain.SkillRevision) error {
	r.revision = candidate
	return nil
}
func (r *evaluationSkillVersionRepo) InsertSkillWithDraft(context.Context, skillport.SkillProductRow, skilldomain.SkillRevision) error {
	return nil
}
func (r *evaluationSkillVersionRepo) GetSkill(context.Context, string) (skillport.SkillProductRow, bool, error) {
	return skillport.SkillProductRow{}, false, nil
}
func (r *evaluationSkillVersionRepo) ListSkills(context.Context) ([]skillport.SkillProductRow, error) {
	return nil, nil
}
func (r *evaluationSkillVersionRepo) DeleteSkill(context.Context, string) error { return nil }
func (r *evaluationSkillVersionRepo) GetDraftRevision(context.Context, string) (skilldomain.SkillRevision, bool, error) {
	return skilldomain.SkillRevision{}, false, nil
}
func (r *evaluationSkillVersionRepo) GetActiveRevision(ctx context.Context, skillID string) (skilldomain.SkillRevision, bool, error) {
	tenant, ok := postgres.FromContext(ctx)
	if ok {
		r.tenantID = tenant.TenantID
	}
	return r.revision, r.revision.SkillID == skillID && r.revision.Status == skilldomain.VersionStatusPublished, nil
}
func (r *evaluationSkillVersionRepo) UpdateDraftCapability(context.Context, string, skilldomain.Capability, string) (skilldomain.SkillRevision, error) {
	return skilldomain.SkillRevision{}, nil
}
func (r *evaluationSkillVersionRepo) UpdateDraftActivation(context.Context, string, skilldomain.ActivationContract, string) (skilldomain.SkillRevision, error) {
	return skilldomain.SkillRevision{}, nil
}
func (r *evaluationSkillVersionRepo) UpdateDraftInstructions(context.Context, string, string, skilldomain.Requirements, string) (skilldomain.SkillRevision, error) {
	return skilldomain.SkillRevision{}, nil
}
func (r *evaluationSkillVersionRepo) PublishDraft(context.Context, string, string, int, map[string]any) (skilldomain.SkillRevision, error) {
	return skilldomain.SkillRevision{}, nil
}
func (r *evaluationSkillVersionRepo) NextRevisionNo(context.Context, string) (int, error) {
	return 1, nil
}

func evaluationPublishedSkillRevision() skilldomain.SkillRevision {
	return skilldomain.SkillRevision{ID: "revision-1", SkillID: "skill-1", Status: skilldomain.VersionStatusPublished, Source: "manual", ContentHash: "hash", Instructions: "baseline"}
}
