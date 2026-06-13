package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// injectRAGTenant sets a tenant context for RAG handler tests.
func injectRAGTenant(tenantID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tc := &tenantdb.TenantContext{TenantID: tenantID, UserID: "user-test", Role: tenantdb.RoleTenantAdmin}
		c.Request = c.Request.WithContext(tenantdb.WithTenant(c.Request.Context(), tc))
		c.Next()
	}
}

func newMinimalRAGHandler() *RAGHandler {
	return NewRAGHandler(nil, nil, nil, zap.NewNop())
}

func TestListWorkspaces_MissingTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newMinimalRAGHandler()
	r.GET("/knowledge/workspaces", h.ListWorkspaces)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/knowledge/workspaces", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateWorkspace_MissingTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newMinimalRAGHandler()
	r.POST("/knowledge/workspaces", h.CreateWorkspace)

	body, _ := json.Marshal(map[string]any{"name": "test"})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/knowledge/workspaces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateWorkspace_InvalidEmbeddingModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newMinimalRAGHandler()
	r.POST("/knowledge/workspaces", injectRAGTenant("test-tenant-id"), h.CreateWorkspace)

	body, _ := json.Marshal(map[string]any{
		"name":   "test",
		"config": map[string]any{"embedding_model": "gpt-4"},
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/knowledge/workspaces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "unsupported embedding model" {
		t.Errorf("unexpected error: %q", resp["error"])
	}
}

func TestCreateWorkspace_InvalidQueryMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newMinimalRAGHandler()
	r.POST("/knowledge/workspaces", injectRAGTenant("test-tenant-id"), h.CreateWorkspace)

	body, _ := json.Marshal(map[string]any{
		"name":   "test",
		"config": map[string]any{"query_mode": "invalid"},
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/knowledge/workspaces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "invalid query_mode" {
		t.Errorf("unexpected error: %q", resp["error"])
	}
}

func TestQuery_MissingTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newMinimalRAGHandler()
	r.POST("/knowledge/query", h.Query)

	body, _ := json.Marshal(map[string]any{
		"question": "hello", "workspace": "ws", "mode": "hybrid",
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/knowledge/query", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

func TestDeleteWorkspace_MissingTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newMinimalRAGHandler()
	r.DELETE("/knowledge/workspaces/:name", h.DeleteWorkspace)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/knowledge/workspaces/myws", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}
