// Package handler — agent_exec_handler.go.
//
// Transport layer for /agents/:id/execute and /execute/stream. SSE
// mechanics (heartbeat, client-cancel watcher, token writer) live here;
// orchestration (registry lookup, capability injection, recording)
// lives in agent.AgentService.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/byteBuilderX/stratum/api/middleware"
	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
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
	var req ExecuteAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	userID, _ := userIDFromCtx(c)

	result, _, err := h.svc.Execute(c.Request.Context(), id, agent.ExecRequest{
		Query:          req.Query,
		ConversationID: req.ConversationID,
		UserID:         userID,
		MaxSteps:       intOption(req.Options, "maxSteps"),
		Timeout:        timeoutOption(req.Options, "timeout"),
	}, agent.ExecMeta{
		TenantID: tenantID,
		TraceID:  middleware.GetTraceID(c),
	})

	if err != nil {
		var approvalErr *agentport.ToolApprovalRequiredError
		if errors.As(err, &approvalErr) {
			c.JSON(http.StatusAccepted, gin.H{"status": "waiting_approval", "approvalId": approvalErr.ApprovalID, "toolCallId": approvalErr.ToolCallID, "serverId": approvalErr.ServerID, "toolName": approvalErr.ToolName, "riskLevel": approvalErr.RiskLevel})
			return
		}
		if errors.Is(err, agent.ErrNotFound) {
			_ = c.Error(err)
			return
		}
		h.logger.Error("agent execution failed", zap.String("agentId", id), zap.Error(err))
		respondAgentExecutionError(c, err)
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

func respondAgentExecutionError(c *gin.Context, err error) {
	_ = c.Error(err)
}

// ExecuteAgentStream runs an agent and streams tokens via SSE.
func (h *AgentHandler) ExecuteAgentStream(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	id := c.Param("id")
	var req ExecuteAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	userID, _ := userIDFromCtx(c)

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Transfer-Encoding", "chunked")

	writer := newSSEEventWriter(c.Writer)

	clientCtx := c.Request.Context()
	tokenCb := func(token string) {
		payload, _ := json.Marshal(map[string]string{"token": token})
		writer.EnqueueData(string(payload))
	}

	execCtx, cancel, run, err := h.svc.ExecuteStream(clientCtx, id, agent.ExecRequest{
		Query:          req.Query,
		ConversationID: req.ConversationID,
		UserID:         userID,
		MaxSteps:       intOption(req.Options, "maxSteps"),
		Timeout:        timeoutOption(req.Options, "timeout"),
	}, agent.ExecMeta{
		TenantID: tenantID,
		TraceID:  middleware.GetTraceID(c),
		Stream:   true,
	}, tokenCb)
	if err != nil {
		_ = c.Error(err)
		return
	}
	defer cancel()
	writer.EnqueueComment("heartbeat")
	go func() {
		ticker := time.NewTicker(constants.SSEHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				writer.EnqueueComment("heartbeat")
			case <-execCtx.Done():
				return
			case <-clientCtx.Done():
				cancel()
				return
			}
		}
	}()

	go func() {
		defer writer.Close()
		result, _, runErr := run()
		if runErr != nil {
			if errors.Is(runErr, context.Canceled) && clientCtx.Err() != nil {
				return
			}
			var approvalErr *agentport.ToolApprovalRequiredError
			if errors.As(runErr, &approvalErr) {
				writer.EnqueueEvent("approval_required", string(approvalRequiredSSEPayload(approvalErr)))
				return
			}
			h.logger.Error("agent stream execution failed", zap.String("agentId", id), zap.Error(runErr))
			payload, _ := json.Marshal(map[string]interface{}{"error": runErr.Error()})
			writer.EnqueueData(string(payload))
			return
		}
		donePayload, _ := json.Marshal(map[string]interface{}{
			"done":       true,
			"output":     result.Output,
			"steps":      result.Steps,
			"tokensUsed": result.TokensUsed,
			"duration":   result.Duration.String(),
		})
		writer.EnqueueData(string(donePayload))
	}()

	writer.WriteUntilClosed(0)
}

func approvalRequiredSSEPayload(approval *agentport.ToolApprovalRequiredError) []byte {
	payload, _ := json.Marshal(map[string]any{
		"status": "waiting_approval", "approvalId": approval.ApprovalID,
		"toolCallId": approval.ToolCallID, "serverId": approval.ServerID,
		"toolName": approval.ToolName, "riskLevel": approval.RiskLevel,
	})
	return payload
}

// intOption pulls a numeric option from req.Options. Returns 0 when
// missing or wrong type — service treats 0 as "use default".
func intOption(opts map[string]interface{}, key string) int {
	if opts == nil {
		return 0
	}
	if v, ok := opts[key].(float64); ok {
		return int(v)
	}
	return 0
}

// timeoutOption pulls a duration option (in seconds) from req.Options.
// Returns 0 when missing — service treats 0 as "use default".
func timeoutOption(opts map[string]interface{}, key string) time.Duration {
	if opts == nil {
		return 0
	}
	if v, ok := opts[key].(float64); ok {
		return time.Duration(v) * time.Second
	}
	return 0
}
