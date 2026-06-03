package handler

import (
	"context"
	"mime/multipart"
	"net/http"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/knowledge"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type RAGHandler struct {
	ingestSvc  *knowledge.KnowledgeIngest
	ragService *knowledge.RAGService
	logger     *zap.Logger
}

func NewRAGHandler(
	ingestSvc *knowledge.KnowledgeIngest,
	ragService *knowledge.RAGService,
	logger *zap.Logger,
) *RAGHandler {
	return &RAGHandler{
		ingestSvc:  ingestSvc,
		ragService: ragService,
		logger:     logger,
	}
}

type UploadDocumentRequest struct {
	Workspace string                `form:"workspace" binding:"required"`
	File      *multipart.FileHeader `form:"file" binding:"required"`
}

type QueryRequest struct {
	Question  string `json:"question" binding:"required"`
	Workspace string `json:"workspace" binding:"required"`
	Mode      string `json:"mode" binding:"required,oneof=vector graph hybrid"`
	TopK      int    `json:"topK"`
}

type CreateWorkspaceRequest struct {
	Workspace string `json:"workspace" binding:"required"`
}

type IngestDocumentRequest struct {
	Workspace    string `json:"workspace" binding:"required"`
	DocumentData []byte `json:"document_data" binding:"required"`
	FileName     string `json:"filename" binding:"required"`
	DocumentID   string `json:"document_id" binding:"required"`
}

func (h *RAGHandler) UploadDocument(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var req UploadDocumentRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.logger.Info("uploading document",
		zap.String("workspace", req.Workspace),
		zap.String("filename", req.File.Filename))

	file, err := req.File.Open()
	if err != nil {
		h.logger.Error("failed to open uploaded file", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to open file"})
		return
	}
	defer file.Close()

	fileData := make([]byte, req.File.Size)
	if _, err := file.Read(fileData); err != nil {
		h.logger.Error("failed to read uploaded file", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
		return
	}

	documentID := uuid.New().String()

	ingestReq := knowledge.IngestDocumentRequest{
		TenantID:     tenantID,
		Workspace:    req.Workspace,
		DocumentData: fileData,
		FileName:     req.File.Filename,
		DocumentID:   documentID,
	}

	result, err := h.ingestSvc.IngestDocument(c.Request.Context(), ingestReq)
	if err != nil {
		h.logger.Error("document ingestion failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"document_id":   result.DocumentID,
		"workspace":     result.Workspace,
		"total_chunks":  result.TotalChunks,
		"total_vectors": result.TotalVectors,
		"total_nodes":   result.TotalNodes,
		"duration":      result.Duration.String(),
		"errors":        result.Errors,
	})
}

func (h *RAGHandler) Query(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var req QueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.logger.Info("executing RAG query",
		zap.String("question", req.Question),
		zap.String("workspace", req.Workspace),
		zap.String("mode", req.Mode))

	if req.TopK <= 0 {
		req.TopK = 5
	}

	ragReq := knowledge.RAGQueryRequest{
		Question:  req.Question,
		Workspace: req.Workspace,
		TenantID:  tenantID,
		Mode:      req.Mode,
		TopK:      req.TopK,
	}

	result, err := h.ragService.Query(c.Request.Context(), ragReq)
	if err != nil {
		h.logger.Error("RAG query failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	sources := make([]gin.H, 0, len(result.Sources))
	for i, src := range result.Sources {
		sources[i] = gin.H{
			"document_id": src.DocumentID,
			"content":     src.Content,
			"chunk_index": src.ChunkIndex,
			"score":       src.Score,
		}
	}

	graphContext := make([]gin.H, 0, len(result.GraphContext))
	for i, e := range result.GraphContext {
		graphContext[i] = gin.H{
			"id":         e.ID,
			"label":      e.Label,
			"properties": e.Properties,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"answer":        result.Answer,
		"sources":       sources,
		"graph_context": graphContext,
		"mode":          result.Mode,
		"latency_ms":    result.Latency.Milliseconds(),
	})
}

func (h *RAGHandler) CreateWorkspace(c *gin.Context) {
	var req CreateWorkspaceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.logger.Info("creating workspace", zap.String("workspace", req.Workspace))

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"workspace": req.Workspace,
		"message":   "workspace created successfully",
	})
}

func (h *RAGHandler) ListWorkspaces(c *gin.Context) {
	ctx := context.Background()

	workspaces, err := h.ragService.GetWorkspaceCollections(ctx)
	if err != nil {
		h.logger.Error("failed to get workspaces", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"workspaces": workspaces,
	})
}

func (h *RAGHandler) GetWorkspaceStats(c *gin.Context) {
	workspace := c.Param("workspace")
	if workspace == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace parameter required"})
		return
	}

	ctx := context.Background()
	stats, err := h.ingestSvc.GetWorkspaceStats(ctx, workspace)
	if err != nil {
		h.logger.Error("failed to get workspace stats", zap.String("workspace", workspace), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"stats":   stats,
	})
}

func (h *RAGHandler) DeleteWorkspace(c *gin.Context) {
	workspace := c.Param("workspace")
	if workspace == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace parameter required"})
		return
	}

	h.logger.Info("deleting workspace", zap.String("workspace", workspace))

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"workspace": workspace,
		"message":   "workspace deleted successfully",
	})
}
