package application

// C2c 合成单测：synthesizeApprovalResume 从已批准载荷合成 assistant 工具调用 P1，
// 置 SkipNextLLM=true 使续跑直接执行已批准参数；工具查不到时 fail-safe 回退；
// ToolCallID 去重防止 LLM 上下文孤立重复 tool_call。

import (
	"testing"

	agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
)

func synthState() agentgraph.ReActState {
	return agentgraph.ReActState{
		Messages: []port.LLMMessage{{Role: "user", Content: "resume"}},
		AvailableTools: []port.ToolDefinition{
			{Name: "mcp:srv:delete", ServerID: "srv", CapabilityID: "delete", ProviderType: "mcp"},
		},
	}
}

// C2c 命中：AvailableTools 按 ServerID+CapabilityID 查到 Name → 合成 P1 + SkipNextLLM。
func TestSynthesizeApprovalResume_Hit(t *testing.T) {
	payload := resumePayload("e1", "a1", "u1")
	out, ok := synthesizeApprovalResume(synthState(), []ToolApprovalPayload{payload})
	require.True(t, ok)
	require.True(t, out.SkipNextLLM)
	require.Len(t, out.Messages, 2, "user + 合成 P1")
	last := out.Messages[1]
	require.Equal(t, "assistant", last.Role)
	require.Len(t, last.ToolCalls, 1)
	tc := last.ToolCalls[0]
	require.Equal(t, "tc1", tc.ID)
	require.Equal(t, "mcp:srv:delete", tc.Name, "Name 取自 AvailableTools 查表（payload.ToolName 是 capability id）")
	require.Equal(t, payload.Arguments, tc.Arguments, "执行参数即已批准载荷参数")
}

// C2c fail-safe：查不到工具（被删/改名）→ 不合成、不跳过，走 LLM 原路径。
func TestSynthesizeApprovalResume_NoToolFallback(t *testing.T) {
	state := agentgraph.ReActState{Messages: []port.LLMMessage{{Role: "user", Content: "resume"}}}
	payload := resumePayload("e1", "a1", "u1")
	out, ok := synthesizeApprovalResume(state, []ToolApprovalPayload{payload})
	require.False(t, ok)
	require.False(t, out.SkipNextLLM)
	require.Len(t, out.Messages, 1, "不追加 P1")
}

// C2c 去重：恢复消息已有同 ID tool_call → 生成新 uuid 避免上下文孤立重复。
func TestSynthesizeApprovalResume_DedupToolCallID(t *testing.T) {
	state := synthState()
	state.Messages = []port.LLMMessage{
		{Role: "user", Content: "resume"},
		{Role: "assistant", ToolCalls: []port.ToolCall{{ID: "tc1", Name: "other"}}},
	}
	payload := resumePayload("e1", "a1", "u1")
	out, ok := synthesizeApprovalResume(state, []ToolApprovalPayload{payload})
	require.True(t, ok)
	require.Len(t, out.Messages, 3)
	last := out.Messages[2].ToolCalls[0]
	require.NotEqual(t, "tc1", last.ID, "同 ID 应生成新唯一 ID")
	require.Equal(t, "mcp:srv:delete", last.Name)
}

// C2c：payload.ToolCallID 为空 → 生成新 uuid。
func TestSynthesizeApprovalResume_EmptyCallIDGenerates(t *testing.T) {
	payload := resumePayload("e1", "a1", "u1")
	payload.ToolCallID = ""
	out, ok := synthesizeApprovalResume(synthState(), []ToolApprovalPayload{payload})
	require.True(t, ok)
	require.NotEmpty(t, out.Messages[1].ToolCalls[0].ID)
}

// C2c 多 tool_call 场景：历史消息已含多个 assistant tool_call（LLM 一次生成多个
// 调用、部分待批），续跑合成只在末尾追加单个已批准调用 P1——旧 calls 原样保留、
// 不被重复执行、不触发重复审批；P1 后经 nodeTool 单次执行。
func TestSynthesizeApprovalResume_MultiToolCallKeepsHistoryAppendsSingleP1(t *testing.T) {
	state := synthState()
	state.Messages = []port.LLMMessage{
		{Role: "user", Content: "resume"},
		// 历史：LLM 一次生成两个调用，其中一个已批准续跑
		{Role: "assistant", ToolCalls: []port.ToolCall{
			{ID: "tc-hist-1", Name: "mcp:srv:get", Arguments: map[string]any{"id": "1"}},
			{ID: "tc1", Name: "mcp:srv:delete", Arguments: map[string]any{"id": "1"}},
		}},
		{Role: "tool", ToolCallID: "tc-hist-1", Content: "result-1"},
	}
	payload := resumePayload("e1", "a1", "u1")
	payload.ToolCallID = "tc1" // 与历史调用同 ID → 去重

	out, ok := synthesizeApprovalResume(state, []ToolApprovalPayload{payload})

	require.True(t, ok)
	require.True(t, out.SkipNextLLM)
	require.Len(t, out.Messages, 4, "user + 历史两消息 + 合成 P1")

	// 历史消息原样保留（值语义拷贝，未被修改；去重只影响合成 P1，不改历史）
	require.Equal(t, 2, len(out.Messages[1].ToolCalls))
	require.Equal(t, "tc-hist-1", out.Messages[1].ToolCalls[0].ID)
	require.Equal(t, "mcp:srv:get", out.Messages[1].ToolCalls[0].Name)
	require.Equal(t, "tc1", out.Messages[1].ToolCalls[1].ID)
	require.Equal(t, "mcp:srv:delete", out.Messages[1].ToolCalls[1].Name)

	// 合成 P1 只含单个已批准调用
	last := out.Messages[3]
	require.Equal(t, "assistant", last.Role)
	require.Len(t, last.ToolCalls, 1, "只合成单个已批准调用，旧 calls 不复制")
	tc := last.ToolCalls[0]
	require.NotEqual(t, "tc1", tc.ID, "历史已存在同 ID → 生成新唯一 ID")
	require.Equal(t, "mcp:srv:delete", tc.Name)
	require.Equal(t, payload.Arguments, tc.Arguments, "执行参数即已批准载荷参数")
}

// ── 多审批续跑合成：N 载荷 → 一条 assistant 消息 N 条 tool_call ───────────

// 批量统一续跑：全部审批终态后一次合成——单条 assistant 消息携带 N 条 tool_call，
// SkipNextLLM=true 直接执行已批准参数，终态条目同样合成（guard 命中后返回友好错误）。
func TestSynthesizeApprovalResume_MultiPayloadsOneMessage(t *testing.T) {
	state := synthState()
	state.AvailableTools = append(state.AvailableTools, port.ToolDefinition{
		Name: "mcp:srv:archive", ServerID: "srv", CapabilityID: "archive", ProviderType: "mcp",
	})
	p1 := resumePayload("e1", "a1", "u1")
	p2 := resumePayload("e1", "a1", "u1")
	p2.ToolCallID = "tc2"
	p2.ToolName = "archive"
	p2.Arguments = map[string]any{"id": "2"}
	d, err := CanonicalToolArgumentsDigest(p2.Arguments)
	require.NoError(t, err)
	p2.ArgumentsDigest = d

	out, ok := synthesizeApprovalResume(state, []ToolApprovalPayload{p1, p2})

	require.True(t, ok)
	require.True(t, out.SkipNextLLM)
	require.Len(t, out.Messages, 2, "user + 一条合成 assistant 消息")
	last := out.Messages[1]
	require.Equal(t, "assistant", last.Role)
	require.Len(t, last.ToolCalls, 2, "一条 assistant 消息含 N 条 tool_call")
	require.Equal(t, "tc1", last.ToolCalls[0].ID)
	require.Equal(t, "mcp:srv:delete", last.ToolCalls[0].Name)
	require.Equal(t, p1.Arguments, last.ToolCalls[0].Arguments)
	require.Equal(t, "tc2", last.ToolCalls[1].ID)
	require.Equal(t, "mcp:srv:archive", last.ToolCalls[1].Name)
	require.Equal(t, p2.Arguments, last.ToolCalls[1].Arguments)
}

// 批量 fail-safe：N 载荷中工具被删的条目跳过（continue），其余照常合成——不因单个
// 条目不可达而丢弃整批已批准调用。
func TestSynthesizeApprovalResume_MultiPayloadsSkipUnavailableTool(t *testing.T) {
	state := synthState() // AvailableTools 只含 mcp:srv:delete
	p1 := resumePayload("e1", "a1", "u1")
	p2 := resumePayload("e1", "a1", "u1")
	p2.ToolCallID = "tc2"
	p2.ToolName = "gone" // 无匹配工具
	d, err := CanonicalToolArgumentsDigest(p2.Arguments)
	require.NoError(t, err)
	p2.ArgumentsDigest = d

	out, ok := synthesizeApprovalResume(state, []ToolApprovalPayload{p1, p2})

	require.True(t, ok)
	require.True(t, out.SkipNextLLM)
	require.Len(t, out.Messages[1].ToolCalls, 1, "查不到的条目跳过，其余合成")
	require.Equal(t, "tc1", out.Messages[1].ToolCalls[0].ID)
	require.Equal(t, "mcp:srv:delete", out.Messages[1].ToolCalls[0].Name)
}
