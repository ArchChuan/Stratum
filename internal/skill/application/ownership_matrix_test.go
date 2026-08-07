package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// failingTenantRoleResolver resolves no role at all so ownership checks fail
// closed on resolver failure (IAM outage must not default-open writes).
type failingTenantRoleResolver struct{ err error }

func (f failingTenantRoleResolver) ResolveTenantRole(context.Context, string, string) (string, error) {
	return "", f.err
}

// failingVersionRepo embeds the audit-capturing fakeVersionRepo and makes the
// UpdateDraftBundle write fail, so tests can assert the repository error is
// propagated (fail closed) instead of being swallowed.
type failingVersionRepo struct {
	*fakeVersionRepo
	failErr error
}

func (f *failingVersionRepo) UpdateDraftBundle(
	ctx context.Context, skillID, expected string, skill port.SkillProductRow, draft domain.SkillRevision, audit *auditdomain.ResourceChangeAuditEvent,
) (domain.SkillRevision, error) {
	return domain.SkillRevision{}, f.failErr
}

// seedOwnedSkill inserts a draft skill whose product row carries the given
// created_by, plus a draft revision whose hash is returned for
// UpdateDraftBundle's expected-content-hash argument.
func seedOwnedSkill(t *testing.T, repo *fakeVersionRepo, skillID, createdBy string) (port.SkillProductRow, domain.SkillRevision) {
	t.Helper()
	draft := domain.SkillRevision{
		ID: "rev-" + skillID, SkillID: skillID, Status: domain.VersionStatusDraft,
		Source: "manual", Instructions: "original instructions",
		Capability:         domain.Capability{Goal: "goal", WhenToUse: "when"},
		ActivationContract: domain.ActivationContract{Name: "act"},
	}
	hash, err := draft.ComputeContentHash()
	require.NoError(t, err)
	draft.ContentHash = hash
	skill := port.SkillProductRow{
		ID: skillID, Name: "skill-" + skillID, Description: "desc",
		Status: "draft", DraftRevisionID: draft.ID, CreatedBy: createdBy,
	}
	require.NoError(t, repo.InsertSkillWithDraft(context.Background(), skill, draft, nil))
	return skill, draft
}

// TestOwnershipMatrixSkillUpdateDraftBundle pins the ownership matrix for
// UpdateDraftBundle: owner may manage the whole tenant (including historical
// resources with empty created_by), admin only their own, everyone else —
// and every resolution failure, missing resolver and empty actor — is denied.
// Fail closed.
func TestOwnershipMatrixSkillUpdateDraftBundle(t *testing.T) {
	const (
		actor = "user-1"
		other = "other-user"
	)
	rows := []struct {
		name        string
		role        string
		noResolver  bool
		resolveErr  error
		actorID     string
		createdBy   string
		wantAllowed bool
	}{
		{"owner edits another user's resource", "owner", false, nil, actor, other, true},
		{"owner edits empty-owner resource", "owner", false, nil, actor, "", true},
		{"admin edits own resource", "admin", false, nil, actor, actor, true},
		{"admin edits another user's resource", "admin", false, nil, actor, other, false},
		{"admin edits empty-owner resource", "admin", false, nil, actor, "", false},
		{"member edits own resource", "member", false, nil, actor, actor, false},
		{"resolver failure fails closed", "", false, errors.New("iam unavailable"), actor, actor, false},
		{"missing resolver fails closed", "", true, nil, actor, actor, false},
		{"empty actor fails closed", "owner", false, nil, "", actor, false},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeVersionRepo()
			skill, draft := seedOwnedSkill(t, repo, "skill-1", tc.createdBy)
			svc := NewVersionService(repo, zap.NewNop())
			if !tc.noResolver {
				svc.SetTenantRoleResolver(stubTenantRole{role: tc.role})
			}
			if tc.resolveErr != nil {
				svc.SetTenantRoleResolver(failingTenantRoleResolver{err: tc.resolveErr})
			}
			_, err := svc.UpdateDraftBundle(context.Background(), skill.ID, draft.ContentHash, UpdateDraftBundleInput{
				Name: "updated-name", Description: "updated-desc", Instructions: "updated instructions",
				ActorID: tc.actorID,
			})
			if tc.wantAllowed {
				require.NoError(t, err)
				require.NotEmpty(t, repo.audits)
				return
			}
			require.ErrorIs(t, err, domain.ErrForbidden)
			require.Empty(t, repo.audits, "denied write must not produce an audit event")
		})
	}
}

// TestSkillCreateDraftRecordsAuditEvent pins the create 打点: the creator
// becomes the skill's owner and the write carries a user/api create audit
// with an empty before projection and a non-empty after projection.
func TestSkillCreateDraftRecordsAuditEvent(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	view, err := svc.CreateSkillDraft(context.Background(), CreateSkillDraftInput{
		Name: "matrix-skill", Goal: "goal", WhenToUse: "when",
		Instructions: "instructions", ActorID: "user-1",
	})
	require.NoError(t, err)
	require.Len(t, repo.audits, 1)

	audit := repo.audits[0]
	require.Equal(t, auditdomain.ResourceKindSkill, audit.ResourceKind)
	require.Equal(t, view.Skill.ID, audit.ResourceID)
	require.Equal(t, auditdomain.ChangeOpCreate, audit.Operation)
	require.Equal(t, "user-1", audit.ActorID)
	require.Equal(t, auditdomain.ChangeActorUser, audit.ActorType)
	require.Equal(t, auditdomain.ChangeSourceAPI, audit.Source)
	require.Empty(t, audit.Before) // persistence normalizes nil before -> `{}` (skill_version_repo.go)
	require.NotEmpty(t, audit.After)
	var after map[string]any
	require.NoError(t, json.Unmarshal(audit.After, &after))
	require.Equal(t, "matrix-skill", after["name"])
}

// TestSkillUpdateDraftBundleAuditFields pins the update audit payload: exact
// resource identity, operation, actor, source and actor type.
func TestSkillUpdateDraftBundleAuditFields(t *testing.T) {
	repo := newFakeVersionRepo()
	skill, draft := seedOwnedSkill(t, repo, "skill-1", "user-1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	_, err := svc.UpdateDraftBundle(context.Background(), skill.ID, draft.ContentHash, UpdateDraftBundleInput{
		Name: "renamed", Description: "new desc", Instructions: "new instructions",
		ActorID: "user-1",
	})
	require.NoError(t, err)
	require.Len(t, repo.audits, 1)

	audit := repo.audits[0]
	require.Equal(t, auditdomain.ResourceKindSkill, audit.ResourceKind)
	require.Equal(t, skill.ID, audit.ResourceID)
	require.Equal(t, auditdomain.ChangeOpUpdate, audit.Operation)
	require.Equal(t, "user-1", audit.ActorID)
	require.Equal(t, auditdomain.ChangeActorUser, audit.ActorType)
	require.Equal(t, auditdomain.ChangeSourceAPI, audit.Source)
	require.Empty(t, audit.ProposalID)
	require.NotEmpty(t, audit.Before)
	var before map[string]any
	require.NoError(t, json.Unmarshal(audit.Before, &before))
	require.Equal(t, "skill-skill-1", before["name"])
	var after map[string]any
	require.NoError(t, json.Unmarshal(audit.After, &after))
	require.Equal(t, "renamed", after["name"])
}

// Audit-construction failure: newChangeAudit only fails when json.Marshal
// fails on the projections. skillSafeProjection emits plain strings and
// structs derived from domain types, so the failure path is unreachable
// through UpdateDraftBundle inputs — skipped by design. The fail-closed
// contract is instead pinned below by making the repository write itself
// fail: the error must propagate, never be swallowed.
func TestSkillUpdateDraftBundlePropagatesRepositoryFailure(t *testing.T) {
	repo := &failingVersionRepo{fakeVersionRepo: newFakeVersionRepo(), failErr: errors.New("audit insert failed")}
	skill, draft := seedOwnedSkill(t, repo.fakeVersionRepo, "skill-1", "user-1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	_, err := svc.UpdateDraftBundle(context.Background(), skill.ID, draft.ContentHash, UpdateDraftBundleInput{
		Name: "renamed", Description: "desc", Instructions: "instructions",
		ActorID: "user-1",
	})
	require.ErrorIs(t, err, repo.failErr)
}

// TestSkillUpdateDraftBundleSystemActorBypassesOwnership pins the evaluation
// worker path: a system actor in ctx skips the ownership matrix (member may
// rewrite another user's skill) but the write is still audited with
// actor_type=system and source=optimization.
func TestSkillUpdateDraftBundleSystemActorBypassesOwnership(t *testing.T) {
	repo := newFakeVersionRepo()
	skill, draft := seedOwnedSkill(t, repo, "skill-1", "other-user")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "member"})

	ctx := reqctx.WithSystemActor(context.Background(), "evaluation-worker")
	_, err := svc.UpdateDraftBundle(ctx, skill.ID, draft.ContentHash, UpdateDraftBundleInput{
		Name: "optimized", Description: "desc", Instructions: "optimized instructions",
		ActorID: "member-1",
	})
	require.NoError(t, err)
	require.Len(t, repo.audits, 1)

	audit := repo.audits[0]
	require.Equal(t, "evaluation-worker", audit.ActorID)
	require.Equal(t, auditdomain.ChangeActorSystem, audit.ActorType)
	require.Equal(t, auditdomain.ChangeSourceOptimization, audit.Source)
	require.Equal(t, auditdomain.ChangeOpUpdate, audit.Operation)
	require.Equal(t, skill.ID, audit.ResourceID)
}
