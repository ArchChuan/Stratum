package application

import (
	"context"
	"errors"
	"testing"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
	"go.uber.org/zap"
)

func TestVersionServicePublishDistinguishesMissingDraft(t *testing.T) {
	repo := newFakeVersionRepo()
	repo.skills["published-skill"] = port.SkillProductRow{ID: "published-skill", Status: "published"}
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	if _, err := svc.PublishDraft(context.Background(), "published-skill", "user-1"); !errors.Is(err, domain.ErrSkillDraftNotFound) {
		t.Fatalf("expected missing draft conflict, got %v", err)
	}
	if _, err := svc.PublishDraft(context.Background(), "missing-skill", "user-1"); !errors.Is(err, domain.ErrSkillNotFound) {
		t.Fatalf("expected missing skill error, got %v", err)
	}
}

func TestVersionServiceCreatesDraftBundle(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	view, err := svc.CreateSkillDraft(context.Background(), CreateSkillDraftInput{
		Name:         "投诉分类",
		Description:  "判断客户投诉类型",
		Instructions: "先判断投诉类别，需要订单信息时查询订单。",
		ActorID:      "user-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Draft.Name != "投诉分类" || view.Draft.Description != "判断客户投诉类型" || view.Draft.Instructions == "" {
		t.Fatalf("expected name/description/instructions draft, got %#v", view.Draft)
	}
	if view.Draft.ContentHash == "" {
		t.Fatalf("expected content hash, got %#v", view.Draft)
	}
	if view.Skill.DraftRevisionID != view.Draft.ID {
		t.Fatalf("draft revision link mismatch: %#v", view.Skill)
	}
}

func TestVersionServicePublishSucceedsWithoutActivationConfirmation(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	view := mustCreateDraft(t, svc)
	published, err := svc.PublishDraft(context.Background(), view.Skill.ID, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if published.Status != domain.VersionStatusPublished || published.RevisionNo != 1 {
		t.Fatalf("unexpected published revision: %#v", published)
	}
}

func TestVersionServiceUpdateDraftRefreshesHash(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	view := mustCreateDraft(t, svc)
	updated, err := svc.UpdateDraftBundle(context.Background(), view.Skill.ID, "", UpdateDraftBundleInput{
		Name:         "投诉分类",
		Description:  "判断客户投诉类型",
		Instructions: "使用新的分类方法",
		ActorID:      "user-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Draft.Instructions != "使用新的分类方法" || updated.Draft.ContentHash == view.Draft.ContentHash {
		t.Fatalf("draft update did not refresh revision: %#v", updated)
	}
}

func TestProposalSkillUpdateDraftBundleUsesExpectedHash(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	view := mustCreateDraft(t, svc)
	updated, err := svc.UpdateDraftBundle(context.Background(), view.Skill.ID, view.Draft.ContentHash, UpdateDraftBundleInput{
		Name: "updated_skill", Description: "updated description", Instructions: "updated instructions",
		ActorID: "user-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Skill.Name != "updated_skill" || updated.Draft.ContentHash == view.Draft.ContentHash {
		t.Fatalf("unexpected bundle update: %#v", updated)
	}
	if _, err := svc.UpdateDraftBundle(context.Background(), view.Skill.ID, view.Draft.ContentHash, UpdateDraftBundleInput{
		Name: "stale", Description: "stale", Instructions: "stale",
		ActorID: "user-1",
	}); err != domain.ErrSkillDraftStale {
		t.Fatalf("expected stale hash rejection, got %v", err)
	}
}

func TestVersionServiceCandidateCanOnlyRewriteInstructions(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
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

func TestVersionServiceResolvePublishedRevisionRejectsUnpublished(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	view := mustCreateDraft(t, svc)

	if _, err := svc.ResolvePublishedRevision(context.Background(), view.Skill.ID, view.Draft.ID); err == nil {
		t.Fatal("draft revision must not resolve for evaluation")
	}
	published, err := svc.PublishDraft(context.Background(), view.Skill.ID, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := svc.ResolvePublishedRevision(context.Background(), view.Skill.ID, published.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ID != published.ID || resolved.SkillID != view.Skill.ID {
		t.Fatalf("unexpected published revision: %#v", resolved)
	}
}

func TestVersionServicePublishedRevisionSafeSummaryHasNoSensitiveFields(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	view := mustCreateDraft(t, svc)
	published, err := svc.PublishDraft(context.Background(), view.Skill.ID, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	summary, err := svc.PublishedRevisionSafeSummary(context.Background(), view.Skill.ID, published.ID)
	if err != nil {
		t.Fatal(err)
	}
	if summary["name"] != view.Skill.Name || summary["description"] != view.Skill.Description {
		t.Fatalf("safe resource identity missing: %#v", summary)
	}
	for _, key := range []string{"secret", "token", "api_key", "destination", "instructions"} {
		if _, ok := summary[key]; ok {
			t.Fatalf("safe summary contains %q: %#v", key, summary)
		}
	}
}

func mustCreateDraft(t *testing.T, svc *VersionService) SkillWorkspaceView {
	t.Helper()
	view, err := svc.CreateSkillDraft(context.Background(), CreateSkillDraftInput{
		Name: "complaint", Description: "分类", Instructions: "分类用户投诉",
		ActorID: "user-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return view
}

type fakeVersionRepo struct {
	skills    map[string]port.SkillProductRow
	revisions map[string]domain.SkillRevision
	// audits captures every audit event handed to a write method so ownership
	// tests can assert the change-audit contract. Nil audit params (seed-only
	// writes) are skipped.
	audits []*auditdomain.ResourceChangeAuditEvent
}

func (r *fakeVersionRepo) recordAudit(audit *auditdomain.ResourceChangeAuditEvent) {
	if audit != nil {
		r.audits = append(r.audits, audit)
	}
}

func newFakeVersionRepo() *fakeVersionRepo {
	return &fakeVersionRepo{skills: map[string]port.SkillProductRow{}, revisions: map[string]domain.SkillRevision{}}
}

func (r *fakeVersionRepo) InsertSkillWithDraft(_ context.Context, skill port.SkillProductRow, draft domain.SkillRevision, audit *auditdomain.ResourceChangeAuditEvent, _ []string) error {
	r.recordAudit(audit)
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
func (r *fakeVersionRepo) DeleteSkill(_ context.Context, id string, audit *auditdomain.ResourceChangeAuditEvent) error {
	r.recordAudit(audit)
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
func (r *fakeVersionRepo) InsertCandidate(_ context.Context, candidate domain.SkillRevision, audit *auditdomain.ResourceChangeAuditEvent) error {
	r.recordAudit(audit)
	r.revisions[candidate.ID] = candidate
	return nil
}
func (r *fakeVersionRepo) UpdateDraftBundle(_ context.Context, skillID, expected string, skill port.SkillProductRow, draft domain.SkillRevision, audit *auditdomain.ResourceChangeAuditEvent, _ string) (domain.SkillRevision, error) {
	r.recordAudit(audit)
	for id, current := range r.revisions {
		if current.SkillID != skillID || current.Status != domain.VersionStatusDraft {
			continue
		}
		// 与真实 repo 语义一致:空 expected(直写)跳过乐观并发校验。
		if expected != "" && current.ContentHash != expected {
			return domain.SkillRevision{}, domain.ErrSkillDraftStale
		}
		r.skills[skillID] = skill
		r.revisions[id] = draft
		return draft, nil
	}
	return domain.SkillRevision{}, domain.ErrSkillNotFound
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
func (r *fakeVersionRepo) PublishDraft(_ context.Context, skillID, draftID string, next int, checks map[string]any, audit *auditdomain.ResourceChangeAuditEvent, _ string) (domain.SkillRevision, error) {
	r.recordAudit(audit)
	revision := r.revisions[draftID]
	revision.Status, revision.RevisionNo, revision.PublishChecks = domain.VersionStatusPublished, next, checks
	r.revisions[draftID] = revision
	skill := r.skills[skillID]
	skill.Status, skill.ActiveRevisionID, skill.DraftRevisionID = "published", draftID, ""
	r.skills[skillID] = skill
	return revision, nil
}
