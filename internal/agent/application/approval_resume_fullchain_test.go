package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fullChainMCPTools 是 MCPToolProvider 桩：返回与批准载荷匹配的工具定义。
type fullChainMCPTools struct {
	tools []port.ToolDefinition
}

func (m fullChainMCPTools) ToolsForServer(context.Context, string, string) []port.ToolDefinition {
	return m.tools
}

// fullChainResourceGuard 是 SystemResourceGuard 桩：所有绑定视为用户自有（不触发
// 平台托管净化），MCPToolIDs 非空时 sanitizeRuntimeBindings 仍需非 nil guard。
type fullChainResourceGuard struct{}

func (fullChainResourceGuard) IsPlatformManagedMCPServer(context.Context, string, string) (bool, error) {
	return false, nil
}
func (fullChainResourceGuard) PlatformManagedMCPServerIDs(context.Context, string) ([]string, error) {
	return nil, nil
}
func (fullChainResourceGuard) PlatformManagedWorkspaceIDs(context.Context, string) ([]string, error) {
	return nil, nil
}

// recordingMCPExecutor 记录工具执行参数，断言已批准参数被原样执行。
type recordingMCPExecutor struct {
	calls int
	args  map[string]any
}

func (e *recordingMCPExecutor) ExecuteMCPTool(_ context.Context, _, _ string, args map[string]any) (port.MCPToolResult, error) {
	e.calls++
	e.args = args
	return port.MCPToolResult{}, nil
}

// 场景 4 关键回归：checkpoint 为 waiting_approval 且消息快照为空（存量循环形态，
// runtime 仅存 approval_id 标记），有效批准 → C2 合成 P1 直接执行（不再经 LLM
// 重新生成参数）。走 AgentService.Execute 全链，串联
// maybeResumeApproval→buildApprovalResumeOptions→executeReAct→synthesizeApprovalResume。
// 断言：批准被 ExecuteApproved CAS 消费一次、checkpoint 终态 completed、工具按批准
// 参数执行、首轮 LLM 被 SkipNextLLM 跳过（capGW 仅收到末轮）。
func TestApprovalResume_EmptySnapshotCheckpoint_ExecutesApprovedPayload(t *testing.T) {
	payload := resumePayload("e1", "a1", "user-1")
	approvalSvc, repo := approvedToolApproval(t, payload)
	cp := &approvalResumeCheckpointStub{
		cp: &domain.AgentExecutionCheckpoint{
			ExecutionID: "e1", AgentID: "a1", UserID: "user-1",
			Status: domain.ExecStatusWaitingApproval, RunGeneration: 1,
			RuntimeStateJSON: []byte(`{"approval_id":"approval-1"}`),
		},
	}
	agentRepo := systemAssistantProfileRepo{cfgs: []*domain.AgentConfig{{
		ID: "a1", Name: "Resume Agent", Type: domain.ReActAgent,
		SystemPrompt: "sys", LLMModel: "qwen-plus", MaxIterations: 3,
		MCPToolIDs: []string{"mcp:srv:delete"},
	}}}
	llm := &toolPermissionLLM{responses: []port.CapabilityResponse{{Content: "approved tool executed"}}}
	executor := &recordingMCPExecutor{}
	svc := NewAgentService(AgentServiceDeps{
		Registry:       NewRegistry(agentRepo, BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantResolver: tenantResolverFake{gateway: llm},
		MCPTools: fullChainMCPTools{tools: []port.ToolDefinition{{
			Name: "mcp:srv:delete", ProviderType: domain.ProviderTypeMCP,
			ServerID: "srv", CapabilityID: "delete",
			InputSchema: map[string]any{"type": "object"},
		}}},
		MCPToolExecutor:     executor,
		ToolAuthorizer:      NewToolAuthorizer(stubToolUserScopeResolver{scope: port.ToolUserScope{UserActive: true, AllowsTool: true}}),
		ApprovalService:     approvalSvc,
		ChatStore:           resumeChatRepo{conv: &domain.ChatConversation{ID: "conv-alive"}},
		CheckpointStore:     cp,
		TenantRoleResolver:  stubTenantRole{role: "member"},
		SystemResourceGuard: fullChainResourceGuard{},
		Logger:              zap.NewNop(),
	})

	result, _, err := svc.Execute(context.Background(), "a1",
		ExecRequest{UserID: "user-1", Query: "resume", ConversationID: "conv-alive"},
		ExecMeta{TenantID: "t1", TraceID: "tr1", ExecutionID: "e1"})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "approved tool executed", result.Output)
	require.Equal(t, 1, repo.claimed, "已批准工具调用必须经 ExecuteApproved CAS 消费一次")
	// MarkCompleted 调用两次均属设计内：graph 收尾 finalizeReActCheckpoint + 应用层
	// finishApprovalResume，两者幂等；断言"成功收尾"至少发生一次。
	require.GreaterOrEqual(t, cp.completed, 1, "续跑成功应以 completed 收尾")
	require.Equal(t, 1, executor.calls, "工具必须被真实执行一次")
	require.Equal(t, payload.Arguments, executor.args, "执行参数必须是已批准载荷的参数")
	require.Len(t, llm.requests, 1, "SkipNextLLM 跳过首轮 LLM 生成，capGW 仅收到末轮")
}
