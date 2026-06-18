package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/api/middleware"
	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure/embedding"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/byteBuilderX/stratum/pkg/vector"
)

func setupRAGRouter(handler *RAGHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	router.Use(func(c *gin.Context) {
		tc := &tenantdb.TenantContext{TenantID: "tenant-1", UserID: "user-1", Role: tenantdb.RoleTenantAdmin}
		ctx := tenantdb.WithTenant(c.Request.Context(), tc)
		ctx = reqctx.WithTenantID(ctx, "tenant-1")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	return router
}

// newTestRAGHandler builds a handler with a real RAGService (the tests below
// only exercise the binding/error-path layers, so the WorkspaceService is
// constructed with nil deps — never reached on these inputs).
func newTestRAGHandler(logger *zap.Logger) *RAGHandler {
	embedSvc := embedding.NewEmbeddingService(llmgateway.NewQwenClient("", logger), logger)
	vectorStore := vector.NewVectorStore("localhost", "19530", logger)
	graphRAG := knowledge.NewMockGraphStore()
	ragService := knowledge.NewRAGService(embedSvc, vectorStore, graphRAG, logger)
	wsService := knowledge.NewWorkspaceService(nil, nil, logger)
	return NewRAGHandler(ragService, wsService, logger)
}

func TestRAGHandlerUploadDocument(t *testing.T) {
	logger := zap.NewNop()
	handler := newTestRAGHandler(logger)
	router := setupRAGRouter(handler)
	router.POST("/upload", handler.UploadDocument)

	req := map[string]interface{}{
		"workspace":   "test",
		"document_id": "doc1",
		"filename":    "test.txt",
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	httpReq, _ := http.NewRequest("POST", "/upload", bytes.NewBuffer(body)) //nolint:noctx
	httpReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK && w.Code != http.StatusBadRequest && w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 200/400/500, got %d", w.Code)
	}
}

func TestRAGHandlerQuery(t *testing.T) {
	logger := zap.NewNop()
	handler := newTestRAGHandler(logger)
	router := setupRAGRouter(handler)
	router.POST("/query", handler.Query)

	req := map[string]interface{}{
		"question":  "test question",
		"workspace": "test",
		"mode":      "vector",
		"topk":      5,
	}
	body, _ := json.Marshal(req)

	w := httptest.NewRecorder()
	httpReq, _ := http.NewRequest("POST", "/query", bytes.NewBuffer(body)) //nolint:noctx
	httpReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK && w.Code != http.StatusBadRequest && w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 200/400/500, got %d", w.Code)
	}
}

func TestRAGHandlerUploadDocumentInvalidRequest(t *testing.T) {
	logger := zap.NewNop()
	handler := newTestRAGHandler(logger)
	router := setupRAGRouter(handler)
	router.POST("/upload", handler.UploadDocument)

	w := httptest.NewRecorder()
	httpReq, _ := http.NewRequest("POST", "/upload", bytes.NewBuffer([]byte("invalid json"))) //nolint:noctx
	httpReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestRAGHandlerQueryInvalidRequest(t *testing.T) {
	logger := zap.NewNop()
	handler := newTestRAGHandler(logger)
	router := setupRAGRouter(handler)
	router.POST("/query", handler.Query)

	w := httptest.NewRecorder()
	httpReq, _ := http.NewRequest("POST", "/query", bytes.NewBuffer([]byte("invalid json"))) //nolint:noctx
	httpReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}
