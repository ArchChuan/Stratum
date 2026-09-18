// Package handler — agent_exec_handler.go.
//
// Transport layer for /agents/:id/execute and /execute/stream. SSE
// mechanics (heartbeat, client-cancel watcher, token writer) live here;
// orchestration (registry lookup, capability injection, recording)
// lives in agent.AgentService.
package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/byteBuilderX/stratum/api/middleware"
	agent "github.com/byteBuilderX/stratum/internal/agent/application"
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

// ExecuteAgentStream 是纯订阅者：它不再拥有 run，也不再有「客户端断连即
// cancel」的分支（spec §4.1）。执行的启动/续接交给应用层，本函数只负责把
// 订阅产出的帧写进 socket。
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

	// 带 execution_id 的请求是续接：先做租户限定的存在性判断，查不到即 404
	// （不区分「不存在」与「不属于你」），避免把 read 失败当成「无活跃执行」
	// 而静默起一个重复运行。
	sub, err := h.svc.OpenStreamSubscription(c.Request.Context(), id, agent.ExecRequest{
		Query:          req.Query,
		ConversationID: req.ConversationID,
		UserID:         userID,
		MaxSteps:       intOption(req.Options, "maxSteps"),
		Timeout:        timeoutOption(req.Options, "timeout"),
	}, agent.ExecMeta{
		TenantID:    tenantID,
		TraceID:     middleware.GetTraceID(c),
		ExecutionID: req.ExecutionID,
		Generation:  req.Generation,
		LastEventID: req.LastEventID,
		Stream:      true,
	}, agent.DefaultStreamSubscriptionConfig())
	if err != nil {
		if errors.Is(err, agent.ErrNotFound) {
			_ = c.Error(err)
			return
		}
		if errors.Is(err, agent.ErrApprovalNotApproved) {
			c.JSON(http.StatusAccepted, gin.H{"status": "waiting_approval"})
			return
		}
		_ = c.Error(err)
		return
	}
	defer sub.Close()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Transfer-Encoding", "chunked")

	writer := newSSEEventWriter(c.Writer)
	// 订阅 goroutine 持续产出；本 goroutine 只做写出。慢客户端的背压止步于
	// 订阅 goroutine 的帧通道，不再顶到 LLM 读循环（spec §4.4）。
	go func() {
		defer writer.Close()
		for frame := range sub.Frames() {
			if frame.Comment != "" {
				if !writer.EnqueueComment(frame.Comment) {
					return
				}
				continue
			}
			if !writer.EnqueueStreamFrame(frame.ID, sseEventName(frame.Event), frame.Data) {
				return
			}
		}
	}()

	// 客户端断开只结束本订阅：run 继续跑，viewer 由 runner 的自然超时摘除
	// （spec §7.4）。停掉 run 的唯一途径是 stop 端点或孤儿超时。
	writer.WriteUntilClosed(0)
}

// sseNamedEvents 是 PR 1 阶段下发给客户端的具名事件白名单。
// 其余事件名存在于流条目中供服务端内部判定（终态识别、游标语义），但不下发
// event: 行——今天的前端按 data 字段嗅探分发，全量具名下发排在 PR 3（spec §6.8）。
// approval_required 今天已具名，保持不变。
var sseNamedEvents = map[string]struct{}{
	agentport.StreamEventMeta:             {},
	agentport.StreamEventReset:            {},
	agentport.StreamEventApprovalRequired: {},
}

func sseEventName(event string) string {
	if _, ok := sseNamedEvents[event]; ok {
		return event
	}
	return ""
}

// StopExecution 请求停止一次在跑的流式执行。它发布控制通道消息，由持有租约的
// runner 收到后取消——不直接杀任何本进程的 goroutine，因此对「run 在另一个
// pod 上」同样有效（spec §7.3）。
func (h *AgentHandler) StopExecution(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	// fail closed：userIDFromCtx 的 ok 只表示类型断言成功，值为空字符串时
	// ok 仍为 true（JWT 中间件对 sub 不做非空校验）。必须显式判空，否则
	// sub="" 的令牌会以空 actor 身份通过归属判定、停掉同租户内任意执行。
	userID, ok := userIDFromCtx(c)
	if !ok || userID == "" {
		respondMissingUser(c)
		return
	}
	executionID := c.Param("executionID")

	// 授权在 HTTP 层完成：Pub/Sub 通道不承载鉴权，发布前必须确认 actor 对该
	// execution 有所有权（spec §9）。存在性与归属合并为一次租户限定的查询，
	// 查不到即 404，不区分「不存在」与「不属于你」。
	if err := h.svc.StopExecution(c.Request.Context(), tenantID, executionID, userID); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "stopping"})
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
