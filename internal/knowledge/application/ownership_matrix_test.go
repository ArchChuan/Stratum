package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/stretchr/testify/require"
)

// failRoleResolver 使租户角色解析失败，验证授权 fail-closed。
type failRoleResolver struct{ err error }

func (f failRoleResolver) ResolveTenantRole(context.Context, string, string) (string, error) {
	return "", f.err
}

func TestWorkspaceUpdateOwnershipMatrix(t *testing.T) {
	t.Parallel()

	const actor = "user-1"

	cases := []struct {
		name      string
		createdBy string
		role      string
		resolver  interface {
			ResolveTenantRole(context.Context, string, string) (string, error)
		}
		emptyActor bool
		wantErr    bool
	}{
		{name: "owner updates others resource", createdBy: "other-user", role: "owner"},
		{name: "owner updates unowned resource", createdBy: "", role: "owner"},
		{name: "admin updates own resource", createdBy: actor, role: "admin"},
		{name: "admin updates others resource", createdBy: "other-user", role: "admin"},
		{name: "admin updates unowned resource", createdBy: "", role: "admin"},
		{name: "member updates own resource", createdBy: actor, role: "member", wantErr: true},
		{name: "role resolution failure fails closed", createdBy: actor, resolver: failRoleResolver{err: errors.New("upstream down")}, wantErr: true},
		{name: "nil resolver fails closed", createdBy: actor, wantErr: true},
		{name: "empty actor fails closed", createdBy: actor, role: "owner", emptyActor: true, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := newFakeWorkspaceRepo()
			ws := seedWorkspace(repo, "ws1")
			ws.CreatedBy = tc.createdBy
			svc, _ := buildWorkspaceService(repo)
			switch {
			case tc.resolver != nil:
				svc.SetTenantRoleResolver(tc.resolver)
			case tc.role != "":
				svc.SetTenantRoleResolver(stubTenantRole{role: tc.role})
			default:
				// buildWorkspaceService 预置 owner；nil resolver 行须显式清空。
				svc.SetTenantRoleResolver(nil)
			}
			actorID := actor
			if tc.emptyActor {
				actorID = ""
			}
			desc := "new desc"

			_, err := svc.UpdateWorkspace(context.Background(), "t1", "ws1",
				UpdateWorkspaceInput{Description: &desc}, actorID)

			if tc.wantErr {
				require.ErrorIs(t, err, domain.ErrForbidden)
				require.Empty(t, repo.audits, "forbidden update must not persist audit")
			} else {
				require.NoError(t, err)
				require.Len(t, repo.audits, 1)
			}
		})
	}
}

func TestWorkspaceUpdateAuditEvent(t *testing.T) {
	t.Parallel()
	repo := newFakeWorkspaceRepo()
	ws := seedWorkspace(repo, "ws1")
	ws.CreatedBy = "user-1"
	svc, _ := buildWorkspaceService(repo)

	desc := "new desc"
	_, err := svc.UpdateWorkspace(context.Background(), "t1", "ws1",
		UpdateWorkspaceInput{Description: &desc}, "user-1")
	require.NoError(t, err)

	require.Len(t, repo.audits, 1)
	ev := repo.audits[0]
	require.Equal(t, auditdomain.ResourceKindKnowledge, ev.ResourceKind)
	require.Equal(t, "wsid-ws1", ev.ResourceID)
	require.Equal(t, auditdomain.ChangeOpUpdate, ev.Operation)
	require.Equal(t, "user-1", ev.ActorID)
	require.Equal(t, auditdomain.ChangeActorUser, ev.ActorType)
	require.Equal(t, auditdomain.ChangeSourceAPI, ev.Source)
	require.Empty(t, ev.ProposalID)
	// before/after 为投影：before 保留旧描述，after 含新描述。
	require.Contains(t, string(ev.Before), "desc")
	require.Contains(t, string(ev.After), "new desc")
}

func TestWorkspaceCreateAuditsCreate(t *testing.T) {
	t.Parallel()
	repo := newFakeWorkspaceRepo()
	svc, _ := buildWorkspaceService(repo)

	ws, err := svc.CreateWorkspace(context.Background(), "t1", CreateWorkspaceInput{
		Name:        "ws1",
		Description: "d",
		Config:      domain.WorkspaceConfig{EmbeddingModel: "text-embedding-v3"},
	}, "user-1")
	require.NoError(t, err)
	require.Equal(t, "user-1", ws.CreatedBy)

	require.Len(t, repo.audits, 1)
	ev := repo.audits[0]
	require.Equal(t, auditdomain.ResourceKindKnowledge, ev.ResourceKind)
	require.Equal(t, auditdomain.ChangeOpCreate, ev.Operation)
	require.Equal(t, "user-1", ev.ActorID)
	require.Equal(t, auditdomain.ChangeActorUser, ev.ActorType)
	require.Equal(t, auditdomain.ChangeSourceAPI, ev.Source)
	// create 无前态：应用层 Before 为空，持久化层默认落成 {}。
	require.Empty(t, ev.Before)
	require.Contains(t, string(ev.After), "ws1")
}

func TestWorkspaceUpdatePropagatesRepoError(t *testing.T) {
	t.Parallel()
	repo := newFakeWorkspaceRepo()
	seedWorkspace(repo, "ws1")
	repo.updateErr = errors.New("db down")
	svc, _ := buildWorkspaceService(repo)

	desc := "x"
	_, err := svc.UpdateWorkspace(context.Background(), "t1", "ws1",
		UpdateWorkspaceInput{Description: &desc}, "user-1")

	// 持久化失败必须向上传播，不得吞掉（fail closed）。
	require.ErrorContains(t, err, "db down")
}

func TestWorkspaceUpdateSystemActorBypassesOwnership(t *testing.T) {
	t.Parallel()
	repo := newFakeWorkspaceRepo()
	ws := seedWorkspace(repo, "ws1")
	ws.CreatedBy = "other-user"
	svc, _ := buildWorkspaceService(repo)
	// member 本应被拒绝；system actor 跳过归属校验但仍落审计。
	svc.SetTenantRoleResolver(stubTenantRole{role: "member"})

	ctx := reqctx.WithSystemActor(context.Background(), "evaluation-worker")
	desc := "new desc"
	_, err := svc.UpdateWorkspace(ctx, "t1", "ws1",
		UpdateWorkspaceInput{Description: &desc}, "user-1")
	require.NoError(t, err)

	require.Len(t, repo.audits, 1)
	ev := repo.audits[0]
	require.Equal(t, "evaluation-worker", ev.ActorID)
	require.Equal(t, auditdomain.ChangeActorSystem, ev.ActorType)
	require.Equal(t, auditdomain.ChangeSourceOptimization, ev.Source)
}

// stubKnowledgeEditorRepo is an in-memory ResourceEditorRepo for
// editor-granted ownership tests. It records the last replace so tests can
// assert the management-endpoint contract.
type stubKnowledgeEditorRepo struct {
	editors      map[string][]string
	replaceErr   error
	replaced     []string
	replaceActor string
	audits       []*auditdomain.ResourceChangeAuditEvent
}

func newStubKnowledgeEditorRepo() *stubKnowledgeEditorRepo {
	return &stubKnowledgeEditorRepo{editors: map[string][]string{}}
}

func (s *stubKnowledgeEditorRepo) ListEditors(_ context.Context, _, resourceID string) ([]string, error) {
	return append([]string(nil), s.editors[resourceID]...), nil
}

func (s *stubKnowledgeEditorRepo) ReplaceEditors(_ context.Context, _, resourceID string, editorIDs []string, createdBy string, audit *auditdomain.ResourceChangeAuditEvent) error {
	if s.replaceErr != nil {
		return s.replaceErr
	}
	s.editors[resourceID] = append([]string(nil), editorIDs...)
	s.replaced = editorIDs
	s.replaceActor = createdBy
	if audit != nil {
		s.audits = append(s.audits, audit)
	}
	return nil
}

var _ port.ResourceEditorRepo = (*stubKnowledgeEditorRepo)(nil)

// TestWorkspaceUpdateEditorGranted pins the granted-editor row of the matrix:
// a foreign admin in the editor set may update the workspace (update-only).
func TestWorkspaceUpdateEditorGranted(t *testing.T) {
	t.Parallel()

	repo := newFakeWorkspaceRepo()
	ws := seedWorkspace(repo, "ws1")
	ws.CreatedBy = "owner-user"
	editors := newStubKnowledgeEditorRepo()
	editors.editors[ws.ID] = []string{"user-1"}
	svc, _ := buildWorkspaceService(repo)
	svc.SetTenantRoleResolver(stubTenantRole{role: "admin"})
	svc.SetEditorRepo(editors)
	desc := "new desc"

	_, err := svc.UpdateWorkspace(context.Background(), "t1", "ws1",
		UpdateWorkspaceInput{Description: &desc}, "user-1")
	require.NoError(t, err)
	require.Len(t, repo.audits, 1)
}

// TestWorkspaceDeleteEditorDenied pins the delete column: editors never grant
// delete rights — the creator passes, a granted editor is denied.
func TestWorkspaceDeleteEditorDenied(t *testing.T) {
	t.Parallel()

	repo := newFakeWorkspaceRepo()
	ws := seedWorkspace(repo, "ws1")
	ws.CreatedBy = "user-1"
	editors := newStubKnowledgeEditorRepo()
	editors.editors[ws.ID] = []string{"editor-1"}
	svc, _ := buildWorkspaceService(repo)
	svc.SetTenantRoleResolver(stubTenantRole{role: "admin"})
	svc.SetEditorRepo(editors)
	ctx := reqctx.WithTenantID(context.Background(), "t1")

	err := svc.DeleteWorkspace(ctx, "t1", "ws1", "editor-1")
	require.ErrorIs(t, err, domain.ErrForbidden)
	_, ok := repo.workspaces["ws1"]
	require.True(t, ok, "editor must not delete the workspace")

	require.NoError(t, svc.DeleteWorkspace(ctx, "t1", "ws1", "user-1"))
	_, ok = repo.workspaces["ws1"]
	require.False(t, ok, "creator deletes their own workspace")
}

// TestWorkspaceSetEditorsPinsManagementEndpoint covers the editor management
// endpoint: creator/owner may replace the set with an audited before/after
// projection; a granted editor cannot delegate their own right.
func TestWorkspaceSetEditorsPinsManagementEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("owner replaces editor set", func(t *testing.T) {
		t.Parallel()
		repo := newFakeWorkspaceRepo()
		ws := seedWorkspace(repo, "ws1")
		ws.CreatedBy = "creator-1"
		editors := newStubKnowledgeEditorRepo()
		editors.editors[ws.ID] = []string{"old-editor"}
		svc, _ := buildWorkspaceService(repo)
		svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
		svc.SetEditorRepo(editors)
		ctx := reqctx.WithTenantID(context.Background(), "t1")

		require.NoError(t, svc.SetEditors(ctx, "t1", "ws1", []string{"editor-a", "editor-b"}, "owner-1"))
		require.Equal(t, []string{"editor-a", "editor-b"}, editors.replaced)
		require.Equal(t, "owner-1", editors.replaceActor)
		require.Len(t, editors.audits, 1)

		var before, after map[string]any
		require.NoError(t, json.Unmarshal(editors.audits[0].Before, &before))
		require.NoError(t, json.Unmarshal(editors.audits[0].After, &after))
		require.Equal(t, []any{"old-editor"}, before["editors"])
		require.Equal(t, []any{"editor-a", "editor-b"}, after["editors"])
	})

	t.Run("granted editor cannot delegate", func(t *testing.T) {
		t.Parallel()
		repo := newFakeWorkspaceRepo()
		ws := seedWorkspace(repo, "ws1")
		ws.CreatedBy = "creator-1"
		editors := newStubKnowledgeEditorRepo()
		editors.editors[ws.ID] = []string{"editor-1"}
		svc, _ := buildWorkspaceService(repo)
		svc.SetTenantRoleResolver(stubTenantRole{role: "member"})
		svc.SetEditorRepo(editors)
		ctx := reqctx.WithTenantID(context.Background(), "t1")

		err := svc.SetEditors(ctx, "t1", "ws1", []string{"someone-else"}, "editor-1")
		require.ErrorIs(t, err, domain.ErrForbidden)
		require.Nil(t, editors.replaced, "denied replace must not reach the repository")
	})
}

// TestWorkspaceListEditorsWrapper pins the detail prefill path: the service
// delegates straight to the editor repo, skipping when not wired.
func TestWorkspaceListEditorsWrapper(t *testing.T) {
	t.Parallel()

	repo := newFakeWorkspaceRepo()
	ws := seedWorkspace(repo, "ws1")
	editors := newStubKnowledgeEditorRepo()
	editors.editors[ws.ID] = []string{"editor-a"}
	svc, _ := buildWorkspaceService(repo)
	svc.SetEditorRepo(editors)

	got, err := svc.ListEditors(context.Background(), "t1", ws.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"editor-a"}, got)

	svcNoRepo, _ := buildWorkspaceService(repo)
	got, err = svcNoRepo.ListEditors(context.Background(), "t1", ws.ID)
	require.NoError(t, err)
	require.Empty(t, got)
}
