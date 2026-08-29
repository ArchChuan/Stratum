package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	versioningdomain "github.com/byteBuilderX/stratum/internal/versioning/domain"
)

// versionHandlerWorkspaceRepo 满足 WorkspaceRepo port：GetByName/GetByID 返回
// 固定 workspace，其余 no-op。restored 非 nil 时 GetByID 返回回滚后的 workspace
// （模拟 repo 事务把版本快照写回行后重读的最新状态）。
type versionHandlerWorkspaceRepo struct {
	ws       *domain.Workspace
	restored *domain.Workspace
}

func (r *versionHandlerWorkspaceRepo) Create(
	context.Context, string, *domain.Workspace, []string, *auditdomain.ResourceChangeAuditEvent,
) error {
	return nil
}
func (r *versionHandlerWorkspaceRepo) GetByName(context.Context, string, string) (*domain.Workspace, error) {
	return r.ws, nil
}
func (r *versionHandlerWorkspaceRepo) GetByID(context.Context, string, string) (*domain.Workspace, error) {
	if r.restored != nil {
		return r.restored, nil
	}
	return r.ws, nil
}
func (r *versionHandlerWorkspaceRepo) List(context.Context, string) ([]*domain.Workspace, error) {
	return nil, nil
}
func (r *versionHandlerWorkspaceRepo) UpdateWorkspaceAll(
	context.Context, string, string, *string, *string, domain.KnowledgeWorkspaceSnapshot,
	string, string, *auditdomain.ResourceChangeAuditEvent,
) error {
	return nil
}
func (r *versionHandlerWorkspaceRepo) RollbackWorkspace(
	context.Context, string, string, domain.KnowledgeWorkspaceSnapshot, string, string,
	*auditdomain.ResourceChangeAuditEvent,
) error {
	return nil
}
func (r *versionHandlerWorkspaceRepo) Delete(
	context.Context, string, string, *auditdomain.ResourceChangeAuditEvent,
) error {
	return nil
}
func (r *versionHandlerWorkspaceRepo) GetConfigForUpload(context.Context, string, string) (domain.WorkspaceConfig, error) {
	return r.ws.Config, nil
}
func (r *versionHandlerWorkspaceRepo) GetConfigByID(context.Context, string, string) (domain.WorkspaceConfig, error) {
	return r.ws.Config, nil
}

// versionStubRepo 满足 versioningport.VersionRepo，行为与 application 包的
// stubVersionRepo 对齐（不同 package，各自独立定义）。
type versionStubRepo struct {
	versions []versioningdomain.Version
	get      func(ctx context.Context, tenantID string, kind versioningdomain.ResourceKind, resourceID, versionID string) (versioningdomain.Version, bool, error)
}

func (s *versionStubRepo) ListVersions(
	ctx context.Context, tenantID string, kind versioningdomain.ResourceKind, resourceID string,
) ([]versioningdomain.Version, error) {
	return s.versions, nil
}

func (s *versionStubRepo) GetVersion(
	ctx context.Context, tenantID string, kind versioningdomain.ResourceKind, resourceID, versionID string,
) (versioningdomain.Version, bool, error) {
	if s.get != nil {
		return s.get(ctx, tenantID, kind, resourceID, versionID)
	}
	return versioningdomain.Version{}, false, nil
}

func TestRAGHandlerListWorkspaceVersions(t *testing.T) {
	svc := knowledge.NewWorkspaceService(
		&versionHandlerWorkspaceRepo{ws: &domain.Workspace{ID: "ws-1", Name: "kb"}}, nil, zap.NewNop())
	svc.SetVersionRepo(&versionStubRepo{versions: []versioningdomain.Version{{
		ID: "v2", ResourceKind: versioningdomain.ResourceKindKnowledge,
		Status: versioningdomain.VersionStatusDeprecated, Source: versioningdomain.VersionSourceManual,
		RevisionNo: 2, CreatedBy: "u1", SafeSummary: map[string]any{"name": "kb"},
	}}})

	h := NewRAGHandler(nil, svc, zap.NewNop())
	r := newRouterWithErrorHandler()
	r.GET("/knowledge/workspaces/:name/versions", injectRAGTenant("tenant-1"), h.ListWorkspaceVersions)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/knowledge/workspaces/kb/versions", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body WorkspaceVersionsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Versions, 1)
	require.Equal(t, "v2", body.Versions[0].ID)
	require.Equal(t, 2, body.Versions[0].VersionNo)
}

func TestRAGHandlerRollbackWorkspace(t *testing.T) {
	ws := &domain.Workspace{ID: "ws-1", Name: "kb", Description: "d", Config: domain.WorkspaceConfig{TopK: 8}}
	repo := &versionHandlerWorkspaceRepo{
		ws: ws,
		// 回滚后 repo 事务把版本快照写回行，GetByID 重读返回恢复后的 workspace。
		restored: &domain.Workspace{ID: "ws-1", Name: "old", Description: "od", Config: domain.WorkspaceConfig{TopK: 4}},
	}
	svc := knowledge.NewWorkspaceService(repo, nil, zap.NewNop())
	// 回滚沿用更新矩阵（fail-closed）：注入 owner 角色使写入路径可达。
	svc.SetTenantRoleResolver(fixedTenantRole{role: "owner"})
	svc.SetVersionRepo(&versionStubRepo{get: func(ctx context.Context, tenantID string, kind versioningdomain.ResourceKind, resourceID, versionID string) (versioningdomain.Version, bool, error) {
		return versioningdomain.Version{ID: "v1", Status: versioningdomain.VersionStatusDeprecated,
			Payload: domain.SnapshotFromWorkspace(&domain.Workspace{Name: "old", Description: "od", Config: domain.WorkspaceConfig{TopK: 4}}).Map()}, true, nil
	}})

	h := NewRAGHandler(nil, svc, zap.NewNop())
	r := newRouterWithErrorHandler()
	r.POST("/knowledge/workspaces/:name/rollback", injectRAGTenant("tenant-1"), h.RollbackWorkspace)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge/workspaces/kb/rollback", strings.NewReader(`{"versionId":"v1"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "old", body["name"])
}
