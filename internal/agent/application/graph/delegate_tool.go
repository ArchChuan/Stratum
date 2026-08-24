package graph

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// stratum_delegate 子 agent 派发的类型定义与（Step 5 的）执行实现。
//
// 数据流（主 ReAct 循环 → dispatchToolCall → execDelegateTool）：
// guard 层走 port.ToolExecutionRequest.DelegateExecutor 闭包，闭包捕获本状态
// 并调用 s.DelegateExecutor（application 层 buildDelegateExecutor 附着），后者
// 值拷贝 child := parent 复用同一 agent 配置在独立上下文窗口执行子循环，只回传
// 结构化外壳摘要。本文件只放类型与常量，保证 Step 3 编译闭合；执行器实现在
// Step 5 追加。

// DelegateInput 是 stratum_delegate 工具的解析后参数（jsonschema 已由 guard 的
// validateToolArguments 校验，此处只做类型化读取）。
type DelegateInput struct {
	Goal     string
	MaxSteps int // 0 = 用 child.DelegateDefaultMaxSteps
}

// DelegateStatus 是委托结果状态（结构化外壳 status 白名单）。解析归属 application
// 层：外壳 JSON 解析失败或缺省时回落 DelegateStatusPartial，绝不 fail 主循环。
type DelegateStatus string

const (
	DelegateStatusSuccess DelegateStatus = "success"
	DelegateStatusPartial DelegateStatus = "partial"
	DelegateStatusFailed  DelegateStatus = "failed"
)

// DelegateOutput 是子循环回传主循环的压缩结果，是结构化外壳
// {summary, status, tokens_used} 的语义载体（外壳 JSON 由闭包包装生成）。
type DelegateOutput struct {
	Summary    string
	Status     string
	TokensUsed int
	StepsUsed  int
	DelegateID string
}

// DelegateEventStatus 是委托进度事件状态（SSE delegate_status 帧白名单）。
type DelegateEventStatus string

const (
	// DelegateEventRunning 在子循环发起前回调一次（用户可见"子 agent 正在执行"）。
	DelegateEventRunning DelegateEventStatus = "running"
	// DelegateEventFinished 在子循环结束（成功或失败）时回调，清除 running 帧并
	// 携带结果：成功/部分完成含摘要与用量；失败路径 result_status=failed，
	// summary 为固定安全文案，原始错误只进日志。
	DelegateEventFinished DelegateEventStatus = "finished"
)

// DelegateEvent 是 delegate 状态事件，经 ReActState.OnDelegateEvent 回调转成
// SSE delegate_status 帧出口（子任务进入/结束时各回调一次）。
type DelegateEvent struct {
	DelegateID   string
	Goal         string
	Status       DelegateEventStatus
	ResultStatus string
	Summary      string
	TokensUsed   int
}

// DelegateExecutor 执行委托子循环。闭包内必须使用值拷贝 child := parent 并整体
// 替换 child.Messages 为新 slice，父 Messages 永不写入子内容。
type DelegateExecutor func(context.Context, *ReActState, DelegateInput) (DelegateOutput, error)

// DelegateSummaryInstruction 追加在子 agent 的 user 消息末尾，要求收尾输出
// 结构化外壳摘要。父 agent 看不到子循环内部，摘要必须自包含；status 白名单与
// tokens_used 供 application 层解析（解析失败回落 partial，不 fail 主循环）。
// 导出给 application 层 buildDelegateExecutor 组装子循环 user 消息引用。
const DelegateSummaryInstruction = `Complete the delegated task, then end with a structured JSON shell as your very last message:

{"summary": "<concise self-contained summary of what was accomplished>", "status": "success|partial|failed", "tokens_used": <int>}

Rules:
- The summary must be self-contained: the parent agent cannot see this conversation.
- status = "success" only when the task was fully completed; "partial" when only partially done; "failed" when it could not be done.
- tokens_used is an informational estimate of tokens spent on this task.
- Output nothing after the JSON shell.`

// execDelegateTool 是 stratum_delegate 工具在工具节点内的执行入口。三道防线
// 依次 fail-closed：未启用 → correction 观察（Success，模型可换路）；深度超限 →
// correction 观察（Success）；executor 未附着 → Error 观察（配置错误，模型不可
// 纠正）。实际子循环经 s.ToolExecutionFn → guard → DelegateExecutor 闭包执行，
// 与 MCP 工具共享同一条 guard 链路。
func execDelegateTool(toolCtx context.Context, tc port.ToolCall, s *ReActState, toolStart time.Time, logger *zap.Logger) toolExecResult {
	if !s.DelegateEnabled {
		msg := s.recordCorrection(StratumDelegateToolName, errors.New("delegate not enabled"), nil)
		return toolExecResult{content: msg, status: domain.ToolTraceStatusSuccess}
	}
	if s.DelegateDepth >= s.DelegateMaxDepth {
		msg := s.recordCorrection(StratumDelegateToolName, errors.New("delegate depth limit reached"), nil)
		return toolExecResult{content: msg, status: domain.ToolTraceStatusSuccess}
	}
	if s.DelegateExecutor == nil || s.ToolExecutionFn == nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: "delegate executor not configured", content: "error: delegate executor not configured"}
	}
	tool, ok := findTool(tc.Name, s.AvailableTools)
	if !ok {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: fmt.Sprintf("unknown tool %q", tc.Name), content: fmt.Sprintf("error: unknown tool %q", tc.Name)}
	}
	emitDelegateRunning(s, tc.Arguments)
	callCtx, cancel := context.WithTimeout(toolCtx, constants.DelegateExecutionTimeout)
	toolOutput, callErr := s.ToolExecutionFn(callCtx, port.ToolExecutionRequest{
		ToolCallID:       tc.ID,
		Tool:             tool,
		Arguments:        tc.Arguments,
		DelegateExecutor: buildDelegateClosure(s),
	})
	cancel()
	toolLatencyMs := time.Since(toolStart).Milliseconds()
	var approvalRequired *port.ToolApprovalRequiredError
	if errors.As(callErr, &approvalRequired) {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: callErr.Error(), content: "", fatalToolErr: callErr}
	}
	switch {
	case callErr != nil:
		logger.Error("react.tool", zap.String("trace_id", s.TraceID), zap.String("tenant_id", s.TenantID),
			zap.String("conversation_id", s.ConversationID), zap.String("tool_name", tc.Name),
			zap.Int64("latency_ms", toolLatencyMs), zap.Error(callErr))
		// 原始错误只进日志；观察正文用固定中文模板（不把子循环内部标识泄入下游错误正文）。
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: "delegate sub-agent execution failed", content: "委托子 Agent 执行失败"}
	case toolOutput != nil:
		logger.Info("react.tool", zap.String("trace_id", s.TraceID), zap.String("tenant_id", s.TenantID),
			zap.String("conversation_id", s.ConversationID), zap.String("tool_name", tc.Name),
			zap.Int64("latency_ms", toolLatencyMs))
		guarded, safe := toolOutput.(port.GuardedToolResult)
		if !safe {
			return toolExecResult{status: domain.ToolTraceStatusError, errMsg: "tool result was not validated", content: "error: tool result validation failed"}
		}
		return toolExecResult{content: guarded.ModelContent, status: domain.ToolTraceStatusSuccess}
	default:
		logger.Info("react.tool", zap.String("trace_id", s.TraceID), zap.String("tenant_id", s.TenantID),
			zap.String("conversation_id", s.ConversationID), zap.String("tool_name", tc.Name),
			zap.Int64("latency_ms", toolLatencyMs))
		return toolExecResult{content: "", status: domain.ToolTraceStatusSuccess}
	}
}

// emitDelegateRunning 进入子循环执行前回调一次 running 帧（SSE delegate_status）。
// 结束帧由 buildDelegateClosure 在成功/失败路径补发；fail-closed 分支（未启用/深度
// 超限/缺失 executor）不触发事件——只有真正进入执行才需要进度反馈；guard 在闭包前
// 拒绝时 running 帧由主循环结束的 onDone/onError 兜底清除。
func emitDelegateRunning(s *ReActState, args map[string]any) {
	if s.OnDelegateEvent != nil {
		s.OnDelegateEvent(DelegateEvent{Goal: parseDelegateArgs(args).Goal, Status: DelegateEventRunning})
	}
}

// buildDelegateClosure 构建 guard 的 DelegateExecutor 闭包（port.DelegateToolRunFunc）：
// 解析参数 → 调 application 层 s.DelegateExecutor 执行子循环 → 把 token 增量折回
// 主状态一次（对齐 plan_graph 的 delta 语义，无重复计费）→ 以 StructuredContent
// 承载结构化外壳。guard 的 ResultGuard 对 StructuredContent 直接序列化，ModelContent
// 呈现为外壳 JSON（无 Content 数组的嵌套转义），观察正文与 trace 均可读。
// 返回的错误只 wrap 上抛给 guard（进日志），观察正文由固定模板承载，不泄子循环内部。
func buildDelegateClosure(s *ReActState) port.DelegateToolRunFunc {
	return func(ctx context.Context, args map[string]any) (port.MCPToolResult, error) {
		input := parseDelegateArgs(args)
		out, err := s.DelegateExecutor(ctx, s, input)
		if err != nil {
			// 子循环失败：仍发一次 finished 帧清除 running，携带 failed 结果状态与
			// delegate_id（与成功帧同源），summary 用固定安全文案，原始错误只进日志。
			if s.OnDelegateEvent != nil {
				s.OnDelegateEvent(DelegateEvent{
					DelegateID: out.DelegateID, Goal: input.Goal,
					Status: DelegateEventFinished, ResultStatus: string(DelegateStatusFailed),
					Summary: "委托子 Agent 执行失败",
				})
			}
			return port.MCPToolResult{}, err
		}
		s.TotalTokens += out.TokensUsed
		if s.OnDelegateEvent != nil {
			s.OnDelegateEvent(DelegateEvent{
				DelegateID: out.DelegateID, Goal: input.Goal,
				Status: DelegateEventFinished, ResultStatus: out.Status,
				Summary: out.Summary, TokensUsed: out.TokensUsed,
			})
		}
		return port.MCPToolResult{StructuredContent: delegateShellContent(out)}, nil
	}
}

// parseDelegateArgs 把 jsonschema 已校验的工具参数读成 DelegateInput。max_steps
// 在 JSON 反序列化后为 float64；0/缺省 = 用 child.DelegateDefaultMaxSteps。
func parseDelegateArgs(args map[string]any) DelegateInput {
	goal, _ := args["goal"].(string)
	maxSteps, _ := args["max_steps"].(float64)
	return DelegateInput{Goal: goal, MaxSteps: int(maxSteps)}
}

// delegateShellContent 把子循环结果打包为结构化外壳 map {summary, status,
// tokens_used}。status 由 application 层解析校验后传入（白名单
// success/partial/failed）；空值回落 partial，绝不 fail 主循环。summary 先按
// DelegateSummaryMaxRunes 截断，再经 ResultGuard 32KB 兜底，防止子摘要挤爆主上下文。
func delegateShellContent(out DelegateOutput) map[string]any {
	status := out.Status
	if status == "" {
		status = string(DelegateStatusPartial)
	}
	return map[string]any{
		"summary":     truncateRunes(out.Summary, constants.DelegateSummaryMaxRunes),
		"status":      status,
		"tokens_used": out.TokensUsed,
	}
}

// MaxDelegateSteps 把用户传入的 max_steps 与 per-agent 默认值归一化：<=0 用默认，
// 再 clamp 到 [1, constants.MaxDelegateMaxLLMSteps]（与工具 schema maximum 一致）。
// application 层 buildDelegateExecutor 在组装子循环时调用，故导出。
func MaxDelegateSteps(inputMax, defaultMax int) int {
	if inputMax <= 0 {
		inputMax = defaultMax
	}
	if inputMax < 1 {
		inputMax = 1
	}
	if inputMax > constants.MaxDelegateMaxLLMSteps {
		inputMax = constants.MaxDelegateMaxLLMSteps
	}
	return inputMax
}
