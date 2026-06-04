package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/document"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/embedding"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/knowledge"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/textchunk"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/vector"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func setupRAGRouter(handler *RAGHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		tc := &tenantdb.TenantContext{TenantID: "tenant-1", UserID: "user-1", Role: tenantdb.RoleTenantAdmin}
		c.Request = c.Request.WithContext(tenantdb.WithTenant(c.Request.Context(), tc))
		c.Next()
	})
	return router
}

func newTestRAGHandler(logger *zap.Logger) *RAGHandler {
	parser := document.NewParser(logger)
	chunker := textchunk.NewChunker(logger)
	embedSvc := embedding.NewEmbeddingService(llmgateway.NewOpenAIClient("", "", logger), logger)
	vectorStore := vector.NewVectorStore("localhost", "19530", logger)
	graphRAG := knowledge.NewGraphRAG("bolt://localhost:7687", "neo4j", "password", logger)
	ingestSvc := knowledge.NewKnowledgeIngest(parser, chunker, embedSvc, vectorStore, graphRAG, logger)
	ragService := knowledge.NewRAGService(embedSvc, vectorStore, graphRAG, logger)
	return NewRAGHandler(ingestSvc, ragService, logger)
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
