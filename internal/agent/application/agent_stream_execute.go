// Package application — agent_stream_execute.go.
//
// runner 的启动与订阅的装配：把 Task 11 的基元与 Task 13 的 ExecuteStream 缝在一起。
package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

// launchStreamRunner 启动一个持有租约的 runner。控制通道订阅在 run 启动之前
// 完成——否则会漏掉早到的 viewer 消息，让刚 attach 的执行被误判为孤儿（spec §7.1）。
func (s *AgentService) launchStreamRunner(
	ctx context.Context, agentID string, req ExecRequest, meta ExecMeta,
	executionID string, generation int, cfg StreamRunnerConfig,
) error {
	// runner 的 context 与请求解绑：HTTP 请求断开不得终止 run（spec §4.1）。
	// 这一行是整个改造的核心——今天 handler 在 clientCtx.Done() 时显式 cancel()。
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	msgCh, unsubscribe, err := s.deps.ControlBus.Subscribe(runCtx, executionID)
	if err != nil {
		cancel()
		return fmt.Errorf("agent: execute stream: subscribe control channel: %w", err)
	}
	viewers := newViewerRegistry(cfg.ViewerTimeout, time.Now)
	handle := s.localRunnerSet().track(executionID, generation, cancel)
	if handle == nil {
		// 本 pod 正在关闭：fail closed。不得降级成「无 runner 也能跑」——那会在
		// 关闭中的 pod 上留下一个无人取消、无人等待的孤儿执行。
		//
		// 必须在启动 goroutine 之前返回：handle 为 nil 时，goroutine 里的
		// untrack(handle) / handle.finish() 会在 nil 接收者上取字段直接 panic。
		// track 拒绝时**不消费** cancel，所以这里的 cancel() 是调用方义务。
		unsubscribe()
		cancel()
		return fmt.Errorf("agent: execute stream: stream runners are shutting down")
	}

	go func() {
		defer unsubscribe()
		defer cancel()
		defer handle.finish()
		defer s.localRunnerSet().untrack(handle)
		s.runStreamRunner(runCtx, agentID, req, meta, executionID, generation, cfg, viewers, msgCh)
	}()
	return nil
}

// runStreamRunner 是 runner 的主循环：写 meta 帧 → 跑 run → 写终态帧 → 释放租约。
// 租约续租心跳与控制消息在 run 期间并行消费。
func (s *AgentService) runStreamRunner(
	ctx context.Context, agentID string, req ExecRequest, meta ExecMeta,
	executionID string, generation int, cfg StreamRunnerConfig,
	viewers *viewerRegistry, msgCh <-chan port.ControlMessage,
) {
	// meta 帧必须是流的第一条：订阅侧据此得知 generation，前端据此获得恢复键。
	s.appendStreamEntry(ctx, executionID, generation, port.StreamEntry{
		Event: port.StreamEventMeta,
		Payload: mustJSON(map[string]any{
			"execution_id": executionID,
			"generation":   generation,
		}),
	})

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	// 续租、控制消息、孤儿计时共用一条 tick：三者语义相同（"我还活着" /
	// "你还在看着"），拆成三个 timer 只增加竞态面（spec §6.7）。
	stopHeartbeat := make(chan struct{})
	go s.streamRunnerHeartbeat(runCtx, cancelRun, meta.TenantID, executionID, generation, cfg, viewers, stopHeartbeat)
	go consumeControlMessages(runCtx, viewers, msgCh, cancelRun)

	result, _, runErr := s.executeStreamRun(runCtx, agentID, req, meta, executionID, generation)

	close(stopHeartbeat)
	// 无论成功、报错还是被取消，都写终态帧到流——订阅侧据此收敛，不会无限跟流。
	s.appendStreamTerminal(ctx, executionID, generation, result, runErr)

	if err := s.deps.LeaseRepo.ReleaseLease(ctx, meta.TenantID, executionID, generation); err != nil {
		s.deps.Logger.Warn("agent stream: release lease failed",
			zap.String("execution_id", executionID), zap.Error(err))
	}
}

// consumeControlMessages 把控制通道翻译成 viewer 心跳与 run 取消。msgCh 关闭或
// run 结束（ctx 取消）即退出，不持有任何资源。
func consumeControlMessages(
	ctx context.Context, viewers *viewerRegistry, msgCh <-chan port.ControlMessage, cancel context.CancelFunc,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-msgCh:
			if !ok {
				return
			}
			switch msg.Type {
			case port.ControlMessageViewer:
				viewers.Seen(msg.ViewerID)
			case port.ControlMessageCancel:
				cancel()
				return
			}
		}
	}
}

// streamRunnerHeartbeat 续租并维护 viewer 集合。续租 CAS 失败意味着已被抢占，
// 立刻自取消——僵尸 runner 不得继续烧 token（spec D3）。
//
// tenantID 必须由调用方传入而非从 ctx 取：runCtx 来自 context.WithoutCancel，
// 不带租户上下文，从 ctx 取会在错的租户命名空间上操作。
func (s *AgentService) streamRunnerHeartbeat(
	ctx context.Context, cancel context.CancelFunc, tenantID, executionID string,
	generation int, cfg StreamRunnerConfig, viewers *viewerRegistry, stop <-chan struct{},
) {
	ticker := time.NewTicker(renewInterval(cfg))
	defer ticker.Stop()

	orphanSince := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			// 取消（续租失败/孤儿超时）由 renewOnce 发起，下一轮 select 会在
			// ctx.Done() 上退出——与就地 return 等价，但把判定收在一个函数里。
			orphanSince = s.renewOnce(ctx, cancel, tenantID, executionID, generation, cfg, viewers, orphanSince)
		}
	}
}

// renewInterval 归一化续租间隔：显式配置 → 租约的 1/3 → 兜底 1s。
func renewInterval(cfg StreamRunnerConfig) time.Duration {
	if cfg.LeaseRenew > 0 {
		return cfg.LeaseRenew
	}
	if ttl := cfg.LeaseTTL / 3; ttl > 0 {
		return ttl
	}
	return time.Second
}

// renewOnce 续租一次并维护 viewer 集合，返回新的孤儿计时基线（有活 viewer 时
// 前移，否则保持，让孤儿超时能真正累积）。
func (s *AgentService) renewOnce(
	ctx context.Context, cancel context.CancelFunc, tenantID, executionID string,
	generation int, cfg StreamRunnerConfig, viewers *viewerRegistry, orphanSince time.Time,
) time.Time {
	if err := s.deps.LeaseRepo.RenewLease(ctx, tenantID, executionID, generation, cfg.LeaseTTL); err != nil {
		s.deps.Logger.Warn("agent stream: lease renew failed, cancelling run",
			zap.String("execution_id", executionID), zap.Error(err))
		cancel()
		return orphanSince
	}
	// 搭车刷新流的 TTL，不新增定时器（spec §6.7）。
	if err := s.deps.StreamStore.RefreshTTL(ctx, executionID, generation); err != nil {
		s.deps.Logger.Debug("agent stream: refresh ttl failed",
			zap.String("execution_id", executionID), zap.Error(err))
	}
	if viewers.Count() > 0 {
		return time.Now()
	}
	if time.Since(orphanSince) >= cfg.OrphanTimeout {
		s.deps.Logger.Info("agent stream: no viewers, cancelling orphan run",
			zap.String("execution_id", executionID))
		cancel()
	}
	return orphanSince
}

// executeStreamRun 复用 prepareAgentExecution 的全部准备语义，但把 token /
// delegate 回调改为写流而非写 socket，并补上终态帧的写入口。
func (s *AgentService) executeStreamRun(
	ctx context.Context, agentID string, req ExecRequest, meta ExecMeta,
	executionID string, generation int,
) (*AgentResult, int, error) {
	tokenCb := func(token string) {
		s.appendStreamEntry(ctx, executionID, generation,
			snapshotStreamFrame(port.StreamEventToken, map[string]string{"token": token}))
	}
	delegateCb := func(ev agentgraph.DelegateEvent) {
		s.appendStreamEntry(ctx, executionID, generation, snapshotStreamFrame(port.StreamEventDelegate, map[string]any{
			"delegate_status": string(ev.Status),
			"delegate_id":     ev.DelegateID,
			"goal":            ev.Goal,
			"summary":         ev.Summary,
			"tokens_used":     ev.TokensUsed,
			"result_status":   ev.ResultStatus,
		}))
	}
	meta.DelegateEventCb = delegateCb

	// 测试注入点：跨实例测试用假 LLM 替换整次执行，否则无法断言
	// 「B 是续传而不是重新生成」。生产为 nil，走下面各条的真实路径。
	if s.deps.StreamRunFn != nil {
		return s.deps.StreamRunFn(ctx, agentID, req, meta, tokenCb)
	}

	a, req, meta, streamCtx, options, cfg, resuming, terminal, consumedApproval, err :=
		s.prepareAgentExecution(ctx, agentID, req, meta, executionID)
	if err != nil {
		return nil, 0, err
	}
	s.logAgentExecutionDebug("agent.execute_stream", agentID, meta, req)
	options = append(options,
		WithTokenCallback(s.wrapTokenCallback(cfg, tokenCb)),
		WithDelegateEventCallback(delegateCb),
		WithExecutionID(executionID),
	)

	execCtx, cancel := context.WithCancel(context.WithoutCancel(streamCtx))
	defer cancel()
	// P1b：与 Execute 路径一致的规则拦截累积器注入（§4.1）。
	blocks := &[]domain.RuleBlock{}
	execCtx = context.WithValue(execCtx, ruleBlockCollectorKey{}, blocks)

	start := time.Now()
	res, runErr := a.Execute(execCtx, req.Query, options...)
	durationMs := int(time.Since(start).Milliseconds())
	s.recordSystemAssistantExecution(cfg, res, runErr)
	s.logAgentExecution("agent.execute_stream", agentID, meta, req, durationMs, runErr)
	if runErr == nil && res != nil {
		// 降级决策与 Execute 路径一致：答案已交付，旁路记忆缓冲失败只记日志。
		scope := a.GetConfig().MemoryScope
		s.bufferMemoryTurn(ctx, meta, req, agentID, scope, "user", req.Query)
		s.bufferMemoryTurn(ctx, meta, req, agentID, scope, "assistant", res.Output)
		s.emitObservation(execCtx, meta, agentID, executionID, res)
	}
	s.enqueueTrajectoryReflection(ctx, meta, req, agentID, a.GetConfig().MemoryScope, executionID, res)
	if resuming {
		// 审批续跑收尾：成功/消费标记推进 checkpoint；失败且未消费批准时
		// 回滚 running→waiting_approval，让 member 可重试同一批准。
		runErr = s.finishApprovalResume(ctx, meta.TenantID, executionID, consumedApproval, terminal, runErr)
	}
	return res, durationMs, runErr
}

// wrapTokenCallback 用 TTFT 指标包裹 token 回调（保留原流式路径的观测语义）。
// Metrics 未装配时原样返回，不在热路径上多一层闭包。
func (s *AgentService) wrapTokenCallback(cfg *ExecutionConfig, tokenCb func(string)) func(string) {
	if s.deps.Metrics == nil {
		return tokenCb
	}
	var firstToken sync.Once
	streamStarted := time.Now()
	return func(token string) {
		firstToken.Do(func() {
			s.deps.Metrics.RecordSystemAssistantTTFT(cfg.AssistantRoleClass, "", time.Since(streamStarted).Seconds())
		})
		tokenCb(token)
	}
}

// OpenStreamSubscription 解析一条 SSE 请求的订阅计划。它先确保 run 已在跑
// （必要时抢占），再决定回放起点。
func (s *AgentService) OpenStreamSubscription(
	ctx context.Context, agentID string, req ExecRequest, meta ExecMeta, cfg StreamSubscriptionConfig,
) (*ExecutionSubscription, error) {
	handle, err := s.ExecuteStream(ctx, agentID, req, meta)
	if err != nil {
		return nil, err
	}
	plan := PlanStream(meta.Generation, handle.Generation, meta.LastEventID)
	return NewExecutionSubscription(ExecutionSubscriptionDeps{
		Stream:      s.deps.StreamStore,
		Control:     s.deps.ControlBus,
		Lease:       s.deps.LeaseRepo,
		TenantID:    meta.TenantID,
		ExecutionID: handle.ExecutionID,
		Generation:  handle.Generation,
		Plan:        plan,
		Cfg:         cfg,
		Logger:      s.deps.Logger,
	}), nil
}

// appendStreamTerminal 写终态帧。被取消（含用户停止）写 stopped 帧，其余按
// done / error 分流。stopped 帧刻意复用 done 的形状——今天的前端按 data 字段
// 嗅探分发，done 走覆盖语义（finalContent = output || accumulatedContent），
// output 为空时保留用户已看到的部分答案。
func (s *AgentService) appendStreamTerminal(
	ctx context.Context, executionID string, generation int, result *AgentResult, runErr error,
) {
	switch {
	case isApprovalPending(runErr):
		// 审批待决是**可恢复的暂停**而不是错误：今天由 handler 把类型化错误翻成
		// approval_required 帧，改版后 handler 只转发帧，这段翻译必须落在 runner。
		// 载荷与 handler 的 approvalRequiredSSEPayload 同构（前端据此渲染审批卡）。
		s.appendStreamEntry(ctx, executionID, generation, port.StreamEntry{
			Event: port.StreamEventApprovalRequired, Payload: approvalRequiredPayload(runErr),
		})
	case runErr == nil && result != nil:
		s.appendStreamEntry(ctx, executionID, generation, port.StreamEntry{
			Event: port.StreamEventDone, Payload: string(s.donePayloadBytes(result)),
		})
	case errors.Is(runErr, context.Canceled):
		s.appendStreamEntry(ctx, executionID, generation, port.StreamEntry{
			Event:   port.StreamEventStopped,
			Payload: mustJSON(map[string]any{"done": true, "stopped": true}),
		})
	default:
		s.appendStreamEntry(ctx, executionID, generation, port.StreamEntry{
			Event: port.StreamEventError, Payload: string(s.errorPayloadBytes(runErr)),
		})
	}
}

// isApprovalPending 判定执行是否停在「等工具审批」这个可恢复的状态上。
func isApprovalPending(runErr error) bool {
	var batchErr *port.BatchToolApprovalRequiredError
	if errors.As(runErr, &batchErr) {
		return true
	}
	var singleErr *port.ToolApprovalRequiredError
	return errors.As(runErr, &singleErr)
}

// approvalRequiredPayload 渲染 approval_required 帧载荷：与 HTTP 202 体同构
// （approvals 数组 + 首条镜像），前端据此批量渲染审批卡并等待全部终态后续跑。
func approvalRequiredPayload(runErr error) string {
	approvals := approvalErrorsOf(runErr)
	items := make([]map[string]any, 0, len(approvals))
	for _, a := range approvals {
		items = append(items, map[string]any{
			"approvalId": a.ApprovalID, "toolCallId": a.ToolCallID,
			"serverId": a.ServerID, "toolName": a.ToolName, "riskLevel": a.RiskLevel,
		})
	}
	payload := map[string]any{"status": "waiting_approval", "approvals": items}
	if len(items) > 0 {
		for _, k := range []string{"approvalId", "toolCallId", "serverId", "toolName", "riskLevel"} {
			payload[k] = items[0][k]
		}
	}
	return mustJSON(payload)
}

// approvalErrorsOf 从错误链里取出待审批工具列表。批量错误携带全部条目，
// 单条错误只携带一个——两者共用同一帧形状（单/批共一帧）。
func approvalErrorsOf(runErr error) []port.ToolApprovalRequiredError {
	var batchErr *port.BatchToolApprovalRequiredError
	if errors.As(runErr, &batchErr) {
		return batchErr.Errors
	}
	var singleErr *port.ToolApprovalRequiredError
	if errors.As(runErr, &singleErr) {
		return []port.ToolApprovalRequiredError{*singleErr}
	}
	return nil
}

// appendStreamEntry 写一条流条目。写流是尽力而为的显示缓冲，失败不阻断 run。
func (s *AgentService) appendStreamEntry(
	ctx context.Context, executionID string, generation int, e port.StreamEntry,
) {
	if _, err := s.deps.StreamStore.Append(ctx, executionID, generation, e); err != nil {
		s.logStreamWriteFailure(executionID, err)
	}
}

// donePayloadBytes 渲染 done 终态帧载荷。字段与 handler 的 agentExecutionDonePayload
// 逐字段对齐——那是同一条 wire 契约，前端按 data 字段嗅探分发。
func (s *AgentService) donePayloadBytes(result *AgentResult) []byte {
	thoughtsJSON, _ := json.Marshal(result.Thoughts)
	toolCallsJSON, _ := json.Marshal(result.ToolCalls)
	metadata := map[string]interface{}{
		"thoughtsJSON": string(thoughtsJSON), "toolCallsJSON": string(toolCallsJSON),
	}
	// 白名单透出 task snapshot（跨会话目标进度摘要条）。禁止透出 result.Metadata
	// 其他键——仅应用层写入的 task 数据可流出。
	if v, ok := result.Metadata[constants.TaskMetadataKey]; ok {
		metadata[constants.TaskMetadataKey] = v
	}
	// Sources/Artifacts 必须序列化为空数组而非 null：前端把 [] 与 null 当两种
	// 语义处理，滚动升级期间保持与 handler 一致。
	artifacts := result.Artifacts
	if artifacts == nil {
		artifacts = []domain.ExecutionArtifact{}
	}
	sources := result.Sources
	if sources == nil {
		sources = []port.RAGSearchSource{}
	}
	payload, err := json.Marshal(struct {
		Done          bool                       `json:"done"`
		Output        string                     `json:"output"`
		Steps         int                        `json:"steps"`
		TokensUsed    int                        `json:"tokensUsed"`
		Duration      string                     `json:"duration"`
		Artifacts     []domain.ExecutionArtifact `json:"artifacts"`
		Sources       []port.RAGSearchSource     `json:"sources"`
		Degraded      bool                       `json:"degraded"`
		DegradeReason string                     `json:"degradeReason,omitempty"`
		FactCheck     *domain.FactCheckReport    `json:"factCheck,omitempty"`
		NoAnswer      *domain.NoAnswerInfo       `json:"noAnswer,omitempty"`
		Metadata      map[string]interface{}     `json:"metadata,omitempty"`
	}{
		Done: true, Output: result.Output, Steps: result.Steps, TokensUsed: result.TokensUsed,
		Duration: result.Duration.String(), Artifacts: artifacts, Sources: sources,
		Degraded: result.Degraded, DegradeReason: result.DegradeReason,
		FactCheck: result.FactCheck, NoAnswer: result.NoAnswer, Metadata: metadata,
	})
	if err != nil {
		// 载荷里的值都来自本包可控的类型，marshal 失败只可能是极端情况；
		// 真失败也只能降级成错误帧，不能让 runner 崩掉。
		return s.errorPayloadBytes(fmt.Errorf("agent: encode done payload: %w", err))
	}
	return payload
}

// 流内错误帧的公开文案与 code。措辞逐字对齐 api/middleware 的公开错误映射
// （DescribePublicError / Code* 常量）——那是同一条对客户端的 wire 契约，
// 前端按 code 分支（AgentChatPage 识别 ASSISTANT_MODEL_UNAVAILABLE）。
const (
	streamCodeAssistantModelUnavailable     = "ASSISTANT_MODEL_UNAVAILABLE"
	streamCodeSystemPromptNotConfigured     = "SYSTEM_PROMPT_NOT_CONFIGURED"
	streamCodeCompactionPromptNotConfigured = "COMPACTION_PROMPT_NOT_CONFIGURED"
	// streamGenericErrorMessage 是未命中已知 sentinel 时的兜底文案。
	// 主路径不返回它——见 errorPayloadBytes 的语义说明。
	streamGenericErrorMessage = "执行失败，请稍后重试"
)

// errorPayloadBytes 渲染 error 终态帧载荷。
//
// 为什么这里要重做一遍公开化而不是直接 err.Error()：run 在 runner 内部执行，
// 错误不再经过 HTTP handler，而错误链里常带上游响应体、内部 BaseURL 等细节，
// 直接透出等于把内部信息送到客户端（本仓库安全红线）。
//
// 为什么不能复用 api/middleware.DescribePublicError：application 不可反向依赖
// api 层（DDD 依赖方向）。因此这里只覆盖**本域可判定**的 fail-closed sentinel，
// 其余一律收敛为固定兜底文案——降级方向是「少说」而不是「多说」，宁可少给一句
// 可读性，也不泄露内部错误文本。
//
// 代价与去向：非 sentinel 的 4xx 语义文案（如参数校验细节）在流内会退化为兜底
// 文案。彻底等价需要把 MapErrorToStatus/DescribePublicError 下沉或被注入（例如
// wiring 装配一个 PublicErrorMapper 注入本服务），那超出本任务范围，已在报告
// Concerns 中显式登记。
func (s *AgentService) errorPayloadBytes(err error) []byte {
	payload := map[string]string{"error": streamGenericErrorMessage}
	switch {
	case errors.Is(err, domain.ErrAssistantModelUnavailable):
		payload["error"] = "该 Agent 尚未配置可用模型"
		payload["code"] = streamCodeAssistantModelUnavailable
	case errors.Is(err, domain.ErrSystemPromptNotConfigured):
		payload["error"] = "平台未配置全局系统提示词（agent.system_prompt），请联系平台管理员在参数配置中补全后重试"
		payload["code"] = streamCodeSystemPromptNotConfigured
	case errors.Is(err, domain.ErrCompactionPromptNotConfigured):
		payload["error"] = "平台未配置对话历史压缩提示词（agent.compaction_prompt），请联系平台管理员在参数配置中补全后重试"
		payload["code"] = streamCodeCompactionPromptNotConfigured
	}
	return []byte(mustJSON(payload))
}

// mustJSON 序列化一个必定可编码的值，失败降级为空对象——调用方都在执行路径上，
// 不允许为了一个展示用载荷 panic 掉整个 run。
func mustJSON(v any) string {
	encoded, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}
