package application

import (
	"context"
	"sort"
	"testing"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestVersionServiceCreateSkill(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	view, err := svc.CreateSkill(context.Background(), CreateSkillInput{
		Name:         "投诉分类",
		Description:  "判断客户投诉类型",
		Instructions: "先判断投诉类别",
		ActorID:      "user-1",
		Editors:      []string{"editor-1"},
	})
	require.NoError(t, err)
	require.Equal(t, domain.VersionStatusPublished, view.Active.Status)
	require.Equal(t, 1, view.Active.RevisionNo)
	require.NotEmpty(t, view.Active.ContentHash)
	require.Equal(t, view.Skill.ActiveRevisionID, view.Active.ID)
	require.NotNil(t, view.Editors)
	require.Empty(t, view.Editors) // CreateSkill 视图不返回编辑器集
}

func TestVersionServiceSaveRevisionDerivesNewVersion(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	view := mustCreateSkill(t, svc)

	updated, err := svc.SaveRevision(context.Background(), view.Skill.ID, "", SaveRevisionInput{
		Name: "投诉分类", Description: "判断客户投诉类型", Instructions: "使用新的分类方法", ActorID: "user-1",
	})
	require.NoError(t, err)
	require.Equal(t, "使用新的分类方法", updated.Active.Instructions)
	require.Equal(t, 2, updated.Active.RevisionNo)
	require.Equal(t, view.Active.ID, updated.Active.ParentRevisionID)
	require.NotEqual(t, view.Active.ContentHash, updated.Active.ContentHash)
	require.Equal(t, updated.Active.ID, updated.Skill.ActiveRevisionID)
}

func TestProposalSkillSaveRevisionUsesExpectedHash(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	view := mustCreateSkill(t, svc)

	updated, err := svc.SaveRevision(context.Background(), view.Skill.ID, view.Active.ContentHash, SaveRevisionInput{
		Name: "updated_skill", Description: "updated description", Instructions: "updated instructions", ActorID: "user-1",
	})
	require.NoError(t, err)
	require.Equal(t, "updated_skill", updated.Skill.Name)

	// 使用陈旧的 expected hash → 409(ErrSkillDraftStale)。
	_, err = svc.SaveRevision(context.Background(), view.Skill.ID, view.Active.ContentHash, SaveRevisionInput{
		Name: "stale", Description: "stale", Instructions: "stale", ActorID: "user-1",
	})
	require.ErrorIs(t, err, domain.ErrSkillDraftStale)
}

func TestVersionServiceCandidateCanOnlyRewriteInstructions(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	view := mustCreateSkill(t, svc)

	candidate, err := svc.CreateCandidate(context.Background(), view.Skill.ID, view.Active.ID, CandidateInput{
		Source: "llm_rewrite", PromptPatch: map[string]any{"instructions": "优化后的方法"},
	})
	require.NoError(t, err)
	require.Equal(t, domain.VersionStatusCandidate, candidate.Status)
	require.Equal(t, view.Active.ID, candidate.ParentRevisionID)
	require.Equal(t, "优化后的方法", candidate.Instructions)

	// 运行时参数优化必须拒绝。
	_, err = svc.CreateCandidate(context.Background(), view.Skill.ID, view.Active.ID, CandidateInput{
		Source: "llm_rewrite", PromptPatch: map[string]any{"temperature": 0.2},
	})
	require.Error(t, err)
}

func TestVersionServiceResolvePublishedRevisionRejectsCandidate(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	view := mustCreateSkill(t, svc)

	// CreateSkill 第一版即 published,可直接解析。
	resolved, err := svc.ResolvePublishedRevision(context.Background(), view.Skill.ID, view.Active.ID)
	require.NoError(t, err)
	require.Equal(t, view.Active.ID, resolved.ID)

	// candidate 不是 published,评测解析必须拒绝。
	candidate, err := svc.CreateCandidate(context.Background(), view.Skill.ID, view.Active.ID, CandidateInput{
		Source: "llm_rewrite", PromptPatch: map[string]any{"instructions": "x"},
	})
	require.NoError(t, err)
	_, err = svc.ResolvePublishedRevision(context.Background(), view.Skill.ID, candidate.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not published")
}

func TestVersionServicePublishedRevisionSafeSummaryHasNoSensitiveFields(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	view := mustCreateSkill(t, svc)

	summary, err := svc.PublishedRevisionSafeSummary(context.Background(), view.Skill.ID, view.Active.ID)
	require.NoError(t, err)
	require.Equal(t, view.Skill.Name, summary["name"])
	require.Equal(t, view.Skill.Description, summary["description"])
	for _, key := range []string{"secret", "token", "api_key", "destination", "instructions"} {
		_, ok := summary[key]
		require.False(t, ok, "safe summary must not contain %q", key)
	}
}

func TestVersionServiceSaveRevisionFromLegacyUnpublishedSkill(t *testing.T) {
	// 存量未发布 skill(active_revision_id 空)首次保存生成第一版。
	repo := newFakeVersionRepo()
	repo.skills["legacy"] = port.SkillProductRow{ID: "legacy", Name: "legacy", CreatedBy: "user-1"}
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	view, err := svc.SaveRevision(context.Background(), "legacy", "", SaveRevisionInput{
		Name: "legacy", Description: "desc", Instructions: "first content", ActorID: "user-1",
	})
	require.NoError(t, err)
	require.Equal(t, 1, view.Active.RevisionNo)
	require.Equal(t, view.Active.ID, view.Skill.ActiveRevisionID)
}

func TestVersionServiceListRevisionsAndRollback(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	view := mustCreateSkill(t, svc) // v1 published + active
	// 保存一版新的 → v2 active、v1 deprecated。
	next, err := svc.SaveRevision(context.Background(), view.Skill.ID, "", SaveRevisionInput{
		Name: "complaint", Description: "分类", Instructions: "v2 content", ActorID: "user-1",
	})
	require.NoError(t, err)

	revisions, err := svc.ListRevisions(context.Background(), view.Skill.ID)
	require.NoError(t, err)
	require.Len(t, revisions, 2)
	require.Equal(t, next.Active.ID, revisions[0].ID)
	require.True(t, revisions[0].IsCurrent)
	require.Equal(t, view.Active.ID, revisions[1].ID)
	require.False(t, revisions[1].IsCurrent)
	require.Equal(t, domain.VersionStatusDeprecated, revisions[1].Status)

	// 回滚到 v1 → v1 生效、v2 降级,不产生新版本。
	require.NoError(t, svc.RollbackRevision(context.Background(), view.Skill.ID, view.Active.ID, "user-1"))
	revisions, err = svc.ListRevisions(context.Background(), view.Skill.ID)
	require.NoError(t, err)
	require.Len(t, revisions, 2, "rollback must not create a new version")
	require.True(t, revisions[1].IsCurrent)
	require.Equal(t, domain.VersionStatusPublished, revisions[1].Status)
	require.Equal(t, domain.VersionStatusDeprecated, revisions[0].Status)
}

func TestVersionServiceRollbackRejectsNonDeprecated(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	view := mustCreateSkill(t, svc)
	candidate, err := svc.CreateCandidate(context.Background(), view.Skill.ID, view.Active.ID, CandidateInput{
		Source: "llm_rewrite", PromptPatch: map[string]any{"instructions": "x"},
	})
	require.NoError(t, err)

	// 当前生效版本(非 deprecated)不可回滚。
	require.ErrorIs(t, svc.RollbackRevision(context.Background(), view.Skill.ID, view.Active.ID, "user-1"), domain.ErrSkillNotFound)
	// candidate 不是历史版本,不可回滚。
	require.ErrorIs(t, svc.RollbackRevision(context.Background(), view.Skill.ID, candidate.ID, "user-1"), domain.ErrSkillNotFound)
}

func TestVersionServiceListRevisionsMissingSkill(t *testing.T) {
	svc := NewVersionService(newFakeVersionRepo(), zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	_, err := svc.ListRevisions(context.Background(), "ghost")
	require.ErrorIs(t, err, domain.ErrSkillNotFound)
}

func TestVersionServiceListAndDeleteSkills(t *testing.T) {
	repo := newFakeVersionRepo()
	seedRevision(t, repo, "s1", "rev-s1", domain.VersionStatusPublished, "user-1")
	seedRevision(t, repo, "s2", "rev-s2", domain.VersionStatusPublished, "user-1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	skills, err := svc.ListSkills(context.Background())
	require.NoError(t, err)
	require.Len(t, skills, 2)

	require.NoError(t, svc.DeleteSkill(context.Background(), "s1", "user-1"))
	skills, err = svc.ListSkills(context.Background())
	require.NoError(t, err)
	require.Len(t, skills, 1)
}

func mustCreateSkill(t *testing.T, svc *VersionService) SkillWorkspaceView {
	t.Helper()
	view, err := svc.CreateSkill(context.Background(), CreateSkillInput{
		Name: "complaint", Description: "分类", Instructions: "分类用户投诉", ActorID: "user-1",
	})
	require.NoError(t, err)
	return view
}

// ---------- fakeVersionRepo ----------

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

func (r *fakeVersionRepo) InsertSkill(_ context.Context, skill port.SkillProductRow, revision domain.SkillRevision, audit *auditdomain.ResourceChangeAuditEvent, _ []string) error {
	r.recordAudit(audit)
	r.skills[skill.ID] = skill
	r.revisions[revision.ID] = revision
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
func (r *fakeVersionRepo) SaveRevision(_ context.Context, skillID, expected string, skill port.SkillProductRow, revision domain.SkillRevision, audit *auditdomain.ResourceChangeAuditEvent, _ string) (domain.SkillRevision, error) {
	r.recordAudit(audit)
	current, ok := r.skills[skillID]
	if !ok {
		return domain.SkillRevision{}, domain.ErrSkillNotFound
	}
	// 与真实 repo 一致:非空 expected 必须匹配当前生效版本 hash。
	if expected != "" {
		active, ok := r.revisions[current.ActiveRevisionID]
		if !ok || active.ContentHash != expected {
			return domain.SkillRevision{}, domain.ErrSkillDraftStale
		}
	}
	// 旧生效版本降级为历史。
	if old, ok := r.revisions[current.ActiveRevisionID]; ok && old.ID != revision.ID {
		old.Status = domain.VersionStatusDeprecated
		r.revisions[old.ID] = old
	}
	skill.ActiveRevisionID = revision.ID
	r.skills[skillID] = skill
	r.revisions[revision.ID] = revision
	return revision, nil
}
func (r *fakeVersionRepo) ListRevisions(_ context.Context, skillID string) ([]domain.SkillRevision, bool, error) {
	skill, ok := r.skills[skillID]
	if !ok {
		return nil, false, nil
	}
	var result []domain.SkillRevision
	for _, rev := range r.revisions {
		if rev.SkillID == skillID {
			rev.IsCurrent = rev.ID == skill.ActiveRevisionID
			result = append(result, rev)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].RevisionNo > result[j].RevisionNo })
	return result, true, nil
}
func (r *fakeVersionRepo) RollbackRevision(_ context.Context, skillID, targetRevisionID, _ string, audit *auditdomain.ResourceChangeAuditEvent) error {
	r.recordAudit(audit)
	skill, ok := r.skills[skillID]
	if !ok {
		return domain.ErrSkillNotFound
	}
	if cur, ok := r.revisions[skill.ActiveRevisionID]; ok {
		cur.Status = domain.VersionStatusDeprecated
		r.revisions[cur.ID] = cur
	}
	target, ok := r.revisions[targetRevisionID]
	if !ok || target.SkillID != skillID || target.Status != domain.VersionStatusDeprecated {
		return domain.ErrSkillNotFound
	}
	target.Status = domain.VersionStatusPublished
	r.revisions[targetRevisionID] = target
	skill.ActiveRevisionID = targetRevisionID
	r.skills[skillID] = skill
	return nil
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
func (r *fakeVersionRepo) SaveDraft(_ context.Context, skillID, expected string, draft domain.SkillRevision, _ string, audit *auditdomain.ResourceChangeAuditEvent) error {
	r.recordAudit(audit)
	skill, ok := r.skills[skillID]
	if !ok {
		return domain.ErrSkillNotFound
	}
	if expected != "" {
		active, ok := r.revisions[skill.ActiveRevisionID]
		if !ok || active.ContentHash != expected {
			return domain.ErrSkillDraftStale
		}
	}
	// 覆盖既有草稿(保持 id 不变)或首次插入。
	if old, ok := r.revisions[skill.DraftRevisionID]; ok && old.SkillID == skillID && old.Status == domain.VersionStatusDraft {
		old.Name, old.Description, old.Instructions = draft.Name, draft.Description, draft.Instructions
		old.ContentHash = draft.ContentHash
		r.revisions[old.ID] = old
	} else {
		r.revisions[draft.ID] = draft
		skill.DraftRevisionID = draft.ID
		r.skills[skillID] = skill
	}
	return nil
}
func (r *fakeVersionRepo) PublishDraft(_ context.Context, skillID, draftID, parentRevisionID string, next int, expected, _ string, audit *auditdomain.ResourceChangeAuditEvent) error {
	r.recordAudit(audit)
	skill, ok := r.skills[skillID]
	if !ok {
		return domain.ErrSkillNotFound
	}
	if expected != "" {
		active, ok := r.revisions[skill.ActiveRevisionID]
		if !ok || active.ContentHash != expected {
			return domain.ErrSkillDraftStale
		}
	}
	draft, ok := r.revisions[draftID]
	if !ok || draft.SkillID != skillID || draft.Status != domain.VersionStatusDraft {
		return domain.ErrSkillDraftNotFound
	}
	// 顺序必须 demote→promote:旧生效版本先降级,再转正草稿。
	if cur, ok := r.revisions[skill.ActiveRevisionID]; ok && cur.ID != draftID {
		cur.Status = domain.VersionStatusDeprecated
		r.revisions[cur.ID] = cur
	}
	draft.Status = domain.VersionStatusPublished
	draft.RevisionNo = next
	draft.ParentRevisionID = parentRevisionID
	r.revisions[draftID] = draft
	skill.ActiveRevisionID = draftID
	skill.DraftRevisionID = ""
	r.skills[skillID] = skill
	return nil
}
func (r *fakeVersionRepo) DiscardDraft(_ context.Context, skillID, _ string, audit *auditdomain.ResourceChangeAuditEvent) error {
	r.recordAudit(audit)
	skill, ok := r.skills[skillID]
	if !ok {
		return domain.ErrSkillNotFound
	}
	if draft, ok := r.revisions[skill.DraftRevisionID]; ok && draft.SkillID == skillID && draft.Status == domain.VersionStatusDraft {
		delete(r.revisions, draft.ID)
	}
	skill.DraftRevisionID = ""
	r.skills[skillID] = skill
	return nil
}

// seedSkill 直接注入 skill + revision 到 fake repo(绕过写路径,便于构造存量场景)。
func seedSkill(repo *fakeVersionRepo, skill port.SkillProductRow, revision domain.SkillRevision) {
	repo.skills[skill.ID] = skill
	repo.revisions[revision.ID] = revision
}

// seedRevision 构造一个带 content hash 的版本化 skill;published 状态时设为生效版本。
func seedRevision(t *testing.T, repo *fakeVersionRepo, skillID, revisionID string, status domain.VersionStatus, createdBy string) (port.SkillProductRow, domain.SkillRevision) {
	t.Helper()
	revision := domain.SkillRevision{
		ID: revisionID, SkillID: skillID, Status: status, Source: "manual",
		Name: "skill-" + skillID, Description: "desc", Instructions: "original instructions", CreatedBy: createdBy,
	}
	if status == domain.VersionStatusPublished {
		revision.RevisionNo = 1
	}
	hash, err := revision.ComputeContentHash()
	require.NoError(t, err)
	revision.ContentHash = hash
	skill := port.SkillProductRow{ID: skillID, Name: "skill-" + skillID, Description: "desc", CreatedBy: createdBy}
	if status == domain.VersionStatusPublished {
		skill.Status = "published"
		skill.ActiveRevisionID = revisionID
	}
	seedSkill(repo, skill, revision)
	return skill, revision
}

func TestVersionServiceSaveDraft(t *testing.T) {
	svc := NewVersionService(newFakeVersionRepo(), zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	view := mustCreateSkill(t, svc)

	out, err := svc.SaveDraft(context.Background(), view.Skill.ID, view.Active.ContentHash,
		SaveDraftInput{Name: "draft-name", Description: "draft-desc", Instructions: "draft-ins", ActorID: "user-1"})
	require.NoError(t, err)
	require.True(t, out.HasDraft)
	// 保存草稿不改变当前生效版本。
	require.Equal(t, view.Active.ID, out.Active.ID)
	require.Equal(t, view.Active.ContentHash, out.Active.ContentHash)

	// 再次保存 = 覆盖更新,草稿 id 复用(service 复用 skill.DraftRevisionID)。
	out2, err := svc.SaveDraft(context.Background(), view.Skill.ID, view.Active.ContentHash,
		SaveDraftInput{Name: "draft-2", Description: "draft-desc", Instructions: "draft-ins", ActorID: "user-1"})
	require.NoError(t, err)
	require.True(t, out2.HasDraft)
	require.Equal(t, out.Skill.DraftRevisionID, out2.Skill.DraftRevisionID)
}

func TestVersionServiceSaveDraftStale(t *testing.T) {
	svc := NewVersionService(newFakeVersionRepo(), zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	view := mustCreateSkill(t, svc)

	_, err := svc.SaveDraft(context.Background(), view.Skill.ID, "stale-hash",
		SaveDraftInput{Name: "n", Description: "d", Instructions: "i", ActorID: "user-1"})
	require.ErrorIs(t, err, domain.ErrSkillDraftStale)
}

func TestVersionServicePublishDraft(t *testing.T) {
	svc := NewVersionService(newFakeVersionRepo(), zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	view := mustCreateSkill(t, svc)
	_, err := svc.SaveDraft(context.Background(), view.Skill.ID, view.Active.ContentHash,
		SaveDraftInput{Name: "draft-name", Description: "draft-desc", Instructions: "draft-ins", ActorID: "user-1"})
	require.NoError(t, err)

	out, err := svc.PublishDraft(context.Background(), view.Skill.ID, view.Active.ContentHash, "user-1")
	require.NoError(t, err)
	require.False(t, out.HasDraft)
	require.Equal(t, "draft-name", out.Active.Name)
	require.Equal(t, view.Active.RevisionNo+1, out.Active.RevisionNo)
	// 旧生效版本降级。
	revisions, err := svc.ListRevisions(context.Background(), view.Skill.ID)
	require.NoError(t, err)
	require.Len(t, revisions, 2)
	require.Equal(t, domain.VersionStatusDeprecated, revisions[1].Status)
}

func TestVersionServicePublishDraftNoDraft(t *testing.T) {
	svc := NewVersionService(newFakeVersionRepo(), zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	view := mustCreateSkill(t, svc)

	_, err := svc.PublishDraft(context.Background(), view.Skill.ID, view.Active.ContentHash, "user-1")
	require.ErrorIs(t, err, domain.ErrSkillDraftNotFound)
}

func TestVersionServicePublishDraftNotPublishable(t *testing.T) {
	svc := NewVersionService(newFakeVersionRepo(), zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	view := mustCreateSkill(t, svc)
	// SaveDraft 允许空指令(草稿可半成品),发布时 ValidatePublishable 拒绝。
	_, err := svc.SaveDraft(context.Background(), view.Skill.ID, view.Active.ContentHash,
		SaveDraftInput{Name: "draft-name", Description: "draft-desc", Instructions: "", ActorID: "user-1"})
	require.NoError(t, err)

	_, err = svc.PublishDraft(context.Background(), view.Skill.ID, view.Active.ContentHash, "user-1")
	require.ErrorIs(t, err, domain.ErrSkillNotPublishable)
	// 校验失败不破坏既有草稿:再次发布仍报 NotPublishable 而非 NotFound。
	_, err = svc.PublishDraft(context.Background(), view.Skill.ID, view.Active.ContentHash, "user-1")
	require.ErrorIs(t, err, domain.ErrSkillNotPublishable)
}

func TestVersionServiceDiscardDraft(t *testing.T) {
	svc := NewVersionService(newFakeVersionRepo(), zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	view := mustCreateSkill(t, svc)
	_, err := svc.SaveDraft(context.Background(), view.Skill.ID, view.Active.ContentHash,
		SaveDraftInput{Name: "draft-name", Description: "draft-desc", Instructions: "draft-ins", ActorID: "user-1"})
	require.NoError(t, err)

	out, err := svc.DiscardDraft(context.Background(), view.Skill.ID, "user-1")
	require.NoError(t, err)
	require.False(t, out.HasDraft)
	// 表单回填当前生效版本。
	require.Equal(t, view.Active.Name, out.Active.Name)
	require.Equal(t, view.Active.ContentHash, out.Active.ContentHash)

	// 幂等:无草稿再次撤销成功。
	_, err = svc.DiscardDraft(context.Background(), view.Skill.ID, "user-1")
	require.NoError(t, err)
}
