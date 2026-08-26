package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestVersionServiceGetWorkspaceReturnsActive(t *testing.T) {
	repo := newFakeVersionRepo()
	seedRevision(t, repo, "s1", "rev-s1", domain.VersionStatusPublished, "user-1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	view, err := svc.GetWorkspace(context.Background(), "s1", "owner-1")
	require.NoError(t, err)
	require.Equal(t, "s1", view.Skill.ID)
	require.Equal(t, domain.VersionStatusPublished, view.Active.Status)
	require.Equal(t, "rev-s1", view.Active.ID)
}

func TestVersionServiceGetWorkspaceMissingSkill(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	_, err := svc.GetWorkspace(context.Background(), "ghost", "owner-1")
	assert.ErrorIs(t, err, domain.ErrSkillNotFound)
}

func TestVersionServiceGetWorkspaceLegacyUnpublished(t *testing.T) {
	// 存量未发布 skill(无 active revision)返回空 Active,不报错——前端可编辑,
	// 首次保存生成第一版。
	repo := newFakeVersionRepo()
	repo.skills["s1"] = port.SkillProductRow{ID: "s1"}
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	view, err := svc.GetWorkspace(context.Background(), "s1", "owner-1")
	require.NoError(t, err)
	require.Empty(t, view.Active.ID)
}

func TestVersionServiceResolveEvaluableRevision(t *testing.T) {
	cases := []struct {
		name   string
		status domain.VersionStatus
		ok     bool
	}{
		{"published is evaluable", domain.VersionStatusPublished, true},
		{"candidate is evaluable", domain.VersionStatusCandidate, true},
		{"draft is not evaluable", domain.VersionStatusDraft, false},
		{"deprecated is not evaluable", domain.VersionStatusDeprecated, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeVersionRepo()
			seedRevision(t, repo, "s1", "rev-s1", tc.status, "user-1")
			svc := NewVersionService(repo, zap.NewNop())
			svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
			rev, err := svc.ResolveEvaluableRevision(context.Background(), "s1", "rev-s1")
			if tc.ok {
				require.NoError(t, err)
				assert.Equal(t, tc.status, rev.Status)
			} else {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "not evaluable")
			}
		})
	}
}

func TestVersionServiceResolveEvaluableRevisionMissing(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	_, err := svc.ResolveEvaluableRevision(context.Background(), "s1", "rev-x")
	assert.ErrorIs(t, err, domain.ErrSkillNotFound)
}

func TestVersionServiceResolveActivePublishedRevision(t *testing.T) {
	// 成功路径。
	repo := newFakeVersionRepo()
	seedRevision(t, repo, "s1", "rev-s1", domain.VersionStatusPublished, "user-1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	rev, err := svc.ResolveActivePublishedRevision(context.Background(), "s1")
	require.NoError(t, err)
	assert.Equal(t, domain.VersionStatusPublished, rev.Status)

	// 无 active revision(存量未发布)→ ErrSkillNotFound。
	repo2 := newFakeVersionRepo()
	seedRevision(t, repo2, "s2", "rev-s2", domain.VersionStatusDraft, "user-1")
	svc2 := NewVersionService(repo2, zap.NewNop())
	svc2.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	_, err = svc2.ResolveActivePublishedRevision(context.Background(), "s2")
	assert.ErrorIs(t, err, domain.ErrSkillNotFound)

	// 极端情况:active 指向未 published revision。
	repo3 := newFakeVersionRepo()
	seedRevision(t, repo3, "s3", "rev-s3", domain.VersionStatusDraft, "user-1")
	repo3.skills["s3"] = port.SkillProductRow{ID: "s3", ActiveRevisionID: "rev-s3"}
	svc3 := NewVersionService(repo3, zap.NewNop())
	svc3.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	_, err = svc3.ResolveActivePublishedRevision(context.Background(), "s3")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not published")
}

func TestVersionServiceSafeSummaries(t *testing.T) {
	repo := newFakeVersionRepo()
	seedRevision(t, repo, "s1", "rev-s1", domain.VersionStatusPublished, "user-1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	pub, err := svc.PublishedRevisionSafeSummary(context.Background(), "s1", "rev-s1")
	require.NoError(t, err)
	assert.Equal(t, "skill-s1", pub["name"])
	assert.Equal(t, "revision-1", pub["version_label"])

	eval, err := svc.EvaluableRevisionSafeSummary(context.Background(), "s1", "rev-s1")
	require.NoError(t, err)
	assert.Equal(t, "revision-1", eval["version_label"])
}

func TestVersionServiceSafeSummariesRejectDraft(t *testing.T) {
	repo := newFakeVersionRepo()
	seedRevision(t, repo, "s1", "rev-s1", domain.VersionStatusDraft, "user-1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	if _, err := svc.PublishedRevisionSafeSummary(context.Background(), "s1", "rev-s1"); err == nil {
		t.Fatal("draft must not be publishable summary")
	}
	if _, err := svc.EvaluableRevisionSafeSummary(context.Background(), "s1", "rev-s1"); err == nil {
		t.Fatal("draft must not be evaluable summary")
	}
}

func TestVersionServiceCreateCandidateSuccess(t *testing.T) {
	repo := newFakeVersionRepo()
	_, baseline := seedRevision(t, repo, "s1", "rev-s1", domain.VersionStatusPublished, "user-1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	candidate, err := svc.CreateCandidate(context.Background(), "s1", baseline.ID, CandidateInput{
		Source:             "llm_rewrite",
		PromptPatch:        map[string]any{"instructions": "  rewritten  "},
		GenerationMetadata: map[string]any{"model": "m"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.VersionStatusCandidate, candidate.Status)
	assert.Equal(t, "  rewritten  ", candidate.Instructions) // 实现原样保留,不 trim
	assert.Equal(t, baseline.ID, candidate.ParentRevisionID)
	assert.Equal(t, "m", candidate.GenerationMetadata["model"])
}

func TestVersionServiceCreateCandidateRejections(t *testing.T) {
	repo := newFakeVersionRepo()
	_, baseline := seedRevision(t, repo, "s1", "rev-s1", domain.VersionStatusPublished, "user-1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	// 不支持的 source。
	_, err := svc.CreateCandidate(context.Background(), "s1", baseline.ID, CandidateInput{Source: "hand"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported candidate source")

	// 修改非 instructions 字段被拒。
	_, err = svc.CreateCandidate(context.Background(), "s1", baseline.ID, CandidateInput{
		Source: "llm_rewrite", PromptPatch: map[string]any{"goal": "x"},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not optimizable")

	// instructions 非字符串或空白。
	for _, patch := range []map[string]any{
		{"instructions": 42},
		{"instructions": "   "},
	} {
		_, err = svc.CreateCandidate(context.Background(), "s1", baseline.ID, CandidateInput{
			Source: "llm_rewrite", PromptPatch: patch,
		})
		assert.Error(t, err)
	}

	// 缺失 baseline → ErrSkillNotFound。
	_, err = svc.CreateCandidate(context.Background(), "s1", "rev-missing", CandidateInput{Source: "llm_rewrite"})
	assert.ErrorIs(t, err, domain.ErrSkillNotFound)
}

func TestVersionServiceSaveRevisionStaleHash(t *testing.T) {
	// 期望 hash 与当前生效版本不一致 → ErrSkillDraftStale(409)。
	repo := newFakeVersionRepo()
	seedRevision(t, repo, "s1", "rev-s1", domain.VersionStatusPublished, "user-1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	_, err := svc.SaveRevision(context.Background(), "s1", "stale-hash", SaveRevisionInput{
		Name: "x", Description: "d", Instructions: "i", ActorID: "user-1",
	})
	assert.ErrorIs(t, err, domain.ErrSkillDraftStale)
}

func TestVersionServiceSaveRevisionMissingSkill(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	_, err := svc.SaveRevision(context.Background(), "ghost", "", SaveRevisionInput{
		Name: "x", Description: "d", Instructions: "i", ActorID: "user-1",
	})
	assert.ErrorIs(t, err, domain.ErrSkillNotFound)
}
