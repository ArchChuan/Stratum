package handler

import (
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/knowledge"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type RAGHandler struct {
	ingestSvc  *knowledge.KnowledgeIngest
	ragService *knowledge.RAGService
	db         *pgxpool.Pool
	logger     *zap.Logger
}

func NewRAGHandler(
	ingestSvc *knowledge.KnowledgeIngest,
	ragService *knowledge.RAGService,
	db *pgxpool.Pool,
	logger *zap.Logger,
) *RAGHandler {
	return &RAGHandler{
		ingestSvc:  ingestSvc,
		ragService: ragService,
		db:         db,
		logger:     logger,
	}
}

// WorkspaceConfig holds per-workspace RAG configuration stored as JSONB.
type WorkspaceConfig struct {
	EmbeddingModel string `json:"embedding_model"`
	ChunkSize      int    `json:"chunk_size"`
	ChunkOverlap   int    `json:"chunk_overlap"`
	QueryMode      string `json:"query_mode"`
	TopK           int    `json:"top_k"`
}

var allowedEmbeddingModels = map[string]bool{
	"text-embedding-v3": true,
	"embedding-3":       true,
}

var allowedQueryModes = map[string]bool{
	"vector": true,
	"graph":  true,
	"hybrid": true,
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
	Name        string          `json:"name" binding:"required"`
	Description string          `json:"description"`
	Config      WorkspaceConfig `json:"config"`
}

type UpdateWorkspaceRequest struct {
	Description *string          `json:"description"`
	Config      *WorkspaceConfig `json:"config"`
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
	defer file.Close() //nolint:errcheck

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
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var req CreateWorkspaceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cfg := req.Config
	if cfg.EmbeddingModel == "" {
		cfg.EmbeddingModel = "text-embedding-v3"
	}
	if !allowedEmbeddingModels[cfg.EmbeddingModel] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported embedding model"})
		return
	}
	if cfg.QueryMode == "" {
		cfg.QueryMode = "hybrid"
	}
	if !allowedQueryModes[cfg.QueryMode] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid query_mode"})
		return
	}
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = 512
	}
	if cfg.ChunkOverlap <= 0 {
		cfg.ChunkOverlap = 64
	}
	if cfg.TopK <= 0 {
		cfg.TopK = 5
	}

	schema := "tenant_" + tenantID
	var id string
	err := h.db.QueryRow(c.Request.Context(),
		fmt.Sprintf(`INSERT INTO "%s".rag_workspaces (name, description, config)
                     VALUES ($1, $2, $3) RETURNING id`, schema),
		req.Name, req.Description, cfg,
	).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			c.JSON(http.StatusConflict, gin.H{"error": "workspace already exists"})
			return
		}
		h.logger.Error("failed to create workspace", zap.String("tenant_id", tenantID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.logger.Info("workspace created", zap.String("name", req.Name), zap.String("tenant_id", tenantID))
	c.JSON(http.StatusCreated, gin.H{
		"id":          id,
		"name":        req.Name,
		"description": req.Description,
		"config":      cfg,
	})
}

func (h *RAGHandler) ListWorkspaces(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}

	schema := "tenant_" + tenantID
	rows, err := h.db.Query(c.Request.Context(),
		fmt.Sprintf(`SELECT id, name, COALESCE(description,''), config, created_at, updated_at
                     FROM "%s".rag_workspaces ORDER BY created_at DESC`, schema),
	)
	if err != nil {
		h.logger.Error("failed to list workspaces", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	type workspaceRow struct {
		ID          string          `json:"id"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Config      WorkspaceConfig `json:"config"`
		CreatedAt   time.Time       `json:"created_at"`
		UpdatedAt   time.Time       `json:"updated_at"`
	}
	var workspaces []workspaceRow
	for rows.Next() {
		var r workspaceRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &r.Config, &r.CreatedAt, &r.UpdatedAt); err != nil {
			h.logger.Error("failed to scan workspace row", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		workspaces = append(workspaces, r)
	}
	if workspaces == nil {
		workspaces = []workspaceRow{}
	}
	c.JSON(http.StatusOK, gin.H{"workspaces": workspaces})
}

func (h *RAGHandler) UpdateWorkspace(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace name required"})
		return
	}

	var req UpdateWorkspaceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	schema := "tenant_" + tenantID

	var currentCfg WorkspaceConfig
	err := h.db.QueryRow(c.Request.Context(),
		fmt.Sprintf(`SELECT config FROM "%s".rag_workspaces WHERE name = $1`, schema),
		name,
	).Scan(&currentCfg)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
		return
	}

	if req.Config != nil {
		if req.Config.EmbeddingModel != "" && req.Config.EmbeddingModel != currentCfg.EmbeddingModel {
			c.JSON(http.StatusBadRequest, gin.H{"error": "embedding_model is immutable after creation"})
			return
		}
		req.Config.EmbeddingModel = currentCfg.EmbeddingModel

		if req.Config.QueryMode != "" && !allowedQueryModes[req.Config.QueryMode] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid query_mode"})
			return
		}
		if req.Config.QueryMode == "" {
			req.Config.QueryMode = currentCfg.QueryMode
		}
		if req.Config.ChunkSize <= 0 {
			req.Config.ChunkSize = currentCfg.ChunkSize
		}
		if req.Config.ChunkOverlap <= 0 {
			req.Config.ChunkOverlap = currentCfg.ChunkOverlap
		}
		if req.Config.TopK <= 0 {
			req.Config.TopK = currentCfg.TopK
		}
	}

	var newDesc *string
	if req.Description != nil {
		newDesc = req.Description
	}
	newCfg := currentCfg
	if req.Config != nil {
		newCfg = *req.Config
	}

	_, err = h.db.Exec(c.Request.Context(),
		fmt.Sprintf(`UPDATE "%s".rag_workspaces
                     SET description = COALESCE($1, description),
                         config = $2,
                         updated_at = NOW()
                     WHERE name = $3`, schema),
		newDesc, newCfg, name,
	)
	if err != nil {
		h.logger.Error("failed to update workspace", zap.String("name", name), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"name": name, "config": newCfg})
}

func (h *RAGHandler) GetWorkspaceStats(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace name required"})
		return
	}

	schema := "tenant_" + tenantID
	var cfg WorkspaceConfig
	var description string
	err := h.db.QueryRow(c.Request.Context(),
		fmt.Sprintf(`SELECT COALESCE(description,''), config
                     FROM "%s".rag_workspaces WHERE name = $1`, schema),
		name,
	).Scan(&description, &cfg)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
		return
	}

	milvusStats, err := h.ingestSvc.GetWorkspaceStats(c.Request.Context(), name)
	if err != nil {
		h.logger.Warn("failed to get milvus stats", zap.String("workspace", name), zap.Error(err))
		milvusStats = map[string]any{"error": err.Error()}
	}

	c.JSON(http.StatusOK, gin.H{
		"name":        name,
		"description": description,
		"config":      cfg,
		"stats":       milvusStats,
	})
}

func (h *RAGHandler) DeleteWorkspace(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace parameter required"})
		return
	}

	schema := "tenant_" + tenantID
	tag, err := h.db.Exec(c.Request.Context(),
		fmt.Sprintf(`DELETE FROM "%s".rag_workspaces WHERE name = $1`, schema),
		name,
	)
	if err != nil {
		h.logger.Error("failed to delete workspace from db", zap.String("name", name), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
		return
	}

	h.logger.Info("workspace deleted", zap.String("name", name), zap.String("tenant_id", tenantID))
	c.JSON(http.StatusOK, gin.H{"success": true, "workspace": name})
}
