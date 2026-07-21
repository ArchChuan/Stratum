// Package handler — agent_crud_handler.go.
//
// CRUD HTTP transport for /agents. Each handler binds → calls
// AgentService → renders. No registry, repo, or SQL knowledge here.
package handler

import (
	"net/http"
	"strconv"

	"github.com/byteBuilderX/stratum/api/middleware"
	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/gin-gonic/gin"
)

func (h *AgentHandler) GetAllAgents(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	dtos, err := h.svc.List(c.Request.Context())
	if err != nil {
		_ = c.Error(err)
		return
	}
	responses := make([]AgentResponse, 0, len(dtos))
	for _, d := range dtos {
		responses = append(responses, dtoToResponse(d))
	}
	c.JSON(http.StatusOK, gin.H{"agents": responses})
}

func (h *AgentHandler) GetAgent(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	dto, err := h.svc.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dtoToResponse(dto))
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
	dto, err := h.svc.Create(c.Request.Context(), agent.CreateAgentInput{
		TenantID:              tenantID,
		Name:                  req.Name,
		Type:                  req.Type,
		Description:           req.Description,
		SystemPrompt:          req.SystemPrompt,
		LLMModel:              req.LLMModel,
		EmbedModel:            req.EmbedModel,
		MaxIterations:         req.MaxIterations,
		MaxContextTokens:      req.MaxContextTokens,
		AllowedSkills:         req.AllowedSkills,
		MCPToolIDs:            req.MCPToolIDs,
		KnowledgeWorkspaceIDs: req.KnowledgeWorkspaceIDs,
		MemoryScope:           req.MemoryScope,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, dtoToResponse(dto))
}

func (h *AgentHandler) UpdateAgent(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	var req UpdateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	dto, err := h.svc.Update(c.Request.Context(), c.Param("id"), agent.UpdateAgentInput{
		Name:                  req.Name,
		Type:                  req.Type,
		Description:           req.Description,
		SystemPrompt:          req.SystemPrompt,
		LLMModel:              req.LLMModel,
		MaxIterations:         req.MaxIterations,
		MaxContextTokens:      req.MaxContextTokens,
		AllowedSkills:         req.AllowedSkills,
		MCPToolIDs:            req.MCPToolIDs,
		KnowledgeWorkspaceIDs: req.KnowledgeWorkspaceIDs,
		MemoryScope:           req.MemoryScope,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dtoToResponse(dto))
}

func (h *AgentHandler) DeleteAgent(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	if err := h.svc.Delete(c.Request.Context(), tenantID, c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "agent deleted successfully"})
}

// executionRow is the wire shape rendered by ListExecutions. JSON tags
// are frozen by the contract test; do not rename.
type executionRow struct {
	ID            string `json:"id"`
	TraceID       string `json:"trace_id"`
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

func (h *AgentHandler) ListExecutions(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	rows, total, err := h.svc.ListExecutions(c.Request.Context(), tenantID, page, pageSize)
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]executionRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, executionRow{
			ID:            r.ID,
			TraceID:       r.TraceID,
			AgentID:       r.AgentID,
			AgentName:     r.AgentName,
			UserID:        r.UserID,
			Status:        r.Status,
			InputPreview:  r.InputPreview,
			OutputPreview: r.OutputPreview,
			ErrorMessage:  r.ErrorMessage,
			TotalTokens:   r.TotalTokens,
			DurationMs:    r.DurationMs,
			CreatedAt:     r.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"executions": out, "total": total})
}

func (h *AgentHandler) ListExecutionToolTraces(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	traceID := c.Param("traceID")
	rows, err := h.svc.ListToolTraces(c.Request.Context(), tenantID, traceID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"tool_traces": rows})
}

func (h *AgentHandler) ListExecutionTraceEvents(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	traceID := c.Param("traceID")
	rows, err := h.svc.ListTraceEvents(c.Request.Context(), tenantID, traceID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"trace_events": rows})
}
