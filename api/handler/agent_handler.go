package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/api/model"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/agent"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/observability"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type AgentHandler struct {
	agentRegistry  *agent.Registry
	logger         *zap.Logger
	gateway        *llmgateway.Gateway
	metrics        observability.MetricsProvider
	executionStore *agent.ExecutionStore
}

type CreateAgentRequest struct {
	Name          string   `json:"name" binding:"required"`
	Type          string   `json:"type"`
	Description   string   `json:"description"`
	Persona       string   `json:"persona"`
	SystemPrompt  string   `json:"systemPrompt"`
	LLMModel      string   `json:"llmModel" binding:"required"`
	MaxIterations int      `json:"maxIterations" binding:"required"`
	AllowedSkills []string `json:"allowedSkills"`
}

type AgentResponse struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	Description   string   `json:"description"`
	Persona       string   `json:"persona"`
	SystemPrompt  string   `json:"systemPrompt"`
	LLMModel      string   `json:"llmModel"`
	MaxIterations int      `json:"maxIterations"`
	AllowedSkills []string `json:"allowedSkills"`
	CreatedAt     string   `json:"createdAt"`
}

type ExecuteAgentRequest struct {
	Query   string                 `json:"query"`
	Context map[string]interface{} `json:"context"`
	Options map[string]interface{} `json:"options"`
}

type AgentExecutionResult struct {
	AgentID    string                 `json:"agentId"`
	Input      string                 `json:"input"`
	Output     string                 `json:"output"`
	Steps      int                    `json:"steps"`
	TokensUsed int                    `json:"tokensUsed"`
	Duration   string                 `json:"duration"`
	Thoughts   []agent.Thought        `json:"thoughts"`
	ToolCalls  []agent.ToolCall       `json:"toolCalls"`
	Metadata   map[string]interface{} `json:"metadata"`
	Error      string                 `json:"error,omitempty"`
}

func NewAgentHandler(agentRegistry *agent.Registry, logger *zap.Logger, gateway *llmgateway.Gateway, metrics observability.MetricsProvider, execStore *agent.ExecutionStore) *AgentHandler {
	return &AgentHandler{
		agentRegistry:  agentRegistry,
		logger:         logger,
		gateway:        gateway,
		metrics:        metrics,
		executionStore: execStore,
	}
}

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
			ID:            cfg.ID,
			Name:          cfg.Name,
			Type:          string(cfg.Type),
			Description:   cfg.Description,
			Persona:       cfg.Persona,
			SystemPrompt:  cfg.SystemPrompt,
			LLMModel:      cfg.LLMModel,
			MaxIterations: cfg.MaxIterations,
			AllowedSkills: []string{},
			CreatedAt:     time.Now().Format(time.RFC3339),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"agents": responses,
	})
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
		ID:            cfg.ID,
		Name:          cfg.Name,
		Type:          string(cfg.Type),
		Description:   cfg.Description,
		Persona:       cfg.Persona,
		SystemPrompt:  cfg.SystemPrompt,
		LLMModel:      cfg.LLMModel,
		MaxIterations: cfg.MaxIterations,
		AllowedSkills: []string{},
		CreatedAt:     time.Now().Format(time.RFC3339),
	})
}

func (h *AgentHandler) CreateAgent(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
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

	id := uuid.New().String()

	agentType := agent.ReActAgent
	switch req.Type {
	case "react":
		agentType = agent.ReActAgent
	case "cot":
		agentType = agent.CoTAgent
	case "planning":
		agentType = agent.PlanningAgent
	case "tool_calling":
		agentType = agent.ToolCallingAgent
	case "rag":
		agentType = agent.RAGAgent
	case "swarm":
		agentType = agent.SwarmAgent
	}

	cfg := &agent.AgentConfig{
		ID:            id,
		Name:          req.Name,
		Type:          agentType,
		Description:   req.Description,
		Persona:       req.Persona,
		SystemPrompt:  req.SystemPrompt,
		LLMModel:      req.LLMModel,
		MaxIterations: req.MaxIterations,
		Capabilities:  []agent.AgentCapability{},
	}

	a := agent.NewBaseAgent(cfg, h.logger).WithMetrics(h.metrics)

	if err := h.agentRegistry.Register(c.Request.Context(), a); err != nil {
		h.logger.Error("failed to register agent", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{
			Code:    http.StatusInternalServerError,
			Message: fmt.Sprintf("failed to create agent: %v", err),
		})
		return
	}

	h.logger.Info("agent created", zap.String("id", id), zap.String("name", req.Name))

	c.JSON(http.StatusCreated, AgentResponse{
		ID:            id,
		Name:          req.Name,
		Type:          string(agentType),
		Description:   req.Description,
		Persona:       req.Persona,
		SystemPrompt:  req.SystemPrompt,
		LLMModel:      req.LLMModel,
		MaxIterations: req.MaxIterations,
		AllowedSkills: req.AllowedSkills,
		CreatedAt:     time.Now().Format(time.RFC3339),
	})
}

func (h *AgentHandler) ExecuteAgent(c *gin.Context) {
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

	var req ExecuteAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid request", zap.Error(err))
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: err.Error(),
		})
		return
	}

	options := []agent.ExecutionOption{
		agent.WithMaxSteps(a.GetConfig().MaxIterations),
	}

	if req.Options != nil {
		if maxSteps, ok := req.Options["maxSteps"].(float64); ok {
			options = append(options, agent.WithMaxSteps(int(maxSteps)))
		}
		if timeout, ok := req.Options["timeout"].(float64); ok {
			options = append(options, agent.WithTimeout(time.Duration(timeout)*time.Second))
		}
	}

	ctx := c.Request.Context()
	tenantID, _ := tenantIDFromCtx(c)
	userID, _ := userIDFromCtx(c)
	start := time.Now()
	result, err := a.Execute(ctx, req.Query, options...)
	durationMs := int(time.Since(start).Milliseconds())

	// fire-and-forget execution record
	if h.executionStore != nil {
		rec := agent.ExecutionRecord{
			TenantID:     tenantID,
			AgentID:      id,
			UserID:       userID,
			AgentName:    a.GetConfig().Name,
			InputPreview: truncate(req.Query, 50),
			TotalTokens:  0,
			DurationMs:   durationMs,
		}
		if err != nil {
			rec.Status = "error"
			rec.ErrorMessage = err.Error()
		} else {
			rec.Status = "success"
			rec.OutputPreview = truncate(result.Output, 50)
			rec.TotalTokens = result.TokensUsed
		}
		go func() {
			if insertErr := h.executionStore.Insert(context.Background(), rec); insertErr != nil {
				h.logger.Warn("execution record insert failed", zap.Error(insertErr))
			}
		}()
	}

	if err != nil {
		h.logger.Error("agent execution failed", zap.String("agentId", id), zap.Error(err))
		c.JSON(http.StatusOK, AgentExecutionResult{
			AgentID:  id,
			Input:    req.Query,
			Output:   "",
			Steps:    0,
			Duration: "0s",
			Error:    err.Error(),
		})
		return
	}

	thoughtsJSON, _ := json.Marshal(result.Thoughts)
	toolCallsJSON, _ := json.Marshal(result.ToolCalls)

	c.JSON(http.StatusOK, AgentExecutionResult{
		AgentID:    id,
		Input:      req.Query,
		Output:     result.Output,
		Steps:      result.Steps,
		TokensUsed: result.TokensUsed,
		Duration:   result.Duration.String(),
		Thoughts:   result.Thoughts,
		ToolCalls:  result.ToolCalls,
		Metadata: map[string]interface{}{
			"thoughtsJSON":  string(thoughtsJSON),
			"toolCallsJSON": string(toolCallsJSON),
		},
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
	c.JSON(http.StatusOK, gin.H{
		"message": "agent deleted successfully",
	})
}

// ListExecutions returns agent execution history for the current tenant (last 30 days).
// GET /agents/executions
func (h *AgentHandler) ListExecutions(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	if h.executionStore == nil {
		c.JSON(http.StatusOK, gin.H{"executions": []struct{}{}})
		return
	}
	records, err := h.executionStore.List(c.Request.Context(), tenantID)
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
	c.JSON(http.StatusOK, gin.H{"executions": out})
}
