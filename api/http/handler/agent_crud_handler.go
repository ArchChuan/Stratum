package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/byteBuilderX/stratum/api/middleware"
	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func (h *AgentHandler) GetAllAgents(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	agents := h.agentRegistry.GetAll(c.Request.Context())
	responses := make([]AgentResponse, 0, len(agents))

	for _, a := range agents {
		cfg := a.GetConfig()
		responses = append(responses, AgentResponse{
			ID:                    cfg.ID,
			Name:                  cfg.Name,
			Type:                  string(cfg.Type),
			Description:           cfg.Description,
			Persona:               cfg.Persona,
			SystemPrompt:          cfg.SystemPrompt,
			LLMModel:              cfg.LLMModel,
			EmbedModel:            cfg.EmbedModel,
			MaxIterations:         cfg.MaxIterations,
			MaxContextTokens:      cfg.MaxContextTokens,
			AllowedSkills:         cfg.AllowedSkills,
			MCPServerIDs:          cfg.MCPServerIDs,
			KnowledgeWorkspaceIDs: cfg.KnowledgeWorkspaceIDs,
			CreatedAt:             time.Now().Format(time.RFC3339),
		})
	}

	c.JSON(http.StatusOK, gin.H{"agents": responses})
}

func (h *AgentHandler) GetAgent(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	id := c.Param("id")
	a, ok := h.agentRegistry.Get(c.Request.Context(), id)
	if !ok {
		_ = c.Error(agent.ErrNotFound)
		return
	}

	cfg := a.GetConfig()
	c.JSON(http.StatusOK, AgentResponse{
		ID:                    cfg.ID,
		Name:                  cfg.Name,
		Type:                  string(cfg.Type),
		Description:           cfg.Description,
		Persona:               cfg.Persona,
		SystemPrompt:          cfg.SystemPrompt,
		LLMModel:              cfg.LLMModel,
		EmbedModel:            cfg.EmbedModel,
		MaxIterations:         cfg.MaxIterations,
		MaxContextTokens:      cfg.MaxContextTokens,
		AllowedSkills:         cfg.AllowedSkills,
		MCPServerIDs:          cfg.MCPServerIDs,
		KnowledgeWorkspaceIDs: cfg.KnowledgeWorkspaceIDs,
		CreatedAt:             time.Now().Format(time.RFC3339),
	})
}

func (h *AgentHandler) CreateAgent(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var req CreateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}

	// Inherit embed_model from tenant settings.
	embedModel := req.EmbedModel
	if embedModel == "" && h.tenantSettings != nil {
		embedModel, _ = h.tenantSettings.GetEmbedModel(c.Request.Context(), tenantID)
	}

	id := uuid.New().String()
	agentType := parseAgentType(req.Type)

	cfg := &agent.AgentConfig{
		ID:                    id,
		Name:                  req.Name,
		Type:                  agentType,
		Description:           req.Description,
		Persona:               req.Persona,
		SystemPrompt:          req.SystemPrompt,
		LLMModel:              req.LLMModel,
		EmbedModel:            embedModel,
		MaxIterations:         req.MaxIterations,
		MaxContextTokens:      req.MaxContextTokens,
		AllowedSkills:         req.AllowedSkills,
		MCPServerIDs:          req.MCPServerIDs,
		KnowledgeWorkspaceIDs: req.KnowledgeWorkspaceIDs,
		Capabilities:          []agent.AgentCapability{},
	}

	a := agent.NewBaseAgent(cfg, h.logger).WithMetrics(h.metrics)

	if err := h.agentRegistry.Register(c.Request.Context(), a); err != nil {
		_ = c.Error(err)
		return
	}

	h.logger.Info("agent created", zap.String("id", id), zap.String("name", req.Name))

	c.JSON(http.StatusCreated, AgentResponse{
		ID:                    id,
		Name:                  req.Name,
		Type:                  string(agentType),
		Description:           req.Description,
		Persona:               req.Persona,
		SystemPrompt:          req.SystemPrompt,
		LLMModel:              req.LLMModel,
		EmbedModel:            embedModel,
		MaxIterations:         req.MaxIterations,
		MaxContextTokens:      req.MaxContextTokens,
		AllowedSkills:         req.AllowedSkills,
		MCPServerIDs:          req.MCPServerIDs,
		KnowledgeWorkspaceIDs: req.KnowledgeWorkspaceIDs,
		CreatedAt:             time.Now().Format(time.RFC3339),
	})
}

func (h *AgentHandler) UpdateAgent(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	id := c.Param("id")

	var req UpdateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}

	existing, ok := h.agentRegistry.Get(c.Request.Context(), id)
	if !ok {
		_ = c.Error(agent.ErrNotFound)
		return
	}
	existingEmbedModel := existing.GetConfig().EmbedModel
	agentType := parseAgentType(req.Type)

	skills := req.AllowedSkills
	if skills == nil {
		skills = []string{}
	}

	cfg := &agent.AgentConfig{
		ID:                    id,
		Name:                  req.Name,
		Type:                  agentType,
		Description:           req.Description,
		Persona:               req.Persona,
		SystemPrompt:          req.SystemPrompt,
		LLMModel:              req.LLMModel,
		EmbedModel:            existingEmbedModel,
		MaxIterations:         req.MaxIterations,
		MaxContextTokens:      req.MaxContextTokens,
		AllowedSkills:         skills,
		MCPServerIDs:          req.MCPServerIDs,
		KnowledgeWorkspaceIDs: req.KnowledgeWorkspaceIDs,
	}

	if err := h.agentRegistry.Update(c.Request.Context(), cfg); err != nil {
		_ = c.Error(err)
		return
	}

	h.logger.Info("agent updated", zap.String("id", id), zap.String("name", req.Name))

	c.JSON(http.StatusOK, AgentResponse{
		ID:                    id,
		Name:                  req.Name,
		Type:                  string(agentType),
		Description:           req.Description,
		Persona:               req.Persona,
		SystemPrompt:          req.SystemPrompt,
		LLMModel:              req.LLMModel,
		EmbedModel:            existingEmbedModel,
		MaxIterations:         req.MaxIterations,
		MaxContextTokens:      req.MaxContextTokens,
		AllowedSkills:         skills,
		MCPServerIDs:          req.MCPServerIDs,
		KnowledgeWorkspaceIDs: req.KnowledgeWorkspaceIDs,
		CreatedAt:             time.Now().Format(time.RFC3339),
	})
}

func (h *AgentHandler) DeleteAgent(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	id := c.Param("id")

	if err := h.agentRegistry.Remove(c.Request.Context(), id); err != nil {
		_ = c.Error(err)
		return
	}

	h.logger.Info("agent deleted", zap.String("id", id))
	c.JSON(http.StatusOK, gin.H{"message": "agent deleted successfully"})
}

func (h *AgentHandler) ListExecutions(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	if h.executionStore == nil {
		c.JSON(http.StatusOK, gin.H{"executions": []struct{}{}, "total": 0})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	records, total, err := h.executionStore.List(c.Request.Context(), agent.ListOptions{Page: page, PageSize: pageSize})
	if err != nil {
		_ = c.Error(err)
		return
	}
	type row struct {
		ID            string `json:"id"`
		AgentID       string `json:"agent_id"`
		AgentName     string `json:"agent_name"`
		UserID        string `json:"user_id"`
		Status        string `json:"status"`
		InputPreview  string `json:"input_preview"`
		OutputPreview string `json:"output_preview"`
		ErrorMessage  string `json:"error_message"`
		TotalTokens   int    `json:"total_tokens"`
		DurationMs    int    `json:"duration_ms"`
		CreatedAt     string `json:"created_at"`
	}
	out := make([]row, 0, len(records))
	for _, r := range records {
		out = append(out, row{
			ID:            r.ID,
			AgentID:       r.AgentID,
			AgentName:     r.AgentName,
			UserID:        r.UserID,
			Status:        r.Status,
			InputPreview:  r.InputPreview,
			OutputPreview: r.OutputPreview,
			ErrorMessage:  r.ErrorMessage,
			TotalTokens:   r.TotalTokens,
			DurationMs:    r.DurationMs,
			CreatedAt:     r.CreatedAt.Format(time.RFC3339),
		})
	}
	c.JSON(http.StatusOK, gin.H{"executions": out, "total": total})
}
