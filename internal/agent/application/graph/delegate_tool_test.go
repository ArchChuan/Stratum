package graph

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// delegateCapGW 是 graph 内部包用的 CapabilityGateway 脚本化 stub（外部包的
// capGWSequence 不可见），按序列依次返回响应，耗尽后返回空响应。
type delegateCapGW struct {
	responses []port.CapabilityResponse
	idx       int
	llmReqs   []port.LLMCapRequest
}

func (s *delegateCapGW) Route(_ context.Context, req port.CapabilityRequest) (port.CapabilityResponse, error) {
	if req.LLM != nil {
		s.llmReqs = append(s.llmReqs, *req.LLM)
	}
	if s.idx < len(s.responses) {
		r := s.responses[s.idx]
		s.idx++
		return r, nil
	}
	return port.CapabilityResponse{}, nil
}

func delegateShellJSON(summary, status string, tokens int) string {
	b, _ := json.Marshal(map[string]any{"summary": summary, "status": status, "tokens_used": tokens})
	return string(b)
}

func wrappedDelegateShell(summary, status string, tokens int) string {
	return "<untrusted_tool_result>\n" + delegateShellJSON(summary, status, tokens) + "\n</untrusted_tool_result>"
}

// fixedDelegateExecutor 返回固定摘要/token 增量的测试执行器，断言 goal 透传。
func fixedDelegateExecutor(t *testing.T, wantGoal string, out DelegateOutput) DelegateExecutor {
	return func(_ context.Context, _ *ReActState, in DelegateInput) (DelegateOutput, error) {
		if wantGoal != "" {
			require.Equal(t, wantGoal, in.Goal)
		}
		return out, nil
	}
}

func TestMaxDelegateSteps_Clamps(t *testing.T) {
	// <=0 用默认；范围内原值；超上限 clamp（与工具 schema maximum 一致）。
	require.Equal(t, 5, MaxDelegateSteps(0, 5))
	require.Equal(t, 5, MaxDelegateSteps(-3, 5))
	require.Equal(t, 2, MaxDelegateSteps(2, 5))
	require.Equal(t, constants.MaxDelegateMaxLLMSteps, MaxDelegateSteps(99, 5))
	// default 也为 0 时地板到 1。
	require.Equal(t, 1, MaxDelegateSteps(0, 0))
}

func TestParseDelegateArgs(t *testing.T) {
	in := parseDelegateArgs(map[string]any{"goal": "g", "max_steps": float64(3)})
	require.Equal(t, "g", in.Goal)
	require.Equal(t, 3, in.MaxSteps)

	in = parseDelegateArgs(map[string]any{})
	require.Empty(t, in.Goal)
	require.Zero(t, in.MaxSteps)

	in = parseDelegateArgs(map[string]any{"goal": "g", "max_steps": float64(0)})
	require.Zero(t, in.MaxSteps)
}

func TestDelegateShellContent(t *testing.T) {
	shell := delegateShellContent(DelegateOutput{Summary: "s", Status: string(DelegateStatusSuccess), TokensUsed: 42})
	require.Equal(t, "s", shell["summary"])
	require.Equal(t, "success", shell["status"])
	require.Equal(t, 42, shell["tokens_used"])

	// 空 status 回落 partial，绝不 fail 主循环。
	shell = delegateShellContent(DelegateOutput{Summary: "s"})
	require.Equal(t, "partial", shell["status"])

	// summary 超长先按 DelegateSummaryMaxRunes 截断。
	long := strings.Repeat("字", constants.DelegateSummaryMaxRunes+100)
	shell = delegateShellContent(DelegateOutput{Summary: long, Status: string(DelegateStatusSuccess), TokensUsed: 1})
	require.LessOrEqual(t, len([]rune(shell["summary"].(string))), constants.DelegateSummaryMaxRunes+len("...[truncated]"))
}

func TestDelegateObservationSummary(t *testing.T) {
	t.Run("success renders readable chinese", func(t *testing.T) {
		got := delegateObservationSummary(wrappedDelegateShell("已总结 3 个函数", "success", 42))
		require.Equal(t, "委托子 Agent 完成：已总结 3 个函数（tokens_used=42）", got)
		require.NotContains(t, got, "<untrusted_tool_result>")
	})

	t.Run("partial and failed map to chinese labels", func(t *testing.T) {
		require.Contains(t, delegateObservationSummary(wrappedDelegateShell("部分完成", "partial", 1)), "部分完成")
		require.Contains(t, delegateObservationSummary(wrappedDelegateShell("任务失败", "failed", 1)), "执行失败")
	})

	t.Run("unknown status falls back to done", func(t *testing.T) {
		got := delegateObservationSummary(wrappedDelegateShell("s", "weird", 1))
		require.Contains(t, got, "委托子 Agent 完成")
	})

	t.Run("truncated wrapper still parses", func(t *testing.T) {
		got := delegateObservationSummary(wrappedDelegateShell("s", "success", 7) + "\n[TRUNCATED]")
		require.Contains(t, got, "tokens_used=7")
	})

	t.Run("non-shell content falls back to original", func(t *testing.T) {
		got := delegateObservationSummary("delegate not enabled")
		require.Equal(t, "delegate not enabled", got)
	})

	t.Run("empty summary falls back to original", func(t *testing.T) {
		got := delegateObservationSummary(wrappedDelegateShell("", "success", 1))
		require.Contains(t, got, "<untrusted_tool_result>")
	})
}

func TestSummarizeToolObservation_DelegateBranch(t *testing.T) {
	// 成功观察：结构化外壳解析成可读中文，不再向用户展示原始 JSON shell。
	got := summarizeToolObservation(StratumDelegateToolName, wrappedDelegateShell("子任务完成", "success", 42), domain.ToolTraceStatusSuccess, "")
	require.Equal(t, "委托子 Agent 完成：子任务完成（tokens_used=42）", got)

	// Error 观察先行走 error 分支（不解析外壳）。
	got = summarizeToolObservation(StratumDelegateToolName, "irrelevant", domain.ToolTraceStatusError, "boom")
	require.Equal(t, "stratum_delegate failed: boom", got)

	// 非 delegate 工具不受影响。
	got = summarizeToolObservation("mcp:orders:get", "raw content", domain.ToolTraceStatusSuccess, "")
	require.Equal(t, "mcp:orders:get returned: raw content", got)
}

func TestExecDelegateTool_NotEnabled_ReturnsCorrection(t *testing.T) {
	s := &ReActState{DelegateEnabled: false}
	res := execDelegateTool(context.Background(), port.ToolCall{Name: StratumDelegateToolName, Arguments: map[string]any{"goal": "g"}}, s, time.Now(), zap.NewNop())
	require.Equal(t, domain.ToolTraceStatusSuccess, res.status)
	require.Contains(t, res.content, "delegate not enabled")
	// correction 走 countToolFailure：一次调用未达止损阈值，不触发降级。
	require.Empty(t, s.StopLossTools)
}

func TestExecDelegateTool_DepthLimit_ReturnsCorrection(t *testing.T) {
	s := &ReActState{DelegateEnabled: true, DelegateMaxDepth: 1, DelegateDepth: 1}
	res := execDelegateTool(context.Background(), port.ToolCall{Name: StratumDelegateToolName, Arguments: map[string]any{"goal": "g"}}, s, time.Now(), zap.NewNop())
	require.Equal(t, domain.ToolTraceStatusSuccess, res.status)
	require.Contains(t, res.content, "delegate depth limit reached")
}

func TestExecDelegateTool_MissingExecutor_ReturnsError(t *testing.T) {
	s := &ReActState{DelegateEnabled: true, DelegateMaxDepth: 1}
	res := execDelegateTool(context.Background(), port.ToolCall{Name: StratumDelegateToolName, Arguments: map[string]any{"goal": "g"}}, s, time.Now(), zap.NewNop())
	require.Equal(t, domain.ToolTraceStatusError, res.status)
	require.Equal(t, "delegate executor not configured", res.errMsg)
}

func TestExecDelegateTool_UnknownTool_ReturnsError(t *testing.T) {
	s := &ReActState{
		DelegateEnabled:  true,
		DelegateMaxDepth: 1,
		DelegateExecutor: fixedDelegateExecutor(t, "", DelegateOutput{}),
		ToolExecutionFn: func(context.Context, port.ToolExecutionRequest) (any, error) {
			t.Fatal("unknown tool must not reach executor")
			return nil, nil
		},
	}
	res := execDelegateTool(context.Background(), port.ToolCall{Name: StratumDelegateToolName, Arguments: map[string]any{"goal": "g"}}, s, time.Now(), zap.NewNop())
	require.Equal(t, domain.ToolTraceStatusError, res.status)
	require.Contains(t, res.errMsg, `unknown tool "stratum_delegate"`)
}

func TestExecDelegateTool_RunsClosure_AndFoldsTokens(t *testing.T) {
	var captured port.ToolExecutionRequest
	s := &ReActState{
		DelegateEnabled:  true,
		DelegateMaxDepth: 1,
		AvailableTools:   []port.ToolDefinition{{Name: StratumDelegateToolName, ProviderType: domain.ProviderTypeBuiltin}},
		// 模拟真实 guard：调用 delegate 闭包（闭包内折回 token），再包裹回传。
		ToolExecutionFn: func(ctx context.Context, req port.ToolExecutionRequest) (any, error) {
			captured = req
			if req.DelegateExecutor != nil {
				if _, err := req.DelegateExecutor(ctx, req.Arguments); err != nil {
					return nil, err
				}
			}
			return port.GuardedToolResult{
				ModelContent: wrappedDelegateShell("done", "success", 42),
				Untrusted:    true,
			}, nil
		},
		DelegateExecutor: fixedDelegateExecutor(t, "summarize file", DelegateOutput{
			Summary: "done", Status: string(DelegateStatusSuccess), TokensUsed: 42, StepsUsed: 3, DelegateID: "d1",
		}),
		TotalTokens: 100,
	}
	res := execDelegateTool(context.Background(), port.ToolCall{
		ID: "call-1", Name: StratumDelegateToolName, Arguments: map[string]any{"goal": "summarize file", "max_steps": float64(2)},
	}, s, time.Now(), zap.NewNop())

	require.Equal(t, domain.ToolTraceStatusSuccess, res.status)
	// guard 请求携带工具参数与 delegate 闭包；租户链路字段由 application 层
	// WithToolExecutionFn 闭包注入，graph 层只填充工具相关字段。
	require.Equal(t, "call-1", captured.ToolCallID)
	require.Equal(t, StratumDelegateToolName, captured.Tool.Name)
	require.Equal(t, "summarize file", captured.Arguments["goal"])
	require.NotNil(t, captured.DelegateExecutor, "guard 必须拿到 delegate 闭包")
	// 观察正文是 ResultGuard 包裹的结构化外壳。
	require.Contains(t, res.content, "<untrusted_tool_result>")
	require.Contains(t, res.content, `"tokens_used":42`)
	// token 增量在闭包内折回主状态一次。
	require.Equal(t, 142, s.TotalTokens)
}

func TestBuildDelegateClosure(t *testing.T) {
	s := &ReActState{
		DelegateExecutor: fixedDelegateExecutor(t, "g", DelegateOutput{
			Summary: "done", Status: string(DelegateStatusSuccess), TokensUsed: 42,
		}),
		TotalTokens: 10,
	}
	closure := buildDelegateClosure(s)
	res, err := closure(context.Background(), map[string]any{"goal": "g", "max_steps": float64(3)})
	require.NoError(t, err)
	// StructuredContent 形态：guard 序列化后 ModelContent 直接是外壳 JSON，
	// 无 Content 数组的嵌套转义。
	require.Equal(t, "done", res.StructuredContent["summary"])
	require.Equal(t, string(DelegateStatusSuccess), res.StructuredContent["status"])
	require.Equal(t, 42, res.StructuredContent["tokens_used"])
	require.Equal(t, 52, s.TotalTokens, "token 增量只在闭包折回一次")

	t.Run("executor error propagates without folding tokens", func(t *testing.T) {
		s2 := &ReActState{
			DelegateExecutor: func(context.Context, *ReActState, DelegateInput) (DelegateOutput, error) {
				return DelegateOutput{}, context.DeadlineExceeded
			},
			TotalTokens: 10,
		}
		_, err := buildDelegateClosure(s2)(context.Background(), map[string]any{"goal": "g"})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, 10, s2.TotalTokens, "失败不得折回 token")
	})
}

func TestClassifyToolProvider_Delegate(t *testing.T) {
	ref := classifyToolProvider(StratumDelegateToolName, nil)
	require.Equal(t, domain.ToolTypeInternal, ref.ToolType)
	require.Equal(t, domain.ProviderTypeBuiltin, ref.ProviderType)
	require.Equal(t, StratumDelegateToolName, ref.ProviderID)
	require.Equal(t, StratumDelegateToolName, ref.CapabilityID)
	require.Equal(t, nodeTool, ref.NodeID)
	require.Equal(t, domain.ObservationTypeTool, ref.NodeType)
}

func TestIsReservedToolName_IncludesDelegate(t *testing.T) {
	require.True(t, IsReservedToolName(StratumDelegateToolName))
}

func TestBuildReActGraph_DelegateDistribution(t *testing.T) {
	stub := &delegateCapGW{responses: []port.CapabilityResponse{
		{ToolCalls: []port.ToolCall{{ID: "x1", Name: StratumDelegateToolName, Arguments: map[string]any{"goal": "delegate goal"}}}},
		{Content: "done"},
	}}
	cg, err := BuildReActGraph(stub, NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	out, err := cg.Invoke(context.Background(), ReActState{
		Model:                   "qwen",
		Messages:                []port.LLMMessage{{Role: "user", Content: "delegate it"}},
		AvailableTools:          []port.ToolDefinition{{Name: StratumDelegateToolName, ProviderType: domain.ProviderTypeBuiltin}},
		DelegateEnabled:         true,
		DelegateMaxDepth:        1,
		DelegateDefaultMaxSteps: 5,
		// 模拟真实 guard 链路：调用 delegate 闭包执行子循环、折回 token，再把
		// 返回的结构化外壳经 ResultGuard 包裹回传。
		ToolExecutionFn: func(ctx context.Context, req port.ToolExecutionRequest) (any, error) {
			require.NotNil(t, req.DelegateExecutor, "guard 必须拿到 delegate 闭包")
			res, err := req.DelegateExecutor(ctx, req.Arguments)
			require.NoError(t, err)
			// 模拟真实 guard：StructuredContent 直接序列化为外壳 JSON（无嵌套转义）。
			shell, err := json.Marshal(res.StructuredContent)
			require.NoError(t, err)
			return port.GuardedToolResult{
				ModelContent: "<untrusted_tool_result>\n" + string(shell) + "\n</untrusted_tool_result>",
				Untrusted:    true,
			}, nil
		},
		DelegateExecutor: fixedDelegateExecutor(t, "delegate goal", DelegateOutput{
			Summary: "子任务完成：已总结文件", Status: string(DelegateStatusSuccess), TokensUsed: 42, StepsUsed: 3,
		}),
	}, RunConfig[ReActState]{MaxSteps: 8})
	require.NoError(t, err)

	require.GreaterOrEqual(t, len(stub.llmReqs), 2, "delegate 后主循环继续推理")
	require.Equal(t, 42, out.TotalTokens, "token 增量折回主状态")
	require.Len(t, out.ToolObservations, 1)
	obs := out.ToolObservations[0]
	require.Equal(t, domain.ToolTraceStatusSuccess, obs.Status)
	require.Equal(t, domain.ProviderTypeBuiltin, obs.ProviderType)
	require.Equal(t, domain.ToolTypeInternal, obs.ToolType)
	// Task 8：观察摘要可读（中文 + 无原始 JSON shell），RawText 保留完整外壳。
	require.Equal(t, "委托子 Agent 完成：子任务完成：已总结文件（tokens_used=42）", obs.Summary)
	require.NotContains(t, obs.Summary, "<untrusted_tool_result>")
	require.Contains(t, obs.RawText, `"tokens_used":42`)
}

// TestExecDelegateTool_EmitsRunningEvent 验证 running 事件在子循环执行前发出
// （SSE delegate_status 帧：进入委托即回调一次，消除委托期间主对话静默）；
// buildDelegateClosure 随后补发 finished 帧。fail-closed 分支（未启用/深度超限/
// 缺失 executor）不触发事件——只有真正进入子循环才需要进度反馈。
func TestExecDelegateTool_EmitsRunningEvent(t *testing.T) {
	var events []DelegateEvent
	s := &ReActState{
		DelegateEnabled:  true,
		DelegateMaxDepth: 1,
		AvailableTools:   []port.ToolDefinition{{Name: StratumDelegateToolName, ProviderType: domain.ProviderTypeBuiltin}},
		OnDelegateEvent:  func(ev DelegateEvent) { events = append(events, ev) },
		ToolExecutionFn: func(ctx context.Context, req port.ToolExecutionRequest) (any, error) {
			// running 事件必须先于执行器调用发出。
			require.Len(t, events, 1)
			require.Equal(t, DelegateEventRunning, events[0].Status)
			require.Equal(t, "summarize file", events[0].Goal)
			if req.DelegateExecutor != nil {
				if _, err := req.DelegateExecutor(ctx, req.Arguments); err != nil {
					return nil, err
				}
			}
			return port.GuardedToolResult{ModelContent: wrappedDelegateShell("done", "success", 42), Untrusted: true}, nil
		},
		DelegateExecutor: fixedDelegateExecutor(t, "summarize file", DelegateOutput{
			Summary: "done", Status: string(DelegateStatusSuccess), TokensUsed: 42, DelegateID: "d1",
		}),
	}
	res := execDelegateTool(context.Background(), port.ToolCall{
		Name: StratumDelegateToolName, Arguments: map[string]any{"goal": "summarize file"},
	}, s, time.Now(), zap.NewNop())
	require.Equal(t, domain.ToolTraceStatusSuccess, res.status)
	require.Len(t, events, 2, "running + finished 各一帧")
	require.Equal(t, DelegateEventRunning, events[0].Status)
	require.Equal(t, DelegateEventFinished, events[1].Status)
	require.Equal(t, "d1", events[1].DelegateID)
	require.Equal(t, "done", events[1].Summary)
	require.Equal(t, 42, events[1].TokensUsed)
}

// TestBuildDelegateClosure_EmitsFinishedEvent 验证 finished 事件：成功路径携带
// DelegateID/Summary/TokensUsed/ResultStatus；失败路径仍发 finished 帧，携带
// failed 结果状态 + DelegateID + 固定安全文案（不折回 token）。
func TestBuildDelegateClosure_EmitsFinishedEvent(t *testing.T) {
	t.Run("success carries result", func(t *testing.T) {
		var events []DelegateEvent
		s := &ReActState{
			DelegateExecutor: fixedDelegateExecutor(t, "g", DelegateOutput{
				Summary: "done", Status: string(DelegateStatusSuccess), TokensUsed: 42, DelegateID: "d1",
			}),
			OnDelegateEvent: func(ev DelegateEvent) { events = append(events, ev) },
			TotalTokens:     10,
		}
		_, err := buildDelegateClosure(s)(context.Background(), map[string]any{"goal": "g"})
		require.NoError(t, err)
		require.Len(t, events, 1)
		ev := events[0]
		require.Equal(t, DelegateEventFinished, ev.Status)
		require.Equal(t, "d1", ev.DelegateID)
		require.Equal(t, "done", ev.Summary)
		require.Equal(t, 42, ev.TokensUsed)
		require.Equal(t, string(DelegateStatusSuccess), ev.ResultStatus)
	})

	t.Run("error emits finished with failed result", func(t *testing.T) {
		var events []DelegateEvent
		s := &ReActState{
			// 真实路径失败时 agent.go 仍回传带 DelegateID 的 DelegateOutput（成功帧同源）。
			DelegateExecutor: func(context.Context, *ReActState, DelegateInput) (DelegateOutput, error) {
				return DelegateOutput{DelegateID: "d1"}, context.DeadlineExceeded
			},
			OnDelegateEvent: func(ev DelegateEvent) { events = append(events, ev) },
			TotalTokens:     10,
		}
		_, err := buildDelegateClosure(s)(context.Background(), map[string]any{"goal": "g"})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Len(t, events, 1)
		ev := events[0]
		require.Equal(t, DelegateEventFinished, ev.Status)
		require.Equal(t, "g", ev.Goal)
		require.Equal(t, "d1", ev.DelegateID, "失败帧携带 delegate_id 与成功帧同源")
		require.Equal(t, string(DelegateStatusFailed), ev.ResultStatus, "失败帧携带 failed 结果状态")
		require.Equal(t, "委托子 Agent 执行失败", ev.Summary, "失败帧 summary 用固定安全文案")
		require.Zero(t, ev.TokensUsed)
		require.Equal(t, 10, s.TotalTokens, "失败不得折回 token")
	})
}
