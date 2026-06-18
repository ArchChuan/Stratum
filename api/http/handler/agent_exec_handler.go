package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/byteBuilderX/stratum/api/middleware"
	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ExecuteAgent runs an agent synchronously and returns the full result.
func (h *AgentHandler) ExecuteAgent(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	id := c.Param("id")
	a, ok := h.agentRegistry.Get(c.Request.Context(), id)
	if !ok {
		_ = c.Error(agent.ErrNotFound)
		return
	}
	var req ExecuteAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}

	userID, _ := userIDFromCtx(c)
	_, options := h.assembleOptions(c, a, req, tenantID, userID, false)

	execCtx, execCancel := context.WithTimeout(context.Background(), constants.AgentExecTimeout)
	defer execCancel()

	start := time.Now()
	result, err := a.Execute(execCtx, req.Query, options...)
	durationMs := int(time.Since(start).Milliseconds())

	h.recordExecution(c.Request.Context(), id, userID, a.GetConfig().Name, req.Query, result, err, durationMs)

	if err != nil {
		h.logger.Error("agent execution failed", zap.String("agentId", id), zap.Error(err))
		c.JSON(http.StatusOK, AgentExecutionResult{
			AgentID: id, Input: req.Query, Output: "", Steps: 0, Duration: "0s", Error: err.Error(),
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

// ExecuteAgentStream runs an agent and streams tokens via SSE.
func (h *AgentHandler) ExecuteAgentStream(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	id := c.Param("id")
	a, ok := h.agentRegistry.Get(c.Request.Context(), id)
	if !ok {
		_ = c.Error(agent.ErrNotFound)
		return
	}
	var req ExecuteAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}

	userID, _ := userIDFromCtx(c)
	streamCtx, options := h.assembleOptions(c, a, req, tenantID, userID, true)

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
	fmt.Fprint(c.Writer, ": heartbeat\n\n") //nolint:errcheck
	if hasFlusher {
		flusher.Flush()
	}

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
	go func() {
		ticker := time.NewTicker(constants.SSEHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				fmt.Fprint(c.Writer, ": heartbeat\n\n") //nolint:errcheck
				if hasFlusher {
					flusher.Flush()
				}
			case <-execCtx.Done():
				return
			case <-clientCtx.Done():
				return
			}
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

	h.recordExecution(c.Request.Context(), id, userID, a.GetConfig().Name, req.Query, result, err, durationMs)

	if err != nil {
		if errors.Is(err, context.Canceled) && clientCtx.Err() != nil {
			return
		}
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

// assembleOptions builds the ExecutionOption slice shared by sync + stream paths.
// When stream is true, returns a context that carries the per-tenant LLM completer
// (used by streaming RAG / inner LLM calls); otherwise returns the request context unchanged.
func (h *AgentHandler) assembleOptions(
	c *gin.Context, a agent.Agent, req ExecuteAgentRequest, tenantID, userID string, stream bool,
) (context.Context, []agent.ExecutionOption) {
	options := []agent.ExecutionOption{agent.WithMaxSteps(a.GetConfig().MaxIterations)}
	options = append(options, optionsFromRequest(req)...)

	ctx := c.Request.Context()
	if h.tenantResolver != nil {
		if capGW, apiKeys, ok := h.tenantResolver.Resolve(ctx, tenantID); ok {
			if stream {
				ctx = h.tenantResolver.InjectCompleter(ctx, tenantID)
			}
			type capGWSetter interface {
				SetCapGateway(port.CapabilityGateway)
			}
			if setter, ok := a.(capGWSetter); ok {
				setter.SetCapGateway(capGW)
			}
			if len(apiKeys) > 0 {
				options = append(options, agent.WithLLMAPIKeys(apiKeys))
			}
		}
	}
	h.attachChatStore(a)

	options = append(options,
		agent.WithTenantID(tenantID),
		agent.WithTraceID(middleware.GetTraceID(c)),
		agent.WithUserID(userID),
	)
	if req.ConversationID != "" {
		options = append(options,
			agent.WithConversationID(req.ConversationID),
			agent.WithHistoryWindow(20),
		)
	}
	options = append(options, agent.WithExtraTools(
		h.buildExtraTools(c.Request.Context(), a.GetConfig().MCPServerIDs, a.GetConfig().AllowedSkills),
	))
	if h.ragService != nil && len(a.GetConfig().KnowledgeWorkspaceIDs) > 0 {
		options = append(options, agent.WithRAGSearchFn(knowledge.NewRAGSearchFn(h.ragService, tenantID)))
	}
	return ctx, options
}

func optionsFromRequest(req ExecuteAgentRequest) []agent.ExecutionOption {
	if req.Options == nil {
		return nil
	}
	var out []agent.ExecutionOption
	if maxSteps, ok := req.Options["maxSteps"].(float64); ok {
		out = append(out, agent.WithMaxSteps(int(maxSteps)))
	}
	if timeout, ok := req.Options["timeout"].(float64); ok {
		out = append(out, agent.WithTimeout(time.Duration(timeout)*time.Second))
	}
	return out
}

func (h *AgentHandler) attachChatStore(a agent.Agent) {
	if h.chatStore == nil {
		return
	}
	type chatStoreSetter interface {
		SetChatStore(agent.ChatStore)
	}
	if setter, ok := a.(chatStoreSetter); ok {
		setter.SetChatStore(h.chatStore)
	}
}

// recordExecution fire-and-forget inserts a per-tenant execution record.
func (h *AgentHandler) recordExecution(
	reqCtx context.Context, id, userID, agentName, query string,
	result *agent.AgentResult, err error, durationMs int,
) {
	if h.executionStore == nil {
		return
	}
	tc, _ := tenantdb.FromContext(reqCtx)
	rec := agent.ExecutionRecord{
		AgentID:      id,
		UserID:       userID,
		AgentName:    agentName,
		InputPreview: truncate(query, previewMaxChars),
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
