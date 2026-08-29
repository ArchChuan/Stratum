package application

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	versioningdomain "github.com/byteBuilderX/stratum/internal/versioning/domain"
)

type stubVersionRepo struct {
	versions []versioningdomain.Version
	get      func(ctx context.Context, tenantID string, kind versioningdomain.ResourceKind, resourceID, versionID string) (versioningdomain.Version, bool, error)
}

func (s *stubVersionRepo) ListVersions(ctx context.Context, tenantID string, kind versioningdomain.ResourceKind, resourceID string) ([]versioningdomain.Version, error) {
	return s.versions, nil
}

func (s *stubVersionRepo) GetVersion(ctx context.Context, tenantID string, kind versioningdomain.ResourceKind, resourceID, versionID string) (versioningdomain.Version, bool, error) {
	if s.get != nil {
		return s.get(ctx, tenantID, kind, resourceID, versionID)
	}
	return versioningdomain.Version{}, false, nil
}

func TestWorkspaceServiceListWorkspaceVersions(t *testing.T) {
	repo := &fakeWorkspaceRepo{workspaces: map[string]*domain.Workspace{"kb": {ID: "ws-1", Name: "kb"}}}
	svc := NewWorkspaceService(repo, nil, zap.NewNop())
	svc.SetVersionRepo(&stubVersionRepo{versions: []versioningdomain.Version{{
		ID: "v2", ResourceKind: versioningdomain.ResourceKindKnowledge,
		Status: versioningdomain.VersionStatusDeprecated, Source: versioningdomain.VersionSourceManual,
		CreatedBy: "u1", SafeSummary: map[string]any{"name": "kb"},
	}}})
	svc.SetActorNameResolver(&stubNameResolver{names: map[string]string{"u1": "张三"}})

	got, err := svc.ListWorkspaceVersions(context.Background(), "tenant-1", "kb")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "v2", got[0].ID)
	require.Equal(t, "deprecated", got[0].Status)
	require.Equal(t, "张三", got[0].CreatedByName)
}

func TestWorkspaceServiceRollbackWorkspace(t *testing.T) {
	repo := &fakeWorkspaceRepo{workspaces: map[string]*domain.Workspace{
		"kb": {ID: "ws-1", Name: "kb", Description: "d", Config: domain.WorkspaceConfig{TopK: 8}},
	}}
	svc := NewWorkspaceService(repo, nil, zap.NewNop())
	// 回滚沿用更新矩阵（fail-closed）：未注入 role resolver 时 resolveUpdateActor
	// 拒绝一切写入（ErrForbidden），故成功路径测试注入 owner 角色。
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	svc.SetVersionRepo(&stubVersionRepo{get: func(ctx context.Context, tenantID string, kind versioningdomain.ResourceKind, resourceID, versionID string) (versioningdomain.Version, bool, error) {
		require.Equal(t, "ws-1", resourceID)
		return versioningdomain.Version{
			ID: "v1", Status: versioningdomain.VersionStatusDeprecated,
			Payload: domain.SnapshotFromWorkspace(&domain.Workspace{Name: "old", Description: "od", Config: domain.WorkspaceConfig{TopK: 4}}).Map(),
		}, true, nil
	}})

	ws, err := svc.RollbackWorkspace(context.Background(), "tenant-1", "kb", RollbackWorkspaceInput{ActorID: "u1", VersionID: "v1"})
	require.NoError(t, err)
	require.Equal(t, "old", ws.Name)
	require.Equal(t, "od", ws.Description)
	require.Equal(t, 4, ws.Config.TopK)
}

func TestWorkspaceServiceRollbackWorkspace_NonDeprecatedFailsClosed(t *testing.T) {
	repo := &fakeWorkspaceRepo{workspaces: map[string]*domain.Workspace{"kb": {ID: "ws-1", Name: "kb"}}}
	svc := NewWorkspaceService(repo, nil, zap.NewNop())
	svc.SetVersionRepo(&stubVersionRepo{get: func(ctx context.Context, tenantID string, kind versioningdomain.ResourceKind, resourceID, versionID string) (versioningdomain.Version, bool, error) {
		return versioningdomain.Version{ID: "v1", Status: versioningdomain.VersionStatusPublished}, true, nil
	}})

	_, err := svc.RollbackWorkspace(context.Background(), "tenant-1", "kb", RollbackWorkspaceInput{ActorID: "u1", VersionID: "v1"})
	require.ErrorIs(t, err, versioningdomain.ErrVersionNotFound)
}

type stubNameResolver struct {
	names map[string]string
}

func (s *stubNameResolver) ResolveActorNames(ctx context.Context, actorIDs []string) (map[string]string, error) {
	return s.names, nil
}
