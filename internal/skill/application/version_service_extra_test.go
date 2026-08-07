package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// seedSkillDraft 在 fake repo 中插入 skill + 指定状态的 revision。
func seedSkillDraft(t *testing.T, repo *fakeVersionRepo, status domain.VersionStatus, skillID, revisionID string) {
	t.Helper()
	revisionNo := 1
	if status != domain.VersionStatusPublished {
		revisionNo = 0
	}
	draft := domain.SkillRevision{
		ID:           revisionID,
		SkillID:      skillID,
		Status:       status,
		RevisionNo:   revisionNo,
		Source:       "manual",
		Instructions: "do the thing",
		Capability: domain.Capability{Goal: "g", WhenToUse: "w",
			Examples: []domain.CapabilityExample{{Input: "in", ExpectedOutput: "out"}}},
		ActivationContract: domain.ActivationContract{Name: "act", Confirmed: true},
	}
	hash, err := draft.ComputeContentHash()
	require.NoError(t, err)
	draft.ContentHash = hash
	skill := port.SkillProductRow{ID: skillID, Name: "Skill " + skillID, Description: "desc", Status: "draft"}
	if status == domain.VersionStatusPublished {
		skill.Status = "published"
		skill.ActiveRevisionID = draft.ID
	}
	require.NoError(t, repo.InsertSkillWithDraft(context.Background(), skill, draft, nil, nil))
}

func TestVersionServiceGetWorkspacePrefersDraft(t *testing.T) {
	// 同时存在 draft 与 active 时返回 draft。
	repo := newFakeVersionRepo()
	seedSkillDraft(t, repo, domain.VersionStatusDraft, "s1", "rev-s1-draft")
	seedSkillDraft(t, repo, domain.VersionStatusPublished, "s1", "rev-s1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	view, err := svc.GetWorkspace(context.Background(), "s1")
	require.NoError(t, err)
	assert.Equal(t, "s1", view.Skill.ID)
	assert.Equal(t, domain.VersionStatusDraft, view.Draft.Status)
}

func TestVersionServiceGetWorkspaceFallsBackToActive(t *testing.T) {
	// 极端情况：无 draft 时回退 active revision。
	repo := newFakeVersionRepo()
	seedSkillDraft(t, repo, domain.VersionStatusPublished, "s1", "rev-s1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	view, err := svc.GetWorkspace(context.Background(), "s1")
	require.NoError(t, err)
	assert.Equal(t, domain.VersionStatusPublished, view.Draft.Status)
}

func TestVersionServiceGetWorkspaceMissingSkill(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	_, err := svc.GetWorkspace(context.Background(), "ghost")
	assert.ErrorIs(t, err, domain.ErrSkillNotFound)
}

func TestVersionServiceGetWorkspaceNoActiveEither(t *testing.T) {
	// 极端情况：skill 存在但 draft 与 active 都缺失 → ErrSkillNotFound。
	repo := newFakeVersionRepo()
	repo.skills["s1"] = port.SkillProductRow{ID: "s1"}
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	_, err := svc.GetWorkspace(context.Background(), "s1")
	assert.ErrorIs(t, err, domain.ErrSkillNotFound)
}

func TestVersionServiceListAndDeleteSkills(t *testing.T) {
	repo := newFakeVersionRepo()
	seedSkillDraft(t, repo, domain.VersionStatusDraft, "s1", "rev-s1")
	seedSkillDraft(t, repo, domain.VersionStatusDraft, "s2", "rev-s2")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	skills, err := svc.ListSkills(context.Background())
	require.NoError(t, err)
	assert.Len(t, skills, 2)

	require.NoError(t, svc.DeleteSkill(context.Background(), "s1", "user-1"))
	skills, err = svc.ListSkills(context.Background())
	require.NoError(t, err)
	assert.Len(t, skills, 1)
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
		{"archived is not evaluable", domain.VersionStatusDeprecated, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeVersionRepo()
			seedSkillDraft(t, repo, tc.status, "s1", "rev-s1")
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
	// 成功路径 + 非 published + 缺失。
	repo := newFakeVersionRepo()
	seedSkillDraft(t, repo, domain.VersionStatusPublished, "s1", "rev-s1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	rev, err := svc.ResolveActivePublishedRevision(context.Background(), "s1")
	require.NoError(t, err)
	assert.Equal(t, domain.VersionStatusPublished, rev.Status)

	// draft revision 未激活（ActiveRevisionID 空）。
	repo2 := newFakeVersionRepo()
	seedSkillDraft(t, repo2, domain.VersionStatusDraft, "s2", "rev-s2")
	svc2 := NewVersionService(repo2, zap.NewNop())
	svc2.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	_, err = svc2.ResolveActivePublishedRevision(context.Background(), "s2")
	assert.ErrorIs(t, err, domain.ErrSkillNotFound)

	// 极端情况：active 指向未 published revision。
	repo3 := newFakeVersionRepo()
	seedSkillDraft(t, repo3, domain.VersionStatusDraft, "s3", "rev-s3")
	repo3.skills["s3"] = port.SkillProductRow{ID: "s3", ActiveRevisionID: "rev-s3"}
	svc3 := NewVersionService(repo3, zap.NewNop())
	svc3.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	_, err = svc3.ResolveActivePublishedRevision(context.Background(), "s3")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not published")
}

func TestVersionServiceSafeSummaries(t *testing.T) {
	repo := newFakeVersionRepo()
	seedSkillDraft(t, repo, domain.VersionStatusPublished, "s1", "rev-s1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	pub, err := svc.PublishedRevisionSafeSummary(context.Background(), "s1", "rev-s1")
	require.NoError(t, err)
	assert.Equal(t, "Skill s1", pub["name"])
	assert.Equal(t, "revision-1", pub["version_label"])

	eval, err := svc.EvaluableRevisionSafeSummary(context.Background(), "s1", "rev-s1")
	require.NoError(t, err)
	assert.Equal(t, "revision-1", eval["version_label"])
}

func TestVersionServiceSafeSummariesRejectDraft(t *testing.T) {
	repo := newFakeVersionRepo()
	seedSkillDraft(t, repo, domain.VersionStatusDraft, "s1", "rev-s1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	if _, err := svc.PublishedRevisionSafeSummary(context.Background(), "s1", "rev-s1"); err == nil {
		t.Fatal("draft must not be publishable summary")
	}
	if _, err := svc.EvaluableRevisionSafeSummary(context.Background(), "s1", "rev-s1"); err == nil {
		t.Fatal("draft must not be evaluable summary")
	}
}

func TestVersionServiceUpdateCapability(t *testing.T) {
	// 成功路径：draft capability 被替换并刷新 hash。
	repo := newFakeVersionRepo()
	seedSkillDraft(t, repo, domain.VersionStatusDraft, "s1", "rev-s1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	rev, err := svc.UpdateCapability(context.Background(), "s1", UpdateCapabilityInput{
		Goal: "new goal", WhenToUse: "new when", InputSpec: "in", OutputSpec: "out",
		ActorID: "user-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "new goal", rev.Capability.Goal)
	assert.Equal(t, "in", rev.Capability.InputSpec)

	// 无 draft → ErrSkillNotFound。
	_, err = svc.UpdateCapability(context.Background(), "ghost", UpdateCapabilityInput{ActorID: "user-1"})
	assert.ErrorIs(t, err, domain.ErrSkillNotFound)
}

func TestVersionServiceUpdateActivationDefaultsSchemas(t *testing.T) {
	// 极端情况：nil schema 回退为 {"type":"object"}。
	repo := newFakeVersionRepo()
	seedSkillDraft(t, repo, domain.VersionStatusDraft, "s1", "rev-s1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	rev, err := svc.UpdateActivation(context.Background(), "s1", UpdateActivationInput{Name: "act", ActorID: "user-1"})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"type": "object"}, rev.ActivationContract.InputSchema)
	assert.Equal(t, map[string]any{"type": "object"}, rev.ActivationContract.OutputSchema)
	assert.False(t, rev.ActivationContract.Confirmed)

	// 显式 schema 保留。
	rev, err = svc.UpdateActivation(context.Background(), "s1", UpdateActivationInput{
		Name: "act", InputSchema: map[string]any{"type": "string"}, Confirmed: true,
		ActorID: "user-1",
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"type": "string"}, rev.ActivationContract.InputSchema)
	assert.True(t, rev.ActivationContract.Confirmed)
}

func TestVersionServiceCreateCandidateSuccess(t *testing.T) {
	repo := newFakeVersionRepo()
	seedSkillDraft(t, repo, domain.VersionStatusPublished, "s1", "rev-s1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	candidate, err := svc.CreateCandidate(context.Background(), "s1", "rev-s1", CandidateInput{
		Source:             "llm_rewrite",
		PromptPatch:        map[string]any{"instructions": "  rewritten  "},
		GenerationMetadata: map[string]any{"model": "m"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.VersionStatusCandidate, candidate.Status)
	assert.Equal(t, "  rewritten  ", candidate.Instructions) // 实现原样保留，不 trim
	assert.Equal(t, "rev-s1", candidate.ParentRevisionID)
	assert.Equal(t, "m", candidate.GenerationMetadata["model"])
}

func TestVersionServiceCreateCandidateRejections(t *testing.T) {
	repo := newFakeVersionRepo()
	seedSkillDraft(t, repo, domain.VersionStatusPublished, "s1", "rev-s1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	// 不支持的 source。
	_, err := svc.CreateCandidate(context.Background(), "s1", "rev-s1", CandidateInput{Source: "hand"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported candidate source")

	// 极端情况：修改非 instructions 字段被拒。
	_, err = svc.CreateCandidate(context.Background(), "s1", "rev-s1", CandidateInput{
		Source: "llm_rewrite", PromptPatch: map[string]any{"goal": "x"},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not optimizable")

	// 极端情况：instructions 非字符串或空白。
	for _, patch := range []map[string]any{
		{"instructions": 42},
		{"instructions": "   "},
	} {
		_, err = svc.CreateCandidate(context.Background(), "s1", "rev-s1", CandidateInput{
			Source: "llm_rewrite", PromptPatch: patch,
		})
		assert.Error(t, err)
	}

	// 缺失 baseline → ErrSkillNotFound。
	_, err = svc.CreateCandidate(context.Background(), "s1", "rev-missing", CandidateInput{Source: "llm_rewrite"})
	assert.ErrorIs(t, err, domain.ErrSkillNotFound)
}

func TestVersionServiceUpdateDraftBundleStaleHash(t *testing.T) {
	// 极端情况：期望 hash 与当前不一致 → ErrSkillDraftStale。
	repo := newFakeVersionRepo()
	seedSkillDraft(t, repo, domain.VersionStatusDraft, "s1", "rev-s1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	_, err := svc.UpdateDraftBundle(context.Background(), "s1", "stale-hash", UpdateDraftBundleInput{Name: "x", ActorID: "user-1"})
	assert.ErrorIs(t, err, domain.ErrSkillDraftStale)
}

func TestVersionServiceUpdateDraftBundleMissingSkill(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	_, err := svc.UpdateDraftBundle(context.Background(), "ghost", "", UpdateDraftBundleInput{ActorID: "user-1"})
	assert.ErrorIs(t, err, domain.ErrSkillNotFound)
}

func TestGeneratedActivationNameBoundaries(t *testing.T) {
	// 极端情况：空名、数字开头、特殊字符、超长截断。
	cases := []struct {
		in   string
		want string
	}{
		{"", "skill_"},
		{"123name", "skill_123name"},
		{"My Skill!", "my_skill"},
		{"__", "skill_"},
		{strings.Repeat("a", 80), strings.Repeat("a", 64)},
		{"camelCase_Name", "camelcase_name"},
	}
	for _, tc := range cases {
		got := generatedActivationName(tc.in)
		assert.Equal(t, tc.want, got, "generatedActivationName(%q)", tc.in)
	}
}

func TestVersionServiceUpdateInstructionBundle(t *testing.T) {
	repo := newFakeVersionRepo()
	seedSkillDraft(t, repo, domain.VersionStatusDraft, "s1", "rev-s1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	rev, err := svc.UpdateInstructionBundle(context.Background(), "s1", UpdateInstructionBundleInput{
		Instructions: "new instructions",
		ActorID:      "user-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "new instructions", rev.Instructions)

	_, err = svc.UpdateInstructionBundle(context.Background(), "ghost", UpdateInstructionBundleInput{ActorID: "user-1"})
	assert.ErrorIs(t, err, domain.ErrSkillNotFound)
}

var _ = errors.Is
