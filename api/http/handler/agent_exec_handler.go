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
		var batchErr *agentport.BatchToolApprovalRequiredError
		if errors.As(err, &batchErr) {
			c.JSON(http.StatusAccepted, approvalAcceptedResponse(batchErr.Errors))
			return
		}
		var approvalErr *agentport.ToolApprovalRequiredError
		if errors.As(err, &approvalErr) {
			c.JSON(http.StatusAccepted, approvalAcceptedResponse([]agentport.ToolApprovalRequiredError{*approvalErr}))
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

	execCtx, cancel, run, executionID, err := h.svc.ExecuteWithDeltas(clientCtx, id, agent.ExecRequest{
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
			var batchErr *agentport.BatchToolApprovalRequiredError
			if errors.As(runErr, &batchErr) {
				writer.EnqueueEvent("approval_required", string(approvalRequiredSSEPayload(batchErr.Errors)))
				return
			}
			var approvalErr *agentport.ToolApprovalRequiredError
			if errors.As(runErr, &approvalErr) {
				writer.EnqueueEvent("approval_required", string(approvalRequiredSSEPayload([]agentport.ToolApprovalRequiredError{*approvalErr})))
				return
			}
			h.logger.Error("agent stream execution failed", zap.String("agentId", id), zap.Error(runErr))
			writer.EnqueueData(string(h.agentExecutionErrorPayload(runErr)))
			return
		}
		donePayload := h.agentExecutionDonePayload(result)
		writer.EnqueueData(string(donePayload))
	}()

	writer.WriteUntilClosed(0)
}

// agentExecutionErrorPayload SSE error 帧载荷薄包装。
//
// 真实现在应用层（agent.AgentService.ErrorPayloadBytes）：run 改由 runner 内执行后
// 流内帧必须由它写出，两处若各留一份实现必然漂移（I-2）。安全红线同样在应用层——
// mapper 是错误文本的唯一来源，本层不得回落到 err.Error()。
func (h *AgentHandler) agentExecutionErrorPayload(err error) []byte {
	return h.svc.ErrorPayloadBytes(err)
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

// agentExecutionDonePayload SSE done 帧载荷薄包装：真实现在应用层
// （agent.AgentService.DonePayloadBytes），理由同 agentExecutionErrorPayload。
func (h *AgentHandler) agentExecutionDonePayload(result *agent.AgentResult) []byte {
	return h.svc.DonePayloadBytes(result)
}

// approvalAcceptedResponse 非流式 /execute 的 202 审批等待体：approvals 数组携带
// 本轮全部待审批工具（单条=长度 1），顶层 approvalId/toolCallId/... 镜像首条兼容
// 旧前端；approvalIds 便于前端按恢复键轮询 active-execution。
func approvalAcceptedResponse(approvals []agentport.ToolApprovalRequiredError) gin.H {
	items := make([]map[string]any, 0, len(approvals))
	for _, a := range approvals {
		items = append(items, map[string]any{
			"approvalId": a.ApprovalID, "toolCallId": a.ToolCallID,
			"serverId": a.ServerID, "toolName": a.ToolName, "riskLevel": a.RiskLevel,
		})
	}
	body := gin.H{"status": "waiting_approval", "approvals": items}
	if len(approvals) > 0 {
		first := items[0]
		for _, k := range []string{"approvalId", "toolCallId", "serverId", "toolName", "riskLevel"} {
			body[k] = first[k]
		}
	}
	return body
}

// approvalRequiredSSEPayload SSE approval_required 帧：与 202 体同构（approvals
// 数组 + 首条镜像），单/批共一帧，前端据此批量渲染审批卡并等待全部终态后续跑。
//
// 薄包装：真实现在应用层（agent.ApprovalRequiredPayloadBytes），run 改由 runner 内执行
// 后流内帧必须由它写出，两处若各留一份实现必然漂移。本函数保留只为既有调用点/用例稳定。
func approvalRequiredSSEPayload(approvals []agentport.ToolApprovalRequiredError) []byte {
	return agent.ApprovalRequiredPayloadBytes(approvals)
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
