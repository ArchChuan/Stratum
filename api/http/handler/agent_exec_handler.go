package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/agent"
	"github.com/byteBuilderX/stratum/internal/capgateway"
	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (h *AgentHandler) ExecuteAgent(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	id := c.Param("id")
	a, ok := h.agentRegistry.Get(c.Request.Context(), id)
	if !ok {
		h.logger.Warn("agent not found", zap.String("id", id))
		c.JSON(http.StatusNotFound, dto.ErrorResponse{
			Code:    http.StatusNotFound,
			Message: "agent not found",
		})
		return
	}

	var req ExecuteAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid request", zap.Error(err))
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{
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
	if h.chatStore != nil {
		type chatStoreSetter interface {
			SetChatStore(agent.ChatStore)
		}
		if setter, ok := a.(chatStoreSetter); ok {
			setter.SetChatStore(h.chatStore)
		}
	}
	options = append(options, agent.WithTenantID(tenantID))
	options = append(options, agent.WithTraceID(middleware.GetTraceID(c)))
	if req.ConversationID != "" {
		options = append(options, agent.WithConversationID(req.ConversationID))
		options = append(options, agent.WithHistoryWindow(20))
	}
	options = append(options, agent.WithUserID(userID))

	options = append(options, agent.WithExtraTools(h.buildExtraTools(c.Request.Context(), a.GetConfig().MCPServerIDs, a.GetConfig().AllowedSkills)))

	if h.ragService != nil && len(a.GetConfig().KnowledgeWorkspaceIDs) > 0 {
		rs := h.ragService
		options = append(options, agent.WithRAGSearchFn(buildRAGSearchFn(rs, tenantID)))
	}

	execCtx, execCancel := context.WithTimeout(context.Background(), constants.AgentExecTimeout)
	defer execCancel()

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
		c.JSON(http.StatusNotFound, dto.ErrorResponse{
			Code:    http.StatusNotFound,
			Message: "agent not found",
		})
		return
	}

	var req ExecuteAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid request", zap.Error(err))
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{
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
	if h.chatStore != nil {
		type chatStoreSetter interface {
			SetChatStore(agent.ChatStore)
		}
		if setter, ok := a.(chatStoreSetter); ok {
			setter.SetChatStore(h.chatStore)
		}
	}
	options = append(options, agent.WithTenantID(tenantID))
	options = append(options, agent.WithTraceID(middleware.GetTraceID(c)))
	if req.ConversationID != "" {
		options = append(options, agent.WithConversationID(req.ConversationID))
		options = append(options, agent.WithHistoryWindow(20))
	}
	options = append(options, agent.WithUserID(userID))

	options = append(options, agent.WithExtraTools(h.buildExtraTools(c.Request.Context(), a.GetConfig().MCPServerIDs, a.GetConfig().AllowedSkills)))

	if h.ragService != nil && len(a.GetConfig().KnowledgeWorkspaceIDs) > 0 {
		rs := h.ragService
		options = append(options, agent.WithRAGSearchFn(buildRAGSearchFn(rs, tenantID)))
	}

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

	// Send an initial heartbeat immediately so Cloudflare sees response body
	// data and resets its proxy read timeout before agent execution begins.
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

	// Periodic heartbeat keeps the Cloudflare proxy read timeout alive when
	// the LLM is silent between tokens (e.g. long first-token latency).
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

func buildRAGSearchFn(rs *knowledge.RAGService, tenantID string) func(ctx context.Context, workspaces []string, query string, topK int) (string, error) {
	return func(ctx context.Context, workspaces []string, query string, topK int) (string, error) {
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
	}
}
