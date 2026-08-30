package application_test

import (
	"context"
	"testing"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/workflow/application"
	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/stretchr/testify/require"
)

// memoryEditorRepo 内存实现 ResourceEditorRepo，供 service 测试注入。
type memoryEditorRepo struct {
	editors map[string][]string
}

func newMemoryEditorRepo(initial map[string][]string) *memoryEditorRepo {
	if initial == nil {
		initial = map[string][]string{}
	}
	return &memoryEditorRepo{editors: initial}
}

func (r *memoryEditorRepo) ListEditors(_ context.Context, _, resourceID string) ([]string, error) {
	return append([]string(nil), r.editors[resourceID]...), nil
}

func (r *memoryEditorRepo) ReplaceEditors(_ context.Context, _, resourceID string, editorIDs []string, _ string, _ *auditdomain.ResourceChangeAuditEvent) error {
	r.editors[resourceID] = append([]string(nil), editorIDs...)
	return nil
}

func TestWorkflowServiceSetAndListEditors(t *testing.T) {
	ctx := context.Background()
	store, idgen := newMemoryStore(), &ids{}
	editorRepo := newMemoryEditorRepo(nil)
	svc := application.NewDefinitionService(store, store, idgen.NewID)
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	svc.SetEditorRepo(editorRepo)

	def, err := svc.Create(ctx, "t1", application.CreateDefinitionCommand{Name: "Research", Spec: workflowSpec()}, "u-1")
	require.NoError(t, err)

	// owner 可设白名单。
	require.NoError(t, svc.SetEditors(ctx, "t1", def.ID, []string{"m-1", "m-2"}, "owner-1"))
	editors, err := svc.ListEditors(ctx, "t1", def.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"m-1", "m-2"}, editors)

	// Get 响应附带 editors。
	got, err := svc.Get(ctx, "t1", def.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"m-1", "m-2"}, got.Editors)

	// member 管理白名单 → 403。
	svc.SetTenantRoleResolver(stubTenantRole{role: "member"})
	err = svc.SetEditors(ctx, "t1", def.ID, []string{"m-3"}, "m-1")
	require.ErrorIs(t, err, domain.ErrForbidden)
}

func TestWorkflowServiceUpdateRequiresOwnership(t *testing.T) {
	store, idgen := newMemoryStore(), &ids{}
	svc := application.NewDefinitionService(store, store, idgen.NewID)
	svc.SetTenantRoleResolver(stubTenantRole{role: "member"})
	svc.SetEditorRepo(newMemoryEditorRepo(map[string][]string{"d1": {"u-1"}}))
	def, err := svc.Create(context.Background(), "t1", application.CreateDefinitionCommand{Name: "Research", Spec: workflowSpec()}, "u-1")
	require.NoError(t, err)

	// 非白名单成员更新 → 403；owner/admin 角色则放行。
	_, err = svc.Update(context.Background(), "t1", def.ID, application.UpdateDefinitionCommand{Name: "X", ExpectedRevision: def.Revision}, "m-9")
	require.ErrorIs(t, err, domain.ErrForbidden)
}

func TestWorkflowServiceCreateWritesCreator(t *testing.T) {
	store, idgen := newMemoryStore(), &ids{}
	svc := newOwnerDefinitionService(store, idgen.NewID)
	def, err := svc.Create(context.Background(), "t1", application.CreateDefinitionCommand{Name: "Research", Spec: workflowSpec()}, "u-1")
	require.NoError(t, err)
	require.Equal(t, "u-1", def.CreatedBy)
}

// TestWorkflowServiceMemberUpdatePassesEditorActor pins the whitelist-member
// core path: a granted member's Update succeeds and forwards the non-empty
// editorActor into the write transaction so the store re-validates the grant
// (TOCTOU closed at the storage layer).
func TestWorkflowServiceMemberUpdatePassesEditorActor(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	svc := application.NewDefinitionService(store, store, func() string { return "d1" })
	svc.SetTenantRoleResolver(stubTenantRole{role: "member"})
	svc.SetEditorRepo(newMemoryEditorRepo(map[string][]string{"d1": {"m-1"}}))
	def, err := svc.Create(ctx, "t1", application.CreateDefinitionCommand{Name: "Research", Spec: workflowSpec()}, "u-1")
	require.NoError(t, err)
	require.Equal(t, "d1", def.ID)

	updated, err := svc.Update(ctx, "t1", def.ID, application.UpdateDefinitionCommand{Name: "Renamed", ExpectedRevision: def.Revision}, "m-1")
	require.NoError(t, err)
	require.Equal(t, "Renamed", updated.Name)
	require.Equal(t, "m-1", store.lastEditorActor)
}
