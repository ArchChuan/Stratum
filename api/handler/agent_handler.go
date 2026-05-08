package handler

import (
	"net/http"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/api/model"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/agent"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// AgentHandler handles agent-related requests
type AgentHandler struct {
	registry *agent.Registry
	logger   *zap.Logger
	gateway  *llmgateway.Gateway
}

// NewAgentHandler creates a new agent handler
func NewAgentHandler(registry *agent.Registry, logger *zap.Logger, gateway *llmgateway.Gateway) *AgentHandler {
	return &AgentHandler{
		registry: registry,
		logger:   logger,
		gateway:  gateway,
	}
}

// CreateAgent handles agent creation
func (h *AgentHandler) CreateAgent(c *gin.Context) {
	var req model.CreateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid agent creation request", zap.Error(err))
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: err.Error(),
		})
		return
	}

	id := uuid.New().String()
	newAgent := agent.NewAgent(
		id,
		req.Name,
		req.Description,
		req.Persona,
		req.SystemPrompt,
		req.LLMModel,
		req.MaxIterations,
		req.AllowedSkills,
	)

	h.registry.Register(newAgent)
	h.logger.Info("agent created", zap.String("id", id), zap.String("name", req.Name))

	c.JSON(http.StatusCreated, model.AgentResponse{
		ID:            id,
		Name:          req.Name,
		Description:   req.Description,
		Persona:       req.Persona,
		SystemPrompt:  req.SystemPrompt,
		LLMModel:      req.LLMModel,
		MaxIterations: req.MaxIterations,
		AllowedSkills: req.AllowedSkills,
		CreatedAt:     time.Now().Format(time.RFC3339),
	})
}

// GetAgent handles retrieving a specific agent
func (h *AgentHandler) GetAgent(c *gin.Context) {
	id := c.Param("id")
	agent, exists := h.registry.Get(id)
	if !exists {
		h.logger.Warn("agent not found", zap.String("id", id))
		c.JSON(http.StatusNotFound, model.ErrorResponse{
			Code:    http.StatusNotFound,
			Message: "agent not found",
		})
		return
	}

	c.JSON(http.StatusOK, model.AgentResponse{
		ID:            agent.ID,
		Name:          agent.Name,
		Description:   agent.Description,
		Persona:       agent.Persona,
		SystemPrompt:  agent.SystemPrompt,
		LLMModel:      agent.LLMModel,
		MaxIterations: agent.MaxIterations,
		AllowedSkills: agent.AllowedSkills,
		CreatedAt:     time.Now().Format(time.RFC3339),
	})
}

// ListAgents handles listing all agents
func (h *AgentHandler) ListAgents(c *gin.Context) {
	agents := h.registry.GetAll()

	var agentResponses []model.AgentResponse
	for _, a := range agents {
		agentResponses = append(agentResponses, model.AgentResponse{
			ID:            a.ID,
			Name:          a.Name,
			Description:   a.Description,
			Persona:       a.Persona,
			SystemPrompt:  a.SystemPrompt,
			LLMModel:      a.LLMModel,
			MaxIterations: a.MaxIterations,
			AllowedSkills: a.AllowedSkills,
			CreatedAt:     time.Now().Format(time.RFC3339),
		})
	}

	c.JSON(http.StatusOK, gin.H{"agents": agentResponses})
}

// ExecuteAgent handles executing an agent with a task
func (h *AgentHandler) ExecuteAgent(c *gin.Context) {
	id := c.Param("id")
	var req model.ExecuteAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid agent execution request", zap.Error(err))
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: err.Error(),
		})
		return
	}

	agt, exists := h.registry.Get(id)
	if !exists {
		h.logger.Warn("agent not found", zap.String("id", id))
		c.JSON(http.StatusNotFound, model.ErrorResponse{
			Code:    http.StatusNotFound,
			Message: "agent not found",
		})
		return
	}

	task := &agent.AgentTask{
		Query:     req.Query,
		Context:   req.Context,
		Variables: req.Variables,
	}

	result, err := agt.Execute(c.Request.Context(), task)
	if err != nil {
		h.logger.Error("agent execution failed", zap.String("id", id), zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ExecuteAgentResponse{
			Error: err.Error(),
		})
		return
	}

	h.logger.Info("agent executed", zap.String("id", id))
	c.JSON(http.StatusOK, model.ExecuteAgentResponse{
		Result: result.Result,
		Steps:  convertStepsToModel(result.Steps),
		Status: result.Status,
		Error:  result.Error,
	})
}

// Helper function to convert internal steps to model steps
func convertStepsToModel(steps []agent.AgentStep) []model.AgentStep {
	modelSteps := make([]model.AgentStep, len(steps))
	for i, step := range steps {
		modelSteps[i] = model.AgentStep{
			Iteration: step.Iteration,
			Action:    step.Action,
			Tool:      step.Tool,
			Input:     step.Input,
			Output:    step.Output,
		}
	}
	return modelSteps
}