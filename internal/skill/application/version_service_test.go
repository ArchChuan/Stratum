package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
	"go.uber.org/zap"
)

func TestVersionServiceCreatesInstructionBundleDraft(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	requirements := domain.Requirements{MCPToolIDs: []string{"mcp:orders:get_order"}}
	view, err := svc.CreateSkillDraft(context.Background(), CreateSkillDraftInput{
		Name: "投诉分类", Goal: "判断客户投诉类型", WhenToUse: "用户表达投诉时",
		SampleInput: "快递没更新", ExpectedOutput: "物流问题",
		Instructions: "先判断投诉类别，需要订单信息时查询订单。", Requirements: requirements,
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Draft.ActivationContract.Name == "" || view.Draft.Instructions == "" {
		t.Fatalf("expected activation and instructions, got %#v", view.Draft)
	}
	if len(view.Draft.Requirements.MCPToolIDs) != 1 || view.Draft.ContentHash == "" {
		t.Fatalf("expected requirements and hash, got %#v", view.Draft)
	}
	if view.Skill.DraftRevisionID != view.Draft.ID {
		t.Fatalf("draft revision link mismatch: %#v", view.Skill)
	}
}

func TestVersionServicePublishRequiresConfirmedActivation(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	view := mustCreateDraft(t, svc)
	if _, err := svc.PublishDraft(context.Background(), view.Skill.ID); err == nil {
		t.Fatal("expected unconfirmed activation contract to block publish")
	}
	_, err := svc.UpdateActivation(context.Background(), view.Skill.ID, UpdateActivationInput{
		Name: "classify_complaint", Description: "判断投诉类型",
		InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}, Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	published, err := svc.PublishDraft(context.Background(), view.Skill.ID)
	if err != nil {
		t.Fatal(err)
	}
	if published.Status != domain.VersionStatusPublished || published.RevisionNo != 1 {
		t.Fatalf("unexpected published revision: %#v", published)
	}
}

func TestVersionServiceUpdateInstructionBundleRefreshesHash(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	view := mustCreateDraft(t, svc)
	updated, err := svc.UpdateInstructionBundle(context.Background(), view.Skill.ID, UpdateInstructionBundleInput{
		Instructions: "使用新的分类方法", Requirements: domain.Requirements{MemoryScopes: []string{"user"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Instructions != "使用新的分类方法" || updated.ContentHash == view.Draft.ContentHash {
		t.Fatalf("instruction update did not refresh revision: %#v", updated)
	}
}

func TestVersionServiceCandidateCanOnlyRewriteInstructions(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	view := mustCreateDraft(t, svc)
	candidate, err := svc.CreateCandidate(context.Background(), view.Skill.ID, view.Draft.ID, CandidateInput{
		Source: "llm_rewrite", PromptPatch: map[string]any{"instructions": "优化后的方法"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.ParentRevisionID != view.Draft.ID || candidate.Instructions != "优化后的方法" {
		t.Fatalf("unexpected candidate: %#v", candidate)
	}
	if _, err := svc.CreateCandidate(context.Background(), view.Skill.ID, view.Draft.ID, CandidateInput{
		Source: "llm_rewrite", PromptPatch: map[string]any{"temperature": 0.2},
	}); err == nil {
		t.Fatal("runtime parameter optimization must be rejected")
	}
}

func mustCreateDraft(t *testing.T, svc *VersionService) SkillWorkspaceView {
	t.Helper()
	view, err := svc.CreateSkillDraft(context.Background(), CreateSkillDraftInput{
		Name: "complaint", Goal: "分类", WhenToUse: "收到投诉时",
		SampleInput: "物流", ExpectedOutput: "物流", Instructions: "分类用户投诉",
	})
	if err != nil {
		t.Fatal(err)
	}
	return view
}

type fakeVersionRepo struct {
	skills    map[string]port.SkillProductRow
	revisions map[string]domain.SkillRevision
}

func newFakeVersionRepo() *fakeVersionRepo {
	return &fakeVersionRepo{skills: map[string]port.SkillProductRow{}, revisions: map[string]domain.SkillRevision{}}
}

func (r *fakeVersionRepo) InsertSkillWithDraft(_ context.Context, skill port.SkillProductRow, draft domain.SkillRevision) error {
	r.skills[skill.ID], r.revisions[draft.ID] = skill, draft
	return nil
}
func (r *fakeVersionRepo) GetSkill(_ context.Context, id string) (port.SkillProductRow, bool, error) {
	v, ok := r.skills[id]
	return v, ok, nil
}
func (r *fakeVersionRepo) ListSkills(_ context.Context) ([]port.SkillProductRow, error) {
	result := make([]port.SkillProductRow, 0, len(r.skills))
	for _, skill := range r.skills {
		result = append(result, skill)
	}
	return result, nil
}
func (r *fakeVersionRepo) DeleteSkill(_ context.Context, id string) error {
	delete(r.skills, id)
	for revisionID, revision := range r.revisions {
		if revision.SkillID == id {
			delete(r.revisions, revisionID)
		}
	}
	return nil
}
func (r *fakeVersionRepo) GetDraftRevision(_ context.Context, skillID string) (domain.SkillRevision, bool, error) {
	for _, revision := range r.revisions {
		if revision.SkillID == skillID && revision.Status == domain.VersionStatusDraft {
			return revision, true, nil
		}
	}
	return domain.SkillRevision{}, false, nil
}
func (r *fakeVersionRepo) GetActiveRevision(_ context.Context, skillID string) (domain.SkillRevision, bool, error) {
	skill := r.skills[skillID]
	v, ok := r.revisions[skill.ActiveRevisionID]
	return v, ok, nil
}
func (r *fakeVersionRepo) GetRevision(_ context.Context, skillID, revisionID string) (domain.SkillRevision, bool, error) {
	v, ok := r.revisions[revisionID]
	return v, ok && v.SkillID == skillID, nil
}
func (r *fakeVersionRepo) InsertCandidate(_ context.Context, candidate domain.SkillRevision) error {
	r.revisions[candidate.ID] = candidate
	return nil
}
func (r *fakeVersionRepo) updateDraft(skillID string, fn func(*domain.SkillRevision)) (domain.SkillRevision, error) {
	for id, revision := range r.revisions {
		if revision.SkillID == skillID && revision.Status == domain.VersionStatusDraft {
			fn(&revision)
			r.revisions[id] = revision
			return revision, nil
		}
	}
	return domain.SkillRevision{}, domain.ErrSkillNotFound
}
func (r *fakeVersionRepo) UpdateDraftCapability(_ context.Context, skillID string, value domain.Capability, hash string) (domain.SkillRevision, error) {
	return r.updateDraft(skillID, func(v *domain.SkillRevision) { v.Capability, v.ContentHash = value, hash })
}
func (r *fakeVersionRepo) UpdateDraftActivation(_ context.Context, skillID string, value domain.ActivationContract, hash string) (domain.SkillRevision, error) {
	return r.updateDraft(skillID, func(v *domain.SkillRevision) { v.ActivationContract, v.ContentHash = value, hash })
}
func (r *fakeVersionRepo) UpdateDraftInstructions(_ context.Context, skillID, instructions string, requirements domain.Requirements, hash string) (domain.SkillRevision, error) {
	return r.updateDraft(skillID, func(v *domain.SkillRevision) {
		v.Instructions, v.Requirements, v.ContentHash = instructions, requirements, hash
	})
}
func (r *fakeVersionRepo) NextRevisionNo(_ context.Context, skillID string) (int, error) {
	next := 1
	for _, revision := range r.revisions {
		if revision.SkillID == skillID && revision.RevisionNo >= next {
			next = revision.RevisionNo + 1
		}
	}
	return next, nil
}
func (r *fakeVersionRepo) PublishDraft(_ context.Context, skillID, draftID string, next int, checks map[string]any) (domain.SkillRevision, error) {
	revision := r.revisions[draftID]
	revision.Status, revision.RevisionNo, revision.PublishChecks = domain.VersionStatusPublished, next, checks
	r.revisions[draftID] = revision
	skill := r.skills[skillID]
	skill.Status, skill.ActiveRevisionID, skill.DraftRevisionID = "published", draftID, ""
	r.skills[skillID] = skill
	return revision, nil
}
