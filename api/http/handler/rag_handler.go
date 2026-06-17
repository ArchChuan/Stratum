package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/api/http/dto"
	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	skillpkg "github.com/byteBuilderX/stratum/internal/skill/domain"
)

// RAGHandler exposes /knowledge/* endpoints. All persistence and validation is
// delegated to WorkspaceService; this layer only binds requests, calls the
// service, and renders responses.
type RAGHandler struct {
	ragService *knowledge.RAGService
	wsService  *knowledge.WorkspaceService
	logger     *zap.Logger
}

// NewRAGHandler constructs a RAGHandler. wsService may be nil for unit tests
// that only exercise the missing-tenant guard rails — every other path
// dereferences it.
func NewRAGHandler(
	ragService *knowledge.RAGService,
	wsService *knowledge.WorkspaceService,
	logger *zap.Logger,
) *RAGHandler {
	return &RAGHandler{
		ragService: ragService,
		wsService:  wsService,
		logger:     logger,
	}
}

func toDTOConfig(c domain.WorkspaceConfig) dto.WorkspaceConfig {
	return dto.WorkspaceConfig{
		EmbeddingModel: c.EmbeddingModel,
		ChunkSize:      c.ChunkSize,
		ChunkOverlap:   c.ChunkOverlap,
		QueryMode:      c.QueryMode,
		TopK:           c.TopK,
	}
}

func fromDTOConfig(c dto.WorkspaceConfig) domain.WorkspaceConfig {
	return domain.WorkspaceConfig{
		EmbeddingModel: c.EmbeddingModel,
		ChunkSize:      c.ChunkSize,
		ChunkOverlap:   c.ChunkOverlap,
		QueryMode:      c.QueryMode,
		TopK:           c.TopK,
	}
}

func (h *RAGHandler) UploadDocument(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var req dto.UploadDocumentRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.logger.Info("uploading document",
		zap.String("workspace", req.Workspace),
		zap.String("filename", req.File.Filename))

	result, err := h.wsService.IngestUpload(c.Request.Context(), tenantID, req.Workspace, req.File)
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"document_id":   result.DocumentID,
		"workspace":     result.Workspace,
		"total_chunks":  result.TotalChunks,
		"total_vectors": result.TotalVectors,
		"total_nodes":   result.TotalNodes,
		"duration":      result.Duration,
		"errors":        result.Errors,
	})
}

func (h *RAGHandler) Query(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var req dto.QueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.logger.Info("executing RAG query",
		zap.String("question", req.Question),
		zap.String("workspace", req.Workspace),
		zap.String("mode", req.Mode))

	if req.TopK <= 0 {
		req.TopK = skillpkg.DefaultTopK
	}

	result, err := h.ragService.Query(c.Request.Context(), knowledge.RAGQueryRequest{
		Question:  req.Question,
		Workspace: req.Workspace,
		TenantID:  tenantID,
		Mode:      req.Mode,
		TopK:      req.TopK,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}

	sources := make([]gin.H, len(result.Sources))
	for i, src := range result.Sources {
		sources[i] = gin.H{
			"document_id": src.DocumentID,
			"content":     src.Content,
			"chunk_index": src.ChunkIndex,
			"score":       src.Score,
		}
	}
	graphContext := make([]gin.H, len(result.GraphContext))
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
	var req dto.CreateWorkspaceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ws, err := h.wsService.CreateWorkspace(c.Request.Context(), tenantID, knowledge.CreateWorkspaceInput{
		Name:        req.Name,
		Description: req.Description,
		Config:      fromDTOConfig(req.Config),
	})
	if err != nil {
		_ = c.Error(err)
		return
	}

	h.logger.Info("workspace created", zap.String("name", ws.Name), zap.String("tenant_id", tenantID))
	c.JSON(http.StatusCreated, gin.H{
		"id":          ws.ID,
		"name":        ws.Name,
		"description": ws.Description,
		"config":      toDTOConfig(ws.Config),
	})
}

func (h *RAGHandler) ListWorkspaces(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}

	list, err := h.wsService.ListWorkspaces(c.Request.Context(), tenantID)
	if err != nil {
		_ = c.Error(err)
		return
	}

	out := make([]dto.WorkspaceListItem, 0, len(list))
	for _, ws := range list {
		out = append(out, dto.WorkspaceListItem{
			ID:          ws.ID,
			Name:        ws.Name,
			Description: ws.Description,
			Config:      toDTOConfig(ws.Config),
			CreatedAt:   ws.CreatedAt,
			UpdatedAt:   ws.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"workspaces": out})
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

	var req dto.UpdateWorkspaceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	in := knowledge.UpdateWorkspaceInput{Description: req.Description}
	if req.Config != nil {
		cfg := fromDTOConfig(*req.Config)
		in.Config = &cfg
	}

	ws, err := h.wsService.UpdateWorkspace(c.Request.Context(), tenantID, name, in)
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"name": ws.Name, "config": toDTOConfig(ws.Config)})
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

	res, err := h.wsService.GetWorkspaceStats(c.Request.Context(), tenantID, name)
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"name":        res.Name,
		"description": res.Description,
		"config":      toDTOConfig(res.Config),
		"stats":       res.Stats,
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

	if err := h.wsService.DeleteWorkspace(c.Request.Context(), tenantID, name); err != nil {
		_ = c.Error(err)
		return
	}

	h.logger.Info("workspace deleted", zap.String("name", name), zap.String("tenant_id", tenantID))
	c.JSON(http.StatusOK, gin.H{"success": true, "workspace": name})
}
