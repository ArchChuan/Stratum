package handler

// SetDocumentAccess handler tests (P0.6 access 接口):missing tenant/user fail
// closed,bind 错误 400,workspace 错误 404,权限拒绝 403,成功回显白名单。

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	knowledgedomain "github.com/byteBuilderX/stratum/internal/knowledge/domain"
)

// accessHandlerWorkspaceRepo 返回固定 workspace;err 非空时 GetByName/GetByID
// 一律返回该错误。
type accessHandlerWorkspaceRepo struct {
	ws  *knowledgedomain.Workspace
	err error
}

func (r *accessHandlerWorkspaceRepo) Create(
	context.Context, string, *knowledgedomain.Workspace, []string,
	*auditdomain.ResourceChangeAuditEvent,
) error {
	return nil
}
func (r *accessHandlerWorkspaceRepo) GetByName(context.Context, string, string) (*knowledgedomain.Workspace, error) {
	return r.ws, r.err
}
func (r *accessHandlerWorkspaceRepo) GetByID(context.Context, string, string) (*knowledgedomain.Workspace, error) {
	return r.ws, r.err
}
func (r *accessHandlerWorkspaceRepo) List(context.Context, string) ([]*knowledgedomain.Workspace, error) {
	return []*knowledgedomain.Workspace{r.ws}, nil
}
func (r *accessHandlerWorkspaceRepo) UpdateWorkspaceAll(
	context.Context, string, string, *string, *string, knowledgedomain.WorkspaceConfig,
	string, *auditdomain.ResourceChangeAuditEvent,
) error {
	return nil
}
func (r *accessHandlerWorkspaceRepo) Delete(
	context.Context, string, string, *auditdomain.ResourceChangeAuditEvent,
) error {
	return nil
}
func (r *accessHandlerWorkspaceRepo) GetConfigForUpload(
	context.Context, string, string,
) (knowledgedomain.WorkspaceConfig, error) {
	return r.ws.Config, nil
}
func (r *accessHandlerWorkspaceRepo) GetConfigByID(
	context.Context, string, string,
) (knowledgedomain.WorkspaceConfig, error) {
	return r.ws.Config, nil
}

// accessHandlerDocRepo 的 GetByID 返回 docs 中匹配文档,SetDocAccess 记录入参,
// 其余方法 no-op。
type accessHandlerDocRepo struct {
	docs     []*knowledgedomain.Document
	gotUsers []string
	gotRoles []string
}

func (r *accessHandlerDocRepo) Save(context.Context, string, string, *knowledgedomain.Document) error {
	return nil
}
func (r *accessHandlerDocRepo) List(context.Context, string, string) ([]*knowledgedomain.Document, error) {
	return r.docs, nil
}
func (r *accessHandlerDocRepo) Delete(context.Context, string, string, string) error { return nil }
func (r *accessHandlerDocRepo) ExistsByHash(context.Context, string, string, string) (bool, error) {
	return false, nil
}
func (r *accessHandlerDocRepo) CountByWorkspace(context.Context, string, string) (int, error) {
	return len(r.docs), nil
}
func (r *accessHandlerDocRepo) MarkIngestStarted(context.Context, string, string, int) error {
	return nil
}
func (r *accessHandlerDocRepo) MarkIngestCompleted(context.Context, string, string, int) error {
	return nil
}
func (r *accessHandlerDocRepo) MarkIngestFailed(context.Context, string, string, string) error {
	return nil
}
func (r *accessHandlerDocRepo) RecoverStuckIngests(context.Context, string, time.Duration) (int, error) {
	return 0, nil
}
func (r *accessHandlerDocRepo) VisibleDocIDs(context.Context, string, string, string, string) ([]string, error) {
	return nil, nil
}
func (r *accessHandlerDocRepo) GetByID(_ context.Context, _, _, docID string) (*knowledgedomain.Document, error) {
	for _, d := range r.docs {
		if d.ID == docID {
			return d, nil
		}
	}
	return nil, knowledgedomain.ErrDocumentNotFound
}
func (r *accessHandlerDocRepo) SetDocAccess(_ context.Context, _, _ string, users, roles []string) error {
	r.gotUsers = append([]string(nil), users...)
	r.gotRoles = append([]string(nil), roles...)
	return nil
}

// newAccessRAGHandler 装配访问接口用例的 handler:固定 workspace + 固定角色,
// 只有 member 时视为非授权身份。
func newAccessRAGHandler(
	ws *knowledgedomain.Workspace, wsErr error, docs *accessHandlerDocRepo, role string,
) *RAGHandler {
	svc := knowledge.NewWorkspaceService(&accessHandlerWorkspaceRepo{ws: ws, err: wsErr}, nil, zap.NewNop())
	svc.SetDocRepo(docs)
	svc.SetTenantRoleResolver(fixedTenantRole{role: role})
	return NewRAGHandler(nil, svc, zap.NewNop())
}

// newAccessRouter 注入 tenant+user 上下文并挂统一错误映射,用于错误映射/成功用例。
func newAccessRouter(h *RAGHandler) *gin.Engine {
	r := newRouterWithErrorHandler()
	r.Use(injectRAGTenant("test-tenant-id"))
	return r
}

func performAccessRequest(r http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut,
		"/knowledge/workspaces/docs/documents/d1/access", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestSetDocumentAccess_MissingTenant(t *testing.T) {
	h := newMinimalRAGHandler()
	r := newRouterWithErrorHandler()
	r.PUT("/knowledge/workspaces/:name/documents/:documentID/access", h.SetDocumentAccess)

	rec := performAccessRequest(r, `{"workspace":"docs","document_id":"d1"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

func TestSetDocumentAccess_MissingUser(t *testing.T) {
	// setupRAGRouter 只注入 tenant,不注入 user —— 必须 fail closed。
	h := newAccessRAGHandler(&knowledgedomain.Workspace{ID: "ws-1", Name: "docs"}, nil,
		&accessHandlerDocRepo{docs: []*knowledgedomain.Document{{ID: "d1"}}}, "owner")
	r := setupRAGRouter(h)
	r.PUT("/knowledge/workspaces/:name/documents/:documentID/access", h.SetDocumentAccess)

	rec := performAccessRequest(r, `{"workspace":"docs","document_id":"d1"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

func TestSetDocumentAccess_BindError(t *testing.T) {
	h := newAccessRAGHandler(&knowledgedomain.Workspace{ID: "ws-1", Name: "docs"}, nil,
		&accessHandlerDocRepo{docs: []*knowledgedomain.Document{{ID: "d1"}}}, "owner")
	r := setupRAGRouter(h)
	r.PUT("/knowledge/workspaces/:name/documents/:documentID/access", h.SetDocumentAccess)

	rec := performAccessRequest(r, `{"workspace":`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestSetDocumentAccess_ErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		ws       *knowledgedomain.Workspace
		wsErr    error
		wantCode int
	}{
		{
			name:     "workspace lookup failure maps to 404",
			ws:       nil,
			wsErr:    knowledgedomain.ErrWorkspaceNotFound,
			wantCode: http.StatusNotFound,
		},
		{
			name:     "non-owner member maps to 403",
			ws:       &knowledgedomain.Workspace{ID: "ws-1", Name: "docs", CreatedBy: "someone-else"},
			wantCode: http.StatusForbidden,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			role := "member"
			h := newAccessRAGHandler(tc.ws, tc.wsErr, &accessHandlerDocRepo{docs: []*knowledgedomain.Document{{ID: "d1"}}}, role)
			r := newAccessRouter(h)
			r.PUT("/knowledge/workspaces/:name/documents/:documentID/access", h.SetDocumentAccess)

			rec := performAccessRequest(r, `{"workspace":"docs","document_id":"d1","allowed_user_ids":["u1"]}`)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

func TestSetDocumentAccess_Success(t *testing.T) {
	docs := &accessHandlerDocRepo{docs: []*knowledgedomain.Document{{ID: "d1"}}}
	h := newAccessRAGHandler(&knowledgedomain.Workspace{ID: "ws-1", Name: "docs"},
		nil, docs, "owner")
	r := newAccessRouter(h)
	r.PUT("/knowledge/workspaces/:name/documents/:documentID/access", h.SetDocumentAccess)

	rec := performAccessRequest(r, `{"workspace":"docs","document_id":"d1",
		"allowed_user_ids":["u1"],"allowed_role_ids":["ADMIN","member"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp genAccessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode = %v: %s", err, rec.Body.String())
	}
	if len(resp.AllowedUserIDs) != 1 || resp.AllowedUserIDs[0] != "u1" {
		t.Fatalf("allowed_user_ids = %v", resp.AllowedUserIDs)
	}
	if len(resp.AllowedRoleIDs) != 2 {
		t.Fatalf("allowed_role_ids = %v", resp.AllowedRoleIDs)
	}
	// 服务层归一化后落库,handler 只回显请求体。
	if docs.gotUsers == nil || !slicesEqual(docs.gotUsers, []string{"u1"}) {
		t.Fatalf("repo users = %v", docs.gotUsers)
	}
}

type genAccessResponse struct {
	AllowedUserIDs []string `json:"allowed_user_ids"`
	AllowedRoleIDs []string `json:"allowed_role_ids"`
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSetDocumentAccess_MissingDocumentParam(t *testing.T) {
	h := newAccessRAGHandler(&knowledgedomain.Workspace{ID: "ws-1", Name: "docs"}, nil,
		&accessHandlerDocRepo{docs: []*knowledgedomain.Document{{ID: "d1"}}}, "owner")
	r := setupRAGRouter(h)
	r.PUT("/knowledge/workspaces/:name/documents/:documentID/access", h.SetDocumentAccess)

	req := httptest.NewRequest(http.MethodPut, "/knowledge/workspaces/docs/documents//access",
		bytes.NewBufferString(`{"workspace":"docs","document_id":"d1"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}
