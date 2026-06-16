package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/byteBuilderX/stratum/api/model"
	"github.com/byteBuilderX/stratum/internal/agent"
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
		h.logger.Warn("agent not found", zap.String("id", id))
		c.JSON(http.StatusNotFound, model.ErrorResponse{
			Code:    http.StatusNotFound,
			Message: "agent not found",
		})
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
		h.logger.Warn("invalid request", zap.Error(err))
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: err.Error(),
		})
		return
	}

	// Inherit embed_model from tenant settings.
	embedModel := req.EmbedModel
	if embedModel == "" && h.db != nil {
		var settingsJSON []byte
		_ = h.db.QueryRow(c.Request.Context(),
			"SELECT settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL",
			tenantID,
		).Scan(&settingsJSON)
		var ts map[string]interface{}
		if len(settingsJSON) > 0 {
			_ = json.Unmarshal(settingsJSON, &ts)
		}
		embedModel, _ = ts["embed_model"].(string)
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
		if errors.Is(err, agent.ErrNameConflict) {
			c.JSON(http.StatusConflict, model.ErrorResponse{Code: http.StatusConflict, Message: err.Error()})
			return
		}
		h.logger.Error("failed to register agent", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{
			Code:    http.StatusInternalServerError,
			Message: fmt.Sprintf("failed to create agent: %v", err),
		})
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
		h.logger.Warn("invalid request", zap.Error(err))
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: err.Error(),
		})
		return
	}

	existing, ok := h.agentRegistry.Get(c.Request.Context(), id)
	if !ok {
		c.JSON(http.StatusNotFound, model.ErrorResponse{
			Code:    http.StatusNotFound,
			Message: "agent not found",
		})
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
		if errors.Is(err, agent.ErrNotFound) {
			c.JSON(http.StatusNotFound, model.ErrorResponse{
				Code:    http.StatusNotFound,
				Message: "agent not found",
			})
			return
		}
		if errors.Is(err, agent.ErrInvalidSkill) {
			c.JSON(http.StatusUnprocessableEntity, model.ErrorResponse{
				Code:    http.StatusUnprocessableEntity,
				Message: fmt.Sprintf("invalid skill: %v", err),
			})
			return
		}
		h.logger.Error("failed to update agent", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{
			Code:    http.StatusInternalServerError,
			Message: fmt.Sprintf("failed to update agent: %v", err),
		})
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
		h.logger.Warn("agent not found or removal failed", zap.String("id", id), zap.Error(err))
		c.JSON(http.StatusNotFound, model.ErrorResponse{
			Code:    http.StatusNotFound,
			Message: "agent not found",
		})
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
		h.logger.Error("list executions failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "failed to list executions"})
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
