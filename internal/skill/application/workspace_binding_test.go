package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

var errSkillBindingRejected = errors.New("skill binding rejected")

// recordingBindingValidator implements port.WorkspaceBindingValidator for
// tests: fails when err is set, otherwise records the validated names.
type recordingBindingValidator struct {
	got  [][]string
	err  error
	call int
}

func (v *recordingBindingValidator) ValidateWorkspaceBindings(_ context.Context, _ string, names []string) error {
	v.call++
	v.got = append(v.got, names)
	return v.err
}

func newBindingVersionService(t *testing.T, repo port.VersionRepo, validator port.WorkspaceBindingValidator) *VersionService {
	t.Helper()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	svc.SetWorkspaceBindingValidator(validator)
	return svc
}

func draftInput(bound bool) CreateSkillDraftInput {
	in := CreateSkillDraftInput{
		Name: "s", Goal: "g", WhenToUse: "w",
		SampleInput: "in", ExpectedOutput: "out",
		Instructions: "do it",
		ActorID:      "u1",
	}
	if bound {
		in.Requirements = domain.Requirements{KnowledgeWorkspaceIDs: []string{"legal", "hr"}}
	}
	return in
}

func TestCreateSkillDraftRejectsUnknownWorkspaceBinding(t *testing.T) {
	repo := newFakeVersionRepo()
	validator := &recordingBindingValidator{err: errSkillBindingRejected}
	svc := newBindingVersionService(t, repo, validator)

	_, err := svc.CreateSkillDraft(context.Background(), draftInput(true))
	require.ErrorIs(t, err, errSkillBindingRejected)
	require.Equal(t, [][]string{{"legal", "hr"}}, validator.got)
}

func TestCreateSkillDraftFailsClosedWithoutValidator(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	_, err := svc.CreateSkillDraft(context.Background(), draftInput(true))
	require.ErrorContains(t, err, "workspace binding validation unavailable")
}

func TestCreateSkillDraftSkipsValidationWhenUnbound(t *testing.T) {
	repo := newFakeVersionRepo()
	validator := &recordingBindingValidator{}
	svc := newBindingVersionService(t, repo, validator)

	_, err := svc.CreateSkillDraft(context.Background(), draftInput(false))
	require.NoError(t, err)
	require.Zero(t, validator.call)
}

func TestPublishDraftRejectsUnknownWorkspaceBinding(t *testing.T) {
	repo := newFakeVersionRepo()
	seedSkillDraft(t, repo, domain.VersionStatusDraft, "s1", "rev-s1-draft")
	bound := repo.revisions["rev-s1-draft"]
	bound.Requirements = domain.Requirements{KnowledgeWorkspaceIDs: []string{"legal"}}
	bound.ActivationContract.Description = "g"
	bound.ActivationContract.InputSchema = map[string]any{"type": "object"}
	bound.ActivationContract.OutputSchema = map[string]any{"type": "object"} // satisfy ValidatePublishable first
	repo.revisions["rev-s1-draft"] = bound
	validator := &recordingBindingValidator{err: errSkillBindingRejected}
	svc := newBindingVersionService(t, repo, validator)

	// Publishing freezes bindings: an unknown workspace fails the publish.
	_, err := svc.PublishDraft(context.Background(), "s1", "u1")
	require.ErrorIs(t, err, errSkillBindingRejected)
}

func TestUpdateDraftBundleRejectsUnknownWorkspaceBinding(t *testing.T) {
	repo := newFakeVersionRepo()
	seedSkillDraft(t, repo, domain.VersionStatusDraft, "s1", "rev-s1-draft")
	validator := &recordingBindingValidator{err: errSkillBindingRejected}
	svc := newBindingVersionService(t, repo, validator)

	_, err := svc.UpdateDraftBundle(context.Background(), "s1", "", UpdateDraftBundleInput{
		Name: "s", Description: "g", Instructions: "do it",
		Requirements: domain.Requirements{KnowledgeWorkspaceIDs: []string{"legal"}},
		ActorID:      "u1",
	})
	require.ErrorIs(t, err, errSkillBindingRejected)
	require.Equal(t, [][]string{{"legal"}}, validator.got)
}

func TestUpdateInstructionBundleRejectsUnknownWorkspaceBinding(t *testing.T) {
	repo := newFakeVersionRepo()
	seedSkillDraft(t, repo, domain.VersionStatusDraft, "s1", "rev-s1-draft")
	validator := &recordingBindingValidator{err: errSkillBindingRejected}
	svc := newBindingVersionService(t, repo, validator)

	_, err := svc.UpdateInstructionBundle(context.Background(), "s1", UpdateInstructionBundleInput{
		Instructions: "do it",
		Requirements: domain.Requirements{KnowledgeWorkspaceIDs: []string{"legal"}},
		ActorID:      "u1",
	})
	require.ErrorIs(t, err, errSkillBindingRejected)
}
