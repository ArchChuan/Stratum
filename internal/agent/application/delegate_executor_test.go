package application

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// delegateRunnerCapGW 是 buildDelegateExecutor 全链路测试的 CapabilityGateway stub：
// 按序列返回 LLM 响应，并记录每次请求（Messages/工具集），供断言子循环组装。
type delegateRunnerCapGW struct {
	responses []port.CapabilityResponse
	requests  []port.CapabilityRequest
	idx       int
	err       error
}

func (s *delegateRunnerCapGW) Route(_ context.Context, req port.CapabilityRequest) (port.CapabilityResponse, error) {
	if req.LLM != nil {
		s.requests = append(s.requests, req)
	}
	if s.err != nil {
		return port.CapabilityResponse{}, s.err
	}
	if s.idx < len(s.responses) {
		r := s.responses[s.idx]
		s.idx++
		return r, nil
	}
	return port.CapabilityResponse{Content: "done"}, nil
}

func delegateAgent() *BaseAgent {
	return &BaseAgent{Logger: zap.NewNop(), Ledger: agentgraph.NoopTokenRecorder{}}
}

func delegateExecContext(gw port.CapabilityGateway) agentExecContext {
	return agentExecContext{
		agentID:      "agent-1",
		systemPrompt: "SYSTEM_PROMPT",
		memCtx:       "MEM_CTX_CONTENT",
		globalSuffix: "GLOBAL_SUFFIX",
		cfg: &ExecutionConfig{
			TenantID: "tenant-1", UserID: "user-1", ExecutionID: "exec-1",
			ConversationID: "11111111-1111-1111-1111-111111111111",
		},
		capGW: gw,
	}
}

// delegateShellContentForTest 返回子循环应产出的结构化外壳 JSON（对齐
// DelegateSummaryInstruction 的输出格式）。
func delegateShellContentForTest(summary, status string, tokens int) string {
	return `{"summary":"` + summary + `","status":"` + status + `","tokens_used":` + strconv.Itoa(tokens) + `}`
}

func delegateParentState(maxDepth, depth int) *agentgraph.ReActState {
	return &agentgraph.ReActState{
		Model:                   "qwen",
		DelegateDepth:           depth,
		DelegateMaxDepth:        maxDepth,
		DelegateDefaultMaxSteps: 5,
		TotalTokens:             100,
		Messages:                []port.LLMMessage{{Role: "user", Content: "parent message"}},
		AvailableTools: []port.ToolDefinition{
			{Name: agentgraph.StratumDelegateToolName, ProviderType: domain.ProviderTypeBuiltin},
			{Name: "mcp:orders:get", ProviderType: domain.ProviderTypeMCP},
		},
	}
}

// firstSubLoopLLM 返回子循环第一次 LLM 请求的 Messages 与工具集。
func firstSubLoopLLM(t *testing.T, gw *delegateRunnerCapGW) (msgs []port.LLMMessage, tools []port.ToolDefinition) {
	require.NotEmpty(t, gw.requests, "子循环必须至少发起一次 LLM 调用")
	llm := gw.requests[0].LLM
	require.NotNil(t, llm)
	return llm.Messages, llm.Tools
}

// TestBuildDelegateExecutor_SubLoopAssemblyAndTokenFold 验证 buildDelegateExecutor
// 的子循环组装与 token 折回（镜像 buildPlanNodeExecutor 的 delta 语义）：
// child := parent 值拷贝继承配置；Messages 整体替换为 [system(带 memCtx), user(goal)]；
// child.DelegateDepth = parent+1；深度参数化过滤（默认 MaxDepth=1 时子循环不再可
// 委托）；只回传摘要+状态+token 增量，父状态不写子内容。
func TestBuildDelegateExecutor_SubLoopAssemblyAndTokenFold(t *testing.T) {
	gw := &delegateRunnerCapGW{responses: []port.CapabilityResponse{
		{Content: delegateShellContentForTest("sub done", "success", 50), Usage: port.TokenUsage{Total: 50}},
	}}
	parent := delegateParentState(1, 0) // 主循环 depth=0，MaxDepth=1
	exec := delegateAgent().buildDelegateExecutor(delegateExecContext(gw), gw)

	out, err := exec(context.Background(), parent, agentgraph.DelegateInput{Goal: "summarize file"})
	require.NoError(t, err)
	// 摘要/状态/用量回传：子循环最终 Output 解析出内层可读 summary（非 JSON 外壳
	// 原文），status 从外壳解析，token 增量 = final − child（父基线 100 + 子循环 50）。
	require.Equal(t, "sub done", out.Summary)
	require.Equal(t, string(agentgraph.DelegateStatusSuccess), out.Status)
	require.Equal(t, 50, out.TokensUsed)
	require.GreaterOrEqual(t, out.StepsUsed, 1)
	require.NotEmpty(t, out.DelegateID)

	// 子循环组装：首条 system 消息带父 system prompt + memCtx + 全局后缀；
	// user 消息 = goal + 摘要指令。
	msgs, tools := firstSubLoopLLM(t, gw)
	require.Len(t, msgs, 2, "子循环只含 system + user 两条消息，父历史不写入子上下文")
	require.Equal(t, "system", msgs[0].Role)
	require.Contains(t, msgs[0].Content, "SYSTEM_PROMPT")
	require.Contains(t, msgs[0].Content, "MEM_CTX_CONTENT")
	require.Contains(t, msgs[0].Content, "GLOBAL_SUFFIX")
	userMsg := msgs[1].Content
	require.Contains(t, userMsg, "summarize file")
	require.Contains(t, userMsg, agentgraph.DelegateSummaryInstruction)

	// 深度参数化过滤：MaxDepth=1 时 child.DelegateDepth=1 >= 1，子循环不再暴露
	// delegate 工具；其余工具保留。
	require.NotContains(t, toolNames(tools), agentgraph.StratumDelegateToolName)
	require.Contains(t, toolNames(tools), "mcp:orders:get")

	// 父状态未被子内容污染：TotalTokens 保持不变（delta 由调用方闭包折回）。
	require.Equal(t, 100, parent.TotalTokens)
}

// TestBuildDelegateExecutor_DepthParametricFiltering 验证深度参数化过滤（R16）：
// MaxDepth=2 时 depth=1 的子循环仍暴露 delegate 工具（可再委托一层）；
// MaxDepth=1 时 depth=0 的子循环即被移除（还原"主→子一层"）。
func TestBuildDelegateExecutor_DepthParametricFiltering(t *testing.T) {
	t.Run("max depth 2 keeps delegate for first sub-loop", func(t *testing.T) {
		gw := &delegateRunnerCapGW{responses: []port.CapabilityResponse{{Content: "done"}}}
		parent := delegateParentState(2, 0) // 主循环 depth=0，MaxDepth=2
		exec := delegateAgent().buildDelegateExecutor(delegateExecContext(gw), gw)

		_, err := exec(context.Background(), parent, agentgraph.DelegateInput{Goal: "g"})
		require.NoError(t, err)
		_, tools := firstSubLoopLLM(t, gw)
		require.Contains(t, toolNames(tools), agentgraph.StratumDelegateToolName,
			"MaxDepth=2 时 depth=1 的子循环必须仍可再委托一层")
	})

	t.Run("max depth 1 removes delegate from sub-loop", func(t *testing.T) {
		gw := &delegateRunnerCapGW{responses: []port.CapabilityResponse{{Content: "done"}}}
		parent := delegateParentState(1, 0)
		exec := delegateAgent().buildDelegateExecutor(delegateExecContext(gw), gw)

		_, err := exec(context.Background(), parent, agentgraph.DelegateInput{Goal: "g"})
		require.NoError(t, err)
		_, tools := firstSubLoopLLM(t, gw)
		require.NotContains(t, toolNames(tools), agentgraph.StratumDelegateToolName)
	})
}

// TestBuildDelegateExecutor_ChildStepsReset 验证子循环 Steps 重置（R17，security
// 中-2）：child := parent 值拷贝会继承父循环已消耗的 Steps，父步数 ≥ 子
// MaxLLMSteps-1 时 react_llm.go 的收尾门控会在子循环首调即剥离工具。断言子循环
// 首次 LLM 请求仍携带工具，证明 Steps 已归零。
func TestBuildDelegateExecutor_ChildStepsReset(t *testing.T) {
	gw := &delegateRunnerCapGW{responses: []port.CapabilityResponse{{Content: "done"}}}
	parent := delegateParentState(1, 0)
	parent.Steps = 100 // 父循环已消耗大量步数，child := parent 会继承
	parent.MaxLLMSteps = 5
	exec := delegateAgent().buildDelegateExecutor(delegateExecContext(gw), gw)

	_, err := exec(context.Background(), parent, agentgraph.DelegateInput{Goal: "g"})
	require.NoError(t, err)
	_, tools := firstSubLoopLLM(t, gw)
	require.Contains(t, toolNames(tools), "mcp:orders:get",
		"子循环 Steps 必须重置为 0，否则首调即被 MaxLLMSteps 门控剥离全部工具")
}

// TestParseDelegateSummary 验证从子循环 final.Output 提取内层可读摘要（M1，
// product review）：JSON 外壳 → 提取 summary 字段；非 JSON 原文 → 回落原文；
// 空 summary → 回落原文。
func TestParseDelegateSummary(t *testing.T) {
	t.Run("extracts inner summary from shell", func(t *testing.T) {
		raw := `{"summary":"完成了订单汇总，共 12 条","status":"success","tokens_used":50}`
		require.Equal(t, "完成了订单汇总，共 12 条", parseDelegateSummary(raw))
	})
	t.Run("falls back to raw text when not a shell", func(t *testing.T) {
		raw := "任务已完成，共汇总 12 条订单。"
		require.Equal(t, raw, parseDelegateSummary(raw))
	})
	t.Run("falls back to raw when summary empty", func(t *testing.T) {
		raw := `{"summary":"","status":"partial","tokens_used":5}`
		require.Equal(t, raw, parseDelegateSummary(raw))
	})
	t.Run("scans trailing brace when output has prose", func(t *testing.T) {
		raw := "done. {\"summary\":\"内层摘要\",\"status\":\"success\",\"tokens_used\":9}"
		require.Equal(t, "内层摘要", parseDelegateSummary(raw))
	})
}

// TestBuildDelegateExecutor_InvokeErrorWrapped 验证子循环 invoke 失败时：原始错误
// 只进日志并 wrap 上抛（不吞错、不泄内部标识进错误正文），且不产出部分结果。
func TestBuildDelegateExecutor_InvokeErrorWrapped(t *testing.T) {
	upstreamErr := errors.New("upstream down")
	gw := &delegateRunnerCapGW{err: upstreamErr}
	parent := delegateParentState(1, 0)
	exec := delegateAgent().buildDelegateExecutor(delegateExecContext(gw), gw)

	out, err := exec(context.Background(), parent, agentgraph.DelegateInput{Goal: "g"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "delegate sub-agent invoke:")
	require.ErrorIs(t, err, upstreamErr)
	require.Empty(t, out.Summary)
	require.Zero(t, out.TokensUsed)
	require.Equal(t, 100, parent.TotalTokens, "失败不得折回 token")
}

func toolNames(tools []port.ToolDefinition) []string {
	names := make([]string, 0, len(tools))
	for _, td := range tools {
		names = append(names, td.Name)
	}
	return names
}
