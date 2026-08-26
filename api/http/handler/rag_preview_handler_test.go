package handler

// PreviewDocument handler tests (P1.3 预览端点):missing tenant/user fail
// closed,不可见统一 404,成功重组分块 + parent content。

import (
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
	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
)

// previewChunkRepo serves ListByDoc/GetParentByID for preview handler tests.
type previewChunkRepo struct {
	chunks []knowledgedomain.Chunk
	parent *knowledgeport.ParentChunk
}

func (r *previewChunkRepo) InsertBatch(context.Context, string, string, []knowledgedomain.Chunk) error {
	return nil
}
func (r *previewChunkRepo) InsertParentBatch(context.Context, string, string, []knowledgeport.ParentChunk) error {
	return nil
}
func (r *previewChunkRepo) GetParentByID(context.Context, string, string, string) (*knowledgeport.ParentChunk, error) {
	return r.parent, nil
}
func (r *previewChunkRepo) GetChunksByIDs(context.Context, string, string, []string) ([]knowledgedomain.Chunk, error) {
	return nil, nil
}
func (r *previewChunkRepo) KeywordSearch(context.Context, string, string, string, []string, int) ([]knowledgedomain.Chunk, error) {
	return nil, nil
}
func (r *previewChunkRepo) ListByDoc(context.Context, string, string, string) ([]knowledgedomain.Chunk, error) {
	return r.chunks, nil
}
func (r *previewChunkRepo) CountByWorkspace(context.Context, string, string) (int64, error) {
	return 0, nil
}
func (r *previewChunkRepo) DeleteByWorkspace(context.Context, string, string) error { return nil }

// previewDocRepo returns doc from GetByID and the whitelist from visible.
type previewDocRepo struct {
	doc     *knowledgedomain.Document
	visible []string
}

func (r *previewDocRepo) Save(context.Context, string, string, *knowledgedomain.Document) (bool, error) {
	return true, nil
}
func (r *previewDocRepo) List(context.Context, string, string) ([]*knowledgedomain.Document, error) {
	return nil, nil
}
func (r *previewDocRepo) Delete(context.Context, string, string, string) error { return nil }
func (r *previewDocRepo) ExistsByHash(context.Context, string, string, string) (bool, error) {
	return false, nil
}
func (r *previewDocRepo) CountByWorkspace(context.Context, string, string) (int, error) {
	return 0, nil
}
func (r *previewDocRepo) MarkIngestStarted(context.Context, string, string, int) error { return nil }
func (r *previewDocRepo) MarkIngestCompleted(context.Context, string, string, int) error {
	return nil
}
func (r *previewDocRepo) MarkIngestFailed(context.Context, string, string, string) error { return nil }
func (r *previewDocRepo) RecoverStuckIngests(context.Context, string, time.Duration) (int, error) {
	return 0, nil
}
func (r *previewDocRepo) VisibleDocIDs(context.Context, string, string, string, string) ([]string, error) {
	return r.visible, nil
}
func (r *previewDocRepo) GetByID(context.Context, string, string, string) (*knowledgedomain.Document, error) {
	return r.doc, nil
}
func (r *previewDocRepo) SetDocAccess(context.Context, string, string, []string, []string) error {
	return nil
}

// previewWorkspaceRepo returns a fixed workspace (member-created) for GetByName.
type previewWorkspaceRepo struct {
	ws *knowledgedomain.Workspace
}

func (r *previewWorkspaceRepo) Create(
	context.Context, string, *knowledgedomain.Workspace, []string,
	*auditdomain.ResourceChangeAuditEvent,
) error {
	return nil
}
func (r *previewWorkspaceRepo) GetByName(context.Context, string, string) (*knowledgedomain.Workspace, error) {
	return r.ws, nil
}
func (r *previewWorkspaceRepo) GetByID(context.Context, string, string) (*knowledgedomain.Workspace, error) {
	return r.ws, nil
}
func (r *previewWorkspaceRepo) List(context.Context, string) ([]*knowledgedomain.Workspace, error) {
	return []*knowledgedomain.Workspace{r.ws}, nil
}
func (r *previewWorkspaceRepo) UpdateWorkspaceAll(
	context.Context, string, string, *string, *string, knowledgedomain.WorkspaceConfig,
	string, *auditdomain.ResourceChangeAuditEvent,
) error {
	return nil
}
func (r *previewWorkspaceRepo) Delete(
	context.Context, string, string, *auditdomain.ResourceChangeAuditEvent,
) error {
	return nil
}
func (r *previewWorkspaceRepo) GetConfigForUpload(
	context.Context, string, string,
) (knowledgedomain.WorkspaceConfig, error) {
	return knowledgedomain.WorkspaceConfig{}, nil
}
func (r *previewWorkspaceRepo) GetConfigByID(
	context.Context, string, string,
) (knowledgedomain.WorkspaceConfig, error) {
	return knowledgedomain.WorkspaceConfig{}, nil
}

// newPreviewRAGHandler assembles a RAGHandler whose RAGService serves previews.
func newPreviewRAGHandler(
	ws *knowledgedomain.Workspace, docs *previewDocRepo, chunks *previewChunkRepo,
) *RAGHandler {
	svc := knowledge.NewRAGService(nil, nil, zap.NewNop())
	svc.SetWorkspaceRepo(&previewWorkspaceRepo{ws: ws})
	svc.SetTenantRoleResolver(fixedTenantRole{role: "member"})
	svc.SetDocRepo(docs)
	svc.SetChunkRepo(chunks)
	return NewRAGHandler(svc, nil, zap.NewNop())
}

func previewRouter(h *RAGHandler) *gin.Engine {
	r := newAccessRouter(h)
	r.GET("/knowledge/workspaces/:name/documents/:documentID/preview", h.PreviewDocument)
	return r
}

func performPreview(r http.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/knowledge/workspaces/docs/documents/d1/preview", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestPreviewDocument_MissingTenant(t *testing.T) {
	// Bare router without tenant injection: tenantIDFromCtx must fail closed.
	h := newMinimalRAGHandler()
	r := newRouterWithErrorHandler()
	r.GET("/knowledge/workspaces/:name/documents/:documentID/preview", h.PreviewDocument)
	if rec := performPreview(r); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

func TestPreviewDocument_MissingUser(t *testing.T) {
	// setupRAGRouter only injects the tenant, not the user — fail closed.
	ws := &knowledgedomain.Workspace{ID: "ws-1", Name: "docs", CreatedBy: "other-user"}
	h := newPreviewRAGHandler(ws,
		&previewDocRepo{doc: &knowledgedomain.Document{ID: "d1"}, visible: []string{"d1"}},
		&previewChunkRepo{})
	r := setupRAGRouter(h)
	r.GET("/knowledge/workspaces/:name/documents/:documentID/preview", h.PreviewDocument)

	if rec := performPreview(r); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

func TestPreviewDocument_InvisibleMapsToNotFound(t *testing.T) {
	// Whitelist does not include d1: 404, indistinguishable from missing.
	ws := &knowledgedomain.Workspace{ID: "ws-1", Name: "docs", CreatedBy: "other-user"}
	h := newPreviewRAGHandler(ws,
		&previewDocRepo{doc: &knowledgedomain.Document{ID: "d1"}, visible: []string{"d-other"}},
		&previewChunkRepo{})
	r := previewRouter(h)

	if rec := performPreview(r); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestPreviewDocument_SuccessReassemblesSegments(t *testing.T) {
	ws := &knowledgedomain.Workspace{ID: "ws-1", Name: "docs", CreatedBy: "other-user"}
	h := newPreviewRAGHandler(ws,
		&previewDocRepo{
			doc:     &knowledgedomain.Document{ID: "d1", Source: "annual-report.pdf"},
			visible: []string{"d1"},
		},
		&previewChunkRepo{
			chunks: []knowledgedomain.Chunk{
				{ID: "c1", DocID: "d1", Index: 0, Text: "leaf-0"},
				{ID: "c2", DocID: "d1", Index: 1, Text: "leaf-1", ParentID: "p1"},
			},
			parent: &knowledgeport.ParentChunk{ID: "p1", Content: "parent-context"},
		})
	r := previewRouter(h)

	rec := performPreview(r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Workspace     string `json:"workspace"`
		DocumentID    string `json:"document_id"`
		DocumentTitle string `json:"document_title"`
		ChunkCount    int    `json:"chunk_count"`
		Segments      []struct {
			ChunkID       string `json:"chunk_id"`
			Index         int64  `json:"index"`
			Content       string `json:"content"`
			ParentContent string `json:"parent_content"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Workspace != "docs" || body.DocumentID != "d1" || body.DocumentTitle != "annual-report.pdf" {
		t.Fatalf("identity fields wrong: %+v", body)
	}
	if body.ChunkCount != 2 || len(body.Segments) != 2 {
		t.Fatalf("expected 2 segments, got count=%d len=%d", body.ChunkCount, len(body.Segments))
	}
	if body.Segments[0].ChunkID != "c1" || body.Segments[1].Content != "leaf-1" {
		t.Fatalf("segments out of order: %+v", body.Segments)
	}
	if body.Segments[1].ParentContent != "parent-context" {
		t.Fatalf("parent content not attached: %+v", body.Segments[1])
	}
}
