package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/api/model"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/agent"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/capgateway"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/knowledge"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/mcp"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/orchestrator"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/constants"
	pkgcrypto "github.com/byteBuilderX/ClawHermes-AI-Go/pkg/crypto"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/observability"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const previewMaxChars = 50

type AgentHandler struct {
	agentRegistry  *agent.Registry
	logger         *zap.Logger
	gateway        *llmgateway.Gateway
	metrics        observability.MetricsProvider
	executionStore *agent.ExecutionStore
	db             PgxPool
	aesKey         [32]byte
	gatewayCache   *llmgateway.TenantGatewayCache
	ragService     *knowledge.RAGService
	mcpRegistry    *mcp.MCPSkillRegistry
	skillAdapter   capgateway.Adapter
	skillRegistry  *orchestrator.Registry
}

type CreateAgentRequest struct {
	Name                  string   `json:"name" binding:"required"`
	Type                  string   `json:"type"`
	Description           string   `json:"description"`
	Persona               string   `json:"persona"`
	SystemPrompt          string   `json:"systemPrompt"`
	LLMModel              string   `json:"llmModel" binding:"required"`
	MaxIterations         int      `json:"maxIterations" binding:"required"`
	AllowedSkills         []string `json:"allowedSkills"`
	MCPServerIDs          []string `json:"mcpServerIds"`
	KnowledgeWorkspaceIDs []string `json:"knowledgeWorkspaceIds"`
}

type AgentResponse struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Type                  string   `json:"type"`
	Description           string   `json:"description"`
	Persona               string   `json:"persona"`
	SystemPrompt          string   `json:"systemPrompt"`
	LLMModel              string   `json:"llmModel"`
	MaxIterations         int      `json:"maxIterations"`
	AllowedSkills         []string `json:"allowedSkills"`
	MCPServerIDs          []string `json:"mcpServerIds"`
	KnowledgeWorkspaceIDs []string `json:"knowledgeWorkspaceIds"`
	CreatedAt             string   `json:"createdAt"`
}

type ExecuteAgentRequest struct {
	Query          string                 `json:"query"`
	ConversationID string                 `json:"conversation_id"`
	UserID         string                 `json:"user_id"`
	Context        map[string]interface{} `json:"context"`
	Options        map[string]interface{} `json:"options"`
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

func NewAgentHandler(
	agentRegistry *agent.Registry,
	logger *zap.Logger,
	gateway *llmgateway.Gateway,
	metrics observability.MetricsProvider,
	execStore *agent.ExecutionStore,
	db PgxPool,
	aesKey [32]byte,
	gatewayCache *llmgateway.TenantGatewayCache,
	ragService *knowledge.RAGService,
	mcpRegistry *mcp.MCPSkillRegistry,
	skillAdapter capgateway.Adapter,
	skillRegistry *orchestrator.Registry,
) *AgentHandler {
	return &AgentHandler{
		agentRegistry:  agentRegistry,
		logger:         logger,
		gateway:        gateway,
		metrics:        metrics,
		executionStore: execStore,
		db:             db,
		aesKey:         aesKey,
		gatewayCache:   gatewayCache,
		ragService:     ragService,
		mcpRegistry:    mcpRegistry,
		skillAdapter:   skillAdapter,
		skillRegistry:  skillRegistry,
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
			ID:                    cfg.ID,
			Name:                  cfg.Name,
			Type:                  string(cfg.Type),
			Description:           cfg.Description,
			Persona:               cfg.Persona,
			SystemPrompt:          cfg.SystemPrompt,
			LLMModel:              cfg.LLMModel,
			MaxIterations:         cfg.MaxIterations,
			AllowedSkills:         cfg.AllowedSkills,
			MCPServerIDs:          cfg.MCPServerIDs,
			KnowledgeWorkspaceIDs: cfg.KnowledgeWorkspaceIDs,
			CreatedAt:             time.Now().Format(time.RFC3339),
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
		ID:                    cfg.ID,
		Name:                  cfg.Name,
		Type:                  string(cfg.Type),
		Description:           cfg.Description,
		Persona:               cfg.Persona,
		SystemPrompt:          cfg.SystemPrompt,
		LLMModel:              cfg.LLMModel,
		MaxIterations:         cfg.MaxIterations,
		AllowedSkills:         cfg.AllowedSkills,
		MCPServerIDs:          cfg.MCPServerIDs,
		KnowledgeWorkspaceIDs: cfg.KnowledgeWorkspaceIDs,
		CreatedAt:             time.Now().Format(time.RFC3339),
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
		ID:                    id,
		Name:                  req.Name,
		Type:                  agentType,
		Description:           req.Description,
		Persona:               req.Persona,
		SystemPrompt:          req.SystemPrompt,
		LLMModel:              req.LLMModel,
		MaxIterations:         req.MaxIterations,
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
		MaxIterations:         req.MaxIterations,
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

	var req CreateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid request", zap.Error(err))
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: err.Error(),
		})
		return
	}

	if _, ok := h.agentRegistry.Get(c.Request.Context(), id); !ok {
		c.JSON(http.StatusNotFound, model.ErrorResponse{
			Code:    http.StatusNotFound,
			Message: "agent not found",
		})
		return
	}

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
		MaxIterations:         req.MaxIterations,
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
		MaxIterations:         req.MaxIterations,
		AllowedSkills:         skills,
		MCPServerIDs:          req.MCPServerIDs,
		KnowledgeWorkspaceIDs: req.KnowledgeWorkspaceIDs,
		CreatedAt:             time.Now().Format(time.RFC3339),
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

	reqCtx := c.Request.Context()
	tenantID, _ := tenantIDFromCtx(c)
	userID, _ := userIDFromCtx(c)

	if tenantGW, apiKeys := h.resolveTenantGateway(reqCtx, tenantID); tenantGW != nil {
		llmAdapter := capgateway.NewLLMAdapter(tenantGW, h.logger)
		capGW := capgateway.NewDefaultCapabilityGateway(llmAdapter, h.skillAdapter, h.logger)
		type capGWSetter interface {
			SetCapGateway(capgateway.CapabilityGateway)
		}
		if setter, ok := a.(capGWSetter); ok {
			setter.SetCapGateway(capGW)
		}
		if len(apiKeys) > 0 {
			options = append(options, agent.WithLLMAPIKeys(apiKeys))
		}
	}
	options = append(options, agent.WithTenantID(tenantID))
	if req.ConversationID != "" {
		options = append(options, agent.WithConversationID(req.ConversationID))
	}
	options = append(options, agent.WithUserID(userID))

	// build extra tools from MCPServerIDs and AllowedSkills
	options = append(options, agent.WithExtraTools(h.buildExtraTools(a.GetConfig().MCPServerIDs, a.GetConfig().AllowedSkills)))

	if h.ragService != nil && len(a.GetConfig().KnowledgeWorkspaceIDs) > 0 {
		rs := h.ragService
		options = append(options, agent.WithRAGSearchFn(func(ctx context.Context, workspaces []string, query string, topK int) (string, error) {
			type wsResult struct {
				content string
				err     error
			}
			results := make([]wsResult, len(workspaces))
			var wg sync.WaitGroup
			for i, ws := range workspaces {
				wg.Add(1)
				go func(i int, ws string) {
					defer wg.Done()
					result, err := rs.Query(ctx, knowledge.RAGQueryRequest{
						Workspace: ws,
						Question:  query,
						TenantID:  tenantID,
						Mode:      "vector",
						TopK:      topK,
					})
					if err != nil {
						results[i] = wsResult{err: err}
						return
					}
					var sb strings.Builder
					for _, src := range result.Sources {
						sb.WriteString(src.Content)
						sb.WriteString("\n---\n")
					}
					results[i] = wsResult{content: sb.String()}
				}(i, ws)
			}
			wg.Wait()
			var combined strings.Builder
			var firstErr error
			for _, r := range results {
				if r.err != nil && firstErr == nil {
					firstErr = r.err
					continue
				}
				combined.WriteString(r.content)
			}
			if combined.Len() == 0 && firstErr != nil {
				return "", firstErr
			}
			return combined.String(), nil
		}))
	}

	// use an independent context so client disconnects don't cancel in-flight LLM calls
	execCtx, execCancel := context.WithTimeout(context.Background(), constants.AgentExecTimeout)
	defer execCancel()

	start := time.Now()
	result, err := a.Execute(execCtx, req.Query, options...)
	durationMs := int(time.Since(start).Milliseconds())

	// fire-and-forget execution record
	if h.executionStore != nil {
		tc, _ := tenantdb.FromContext(c.Request.Context())
		rec := agent.ExecutionRecord{
			AgentID:      id,
			UserID:       userID,
			AgentName:    a.GetConfig().Name,
			InputPreview: truncate(req.Query, previewMaxChars),
			DurationMs:   durationMs,
		}
		if err != nil {
			rec.Status = "error"
			rec.ErrorMessage = err.Error()
		} else {
			rec.Status = "success"
			rec.OutputPreview = truncate(result.Output, previewMaxChars)
			rec.TotalTokens = result.TokensUsed
		}
		insertCtx := tenantdb.WithTenant(context.Background(), tc)
		go func() {
			if insertErr := h.executionStore.Insert(insertCtx, rec); insertErr != nil {
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

func (h *AgentHandler) ExecuteAgentStream(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
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

	userID, _ := userIDFromCtx(c)
	streamCtx := c.Request.Context()
	if tenantGW, apiKeys := h.resolveTenantGateway(streamCtx, tenantID); tenantGW != nil {
		streamCtx = llmgateway.WithGateway(streamCtx, tenantGW)
		llmAdapter := capgateway.NewLLMAdapter(tenantGW, h.logger)
		capGW := capgateway.NewDefaultCapabilityGateway(llmAdapter, h.skillAdapter, h.logger)
		type capGWSetter interface {
			SetCapGateway(capgateway.CapabilityGateway)
		}
		if setter, ok := a.(capGWSetter); ok {
			setter.SetCapGateway(capGW)
		}
		if len(apiKeys) > 0 {
			options = append(options, agent.WithLLMAPIKeys(apiKeys))
		}
	}
	options = append(options, agent.WithTenantID(tenantID))
	if req.ConversationID != "" {
		options = append(options, agent.WithConversationID(req.ConversationID))
	}
	options = append(options, agent.WithUserID(userID))

	// build extra tools from MCPServerIDs and AllowedSkills
	options = append(options, agent.WithExtraTools(h.buildExtraTools(a.GetConfig().MCPServerIDs, a.GetConfig().AllowedSkills)))

	if h.ragService != nil && len(a.GetConfig().KnowledgeWorkspaceIDs) > 0 {
		rs := h.ragService
		options = append(options, agent.WithRAGSearchFn(func(ctx context.Context, workspaces []string, query string, topK int) (string, error) {
			type wsResult struct {
				content string
				err     error
			}
			results := make([]wsResult, len(workspaces))
			var wg sync.WaitGroup
			for i, ws := range workspaces {
				wg.Add(1)
				go func(i int, ws string) {
					defer wg.Done()
					result, err := rs.Query(ctx, knowledge.RAGQueryRequest{
						Workspace: ws,
						Question:  query,
						TenantID:  tenantID,
						Mode:      "vector",
						TopK:      topK,
					})
					if err != nil {
						results[i] = wsResult{err: err}
						return
					}
					var sb strings.Builder
					for _, src := range result.Sources {
						sb.WriteString(src.Content)
						sb.WriteString("\n---\n")
					}
					results[i] = wsResult{content: sb.String()}
				}(i, ws)
			}
			wg.Wait()
			var combined strings.Builder
			var firstErr error
			for _, r := range results {
				if r.err != nil && firstErr == nil {
					firstErr = r.err
					continue
				}
				combined.WriteString(r.content)
			}
			if combined.Len() == 0 && firstErr != nil {
				return "", firstErr
			}
			return combined.String(), nil
		}))
	}

	// SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Transfer-Encoding", "chunked")

	flusher, hasFlusher := c.Writer.(http.Flusher)

	writeEvent := func(data string) {
		fmt.Fprintf(c.Writer, "data: %s\n\n", data) //nolint:errcheck
		if hasFlusher {
			flusher.Flush()
		}
	}

	// client disconnect detection — cancel execution when client disconnects
	clientCtx := c.Request.Context()
	execCtx, execCancel := context.WithTimeout(streamCtx, constants.AgentExecTimeout)
	defer execCancel()
	go func() {
		select {
		case <-clientCtx.Done():
			execCancel()
		case <-execCtx.Done():
		}
	}()

	options = append(options, agent.WithTokenCallback(func(token string) {
		if execCtx.Err() != nil {
			return
		}
		payload, _ := json.Marshal(map[string]string{"token": token})
		writeEvent(string(payload))
	}))

	start := time.Now()
	result, err := a.Execute(execCtx, req.Query, options...)
	durationMs := int(time.Since(start).Milliseconds())

	if h.executionStore != nil {
		tc, _ := tenantdb.FromContext(c.Request.Context())
		rec := agent.ExecutionRecord{
			AgentID:      id,
			UserID:       userID,
			AgentName:    a.GetConfig().Name,
			InputPreview: truncate(req.Query, previewMaxChars),
			DurationMs:   durationMs,
		}
		if err != nil {
			rec.Status = "error"
			rec.ErrorMessage = err.Error()
		} else {
			rec.Status = "success"
			rec.OutputPreview = truncate(result.Output, previewMaxChars)
			rec.TotalTokens = result.TokensUsed
		}
		insertCtx := tenantdb.WithTenant(context.Background(), tc)
		go func() {
			if insertErr := h.executionStore.Insert(insertCtx, rec); insertErr != nil {
				h.logger.Warn("execution record insert failed", zap.Error(insertErr))
			}
		}()
	}

	if err != nil {
		h.logger.Error("agent stream execution failed", zap.String("agentId", id), zap.Error(err))
		payload, _ := json.Marshal(map[string]interface{}{"error": err.Error()})
		writeEvent(string(payload))
		return
	}

	donePayload, _ := json.Marshal(map[string]interface{}{
		"done":       true,
		"output":     result.Output,
		"steps":      result.Steps,
		"tokensUsed": result.TokensUsed,
		"duration":   result.Duration.String(),
	})
	writeEvent(string(donePayload))
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
// GET /agents/executions?page=1&page_size=20
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

// resolveTenantGateway looks up per-tenant LLM API keys from DB settings,
// decrypts them, builds a Gateway, and caches with 5-minute TTL.
// Returns (nil, nil) when no keys are configured or on any error (fallback to global gateway).
func (h *AgentHandler) resolveTenantGateway(ctx context.Context, tenantID string) (*llmgateway.Gateway, map[string]string) {
	if h.db == nil || h.gatewayCache == nil {
		return nil, nil
	}
	if gw, keys, ok := h.gatewayCache.Get(tenantID); ok {
		return gw, keys
	}

	var settingsJSON []byte
	err := h.db.QueryRow(ctx,
		"SELECT settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL",
		tenantID,
	).Scan(&settingsJSON)
	if err != nil {
		h.logger.Warn("resolveTenantGateway: settings query failed",
			zap.String("tenant_id", tenantID), zap.Error(err))
		return nil, nil
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(settingsJSON, &settings); err != nil {
		return nil, nil
	}

	apiKeysRaw, ok := settings["llm_api_keys"].(map[string]interface{})
	if !ok || len(apiKeysRaw) == 0 {
		return nil, nil
	}

	decrypted := make(map[string]string, len(apiKeysRaw))
	for provider, enc := range apiKeysRaw {
		encStr, ok := enc.(string)
		if !ok || encStr == "" {
			continue
		}
		plain, err := pkgcrypto.Decrypt(h.aesKey, encStr)
		if err != nil {
			h.logger.Warn("resolveTenantGateway: decrypt failed",
				zap.String("tenant_id", tenantID), zap.String("provider", provider))
			continue
		}
		decrypted[provider] = plain
	}

	if len(decrypted) == 0 {
		return nil, nil
	}

	gw := llmgateway.NewGateway()
	if qwenKey, ok := decrypted["qwen"]; ok {
		qwenClient := llmgateway.NewQwenClient(qwenKey, h.logger)
		gw.RegisterClient(llmgateway.ProviderQwen, qwenClient)
		gw.RegisterEmbeddingClient(llmgateway.ProviderQwen, qwenClient)
	}
	if zhipuKey, ok := decrypted["zhipu"]; ok {
		zhipuClient := llmgateway.NewZhipuClient(zhipuKey, h.logger)
		gw.RegisterClient(llmgateway.ProviderZhipu, zhipuClient)
		gw.RegisterEmbeddingClient(llmgateway.ProviderZhipu, zhipuClient)
	}

	// set default to first available provider
	for _, pref := range []llmgateway.ModelProvider{llmgateway.ProviderQwen, llmgateway.ProviderZhipu} {
		if _, ok := decrypted[string(pref)]; ok {
			gw.SetDefault(pref)
			break
		}
	}

	h.gatewayCache.Set(tenantID, gw, decrypted, constants.GatewayCacheTTL)
	return gw, decrypted
}

// buildExtraTools converts MCPServerIDs and AllowedSkills into ToolDefinitions for the ReAct loop.
func (h *AgentHandler) buildExtraTools(mcpServerIDs, allowedSkills []string) []capgateway.ToolDefinition {
	var tools []capgateway.ToolDefinition

	for _, serverID := range mcpServerIDs {
		if h.mcpRegistry == nil {
			continue
		}
		adapter := h.mcpRegistry.GetAdapterForServer(serverID)
		if adapter == nil {
			continue
		}
		for _, s := range adapter.GetAllSkills() {
			w, ok := s.(*mcp.MCPSkillWrapper)
			if !ok {
				continue
			}
			tools = append(tools, capgateway.ToolDefinition{
				Name:        w.GetID(),
				Description: w.Tool.Description,
				InputSchema: w.Tool.InputSchema,
			})
		}
	}

	for _, skillID := range allowedSkills {
		name := skillID
		description := skillID
		if h.skillRegistry != nil {
			if s, ok := h.skillRegistry.Get(skillID); ok {
				name = s.GetName()
				description = s.GetDescription()
			}
		}
		tools = append(tools, capgateway.ToolDefinition{
			Name:        skillID,
			Description: name + ": " + description,
			InputSchema: map[string]interface{}{"type": "object"},
		})
	}

	return tools
}
