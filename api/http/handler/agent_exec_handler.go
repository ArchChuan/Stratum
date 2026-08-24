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
	agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
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
		TenantID:    tenantID,
		TraceID:     middleware.GetTraceID(c),
		ExecutionID: req.ExecutionID,
	})

	if err != nil {
		var approvalErr *agentport.ToolApprovalRequiredError
		if errors.As(err, &approvalErr) {
			c.JSON(http.StatusAccepted, gin.H{"status": "waiting_approval", "approvalId": approvalErr.ApprovalID, "toolCallId": approvalErr.ToolCallID, "serverId": approvalErr.ServerID, "toolName": approvalErr.ToolName, "riskLevel": approvalErr.RiskLevel})
			return
		}
		// 续跑竞态：带 execution_id 续跑但审批其实还在等待中（未批准）。
		// 幂等返回 202，前端据此恢复"等待审批"卡片而非报错/重复创建。
		if errors.Is(err, agent.ErrApprovalNotApproved) {
			c.JSON(http.StatusAccepted, gin.H{"status": "waiting_approval"})
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
	c.JSON(http.StatusOK, agentExecutionResultDTO(result))
}

func respondAgentExecutionError(c *gin.Context, err error) {
	_ = c.Error(err)
}

// GetActiveExecution reports a conversation's in-flight execution for session
// continuity after a hard refresh. A 404 {"status":"none"} means genuinely no
// active execution (or the actor lacks ownership — existence oracle closed);
// any transient DB failure surfaces as a 500 so the frontend never mistakes a
// read failure for "no active execution" and silently starts a duplicate run.
func (h *AgentHandler) GetActiveExecution(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	userID, _ := userIDFromCtx(c)
	convID := c.Param("convID")

	active, err := h.svc.GetActiveExecution(c.Request.Context(), tenantID, convID, userID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if active == nil {
		c.JSON(http.StatusNotFound, gin.H{"status": "none"})
		return
	}
	c.JSON(http.StatusOK, active)
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

	writer := newSSEEventWriter(c.Writer)

	// 委托进度帧：stratum_delegate 子 agent 进入/结束时推送，供前端渲染
	// "子 agent 正在执行"占位（消除委托期间主对话静默）。
	delegateCb := func(ev agentgraph.DelegateEvent) {
		payload, _ := json.Marshal(map[string]any{
			"delegate_status": string(ev.Status),
			"delegate_id":     ev.DelegateID,
			"goal":            ev.Goal,
			"summary":         ev.Summary,
			"tokens_used":     ev.TokensUsed,
			"result_status":   ev.ResultStatus,
		})
		writer.EnqueueData(string(payload))
	}

	clientCtx := c.Request.Context()
	tokenCb := func(token string) {
		payload, _ := json.Marshal(map[string]string{"token": token})
		writer.EnqueueData(string(payload))
	}

	execCtx, cancel, run, executionID, err := h.svc.ExecuteStream(clientCtx, id, agent.ExecRequest{
		Query:          req.Query,
		ConversationID: req.ConversationID,
		UserID:         userID,
		MaxSteps:       intOption(req.Options, "maxSteps"),
		Timeout:        timeoutOption(req.Options, "timeout"),
	}, agent.ExecMeta{
		TenantID:        tenantID,
		TraceID:         middleware.GetTraceID(c),
		ExecutionID:     req.ExecutionID,
		Stream:          true,
		DelegateEventCb: delegateCb,
	}, tokenCb)
	if err != nil {
		// 续跑竞态：携带 execution_id 重发续跑，但审批其实还在等待中（未批准）。
		// 幂等返回 202，前端据此恢复"等待审批"卡片而非报错/重复创建。
		if errors.Is(err, agent.ErrApprovalNotApproved) {
			c.JSON(http.StatusAccepted, gin.H{"status": "waiting_approval"})
			return
		}
		_ = c.Error(err)
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Transfer-Encoding", "chunked")
	defer cancel()
	// 首帧下发恢复键:断线续发协议的前提,先于任何 token 帧(EnqueueData FIFO)。
	// 客户端只要收到本帧即可在断线后携带 execution_id 重发续接。
	firstFrame, _ := json.Marshal(map[string]string{"execution_id": executionID})
	writer.EnqueueData(string(firstFrame))
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
			writer.EnqueueData(string(agentExecutionErrorPayload(runErr)))
			return
		}
		donePayload := agentExecutionDonePayload(result)
		writer.EnqueueData(string(donePayload))
	}()

	writer.WriteUntilClosed(0)
}

func agentExecutionErrorPayload(err error) []byte {
	descriptor := middleware.DescribePublicError(err, middleware.MapErrorToStatus(err))
	payload := map[string]string{"error": descriptor.Message}
	if descriptor.Code != "" {
		payload["code"] = descriptor.Code
	}
	encoded, _ := json.Marshal(payload)
	return encoded
}

func agentExecutionResultDTO(result *agent.AgentResult) AgentExecutionResult {
	thoughtsJSON, _ := json.Marshal(result.Thoughts)
	toolCallsJSON, _ := json.Marshal(result.ToolCalls)
	artifacts := executionArtifactsResponse(result.Artifacts)
	metadata := map[string]interface{}{"thoughtsJSON": string(thoughtsJSON), "toolCallsJSON": string(toolCallsJSON)}
	// 白名单透出 task snapshot（跨会话目标进度摘要条）。禁止透出 result.Metadata
	// 其他键——仅应用层写入的 task 数据可流出。
	if v, ok := result.Metadata[constants.TaskMetadataKey]; ok {
		metadata[constants.TaskMetadataKey] = v
	}
	return AgentExecutionResult{AgentID: result.AgentID, Input: result.Input, Output: result.Output, Steps: result.Steps,
		TokensUsed: result.TokensUsed, Duration: result.Duration.String(), Thoughts: result.Thoughts, ToolCalls: result.ToolCalls,
		Artifacts: artifacts, Metadata: metadata}
}

func executionArtifactsResponse(artifacts []domain.ExecutionArtifact) []domain.ExecutionArtifact {
	if artifacts == nil {
		return []domain.ExecutionArtifact{}
	}
	return artifacts
}

func agentExecutionDonePayload(result *agent.AgentResult) []byte {
	dto := agentExecutionResultDTO(result)
	// Sources must serialize as an empty array, never null: the frontend
	// reads done.sources as a list and treats [] and null differently during
	// rolling upgrades.
	sources := result.Sources
	if sources == nil {
		sources = []agentport.RAGSearchSource{}
	}
	payload, _ := json.Marshal(struct {
		Done          bool                        `json:"done"`
		Output        string                      `json:"output"`
		Steps         int                         `json:"steps"`
		TokensUsed    int                         `json:"tokensUsed"`
		Duration      string                      `json:"duration"`
		Artifacts     []domain.ExecutionArtifact  `json:"artifacts"`
		Sources       []agentport.RAGSearchSource `json:"sources"`
		Degraded      bool                        `json:"degraded"`
		DegradeReason string                      `json:"degradeReason,omitempty"`
		FactCheck     *domain.FactCheckReport     `json:"factCheck,omitempty"`
		NoAnswer      *domain.NoAnswerInfo        `json:"noAnswer,omitempty"`
		Metadata      map[string]interface{}      `json:"metadata,omitempty"`
	}{true, dto.Output, dto.Steps, dto.TokensUsed, dto.Duration, dto.Artifacts, sources, result.Degraded, result.DegradeReason, result.FactCheck, result.NoAnswer, dto.Metadata})
	return payload
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
