package application

import (
	"context"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	versioningdomain "github.com/byteBuilderX/stratum/internal/versioning/domain"
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
		Registry:       NewRegistry(agentRepo, zap.NewNop()),
		TenantResolver: tenantResolverFake{gateway: llm},
		MCPTools: fullChainMCPTools{tools: []port.ToolDefinition{{
			Name: "mcp:srv:delete", ProviderType: domain.ProviderTypeMCP,
			ServerID: "srv", CapabilityID: "delete",
			InputSchema: map[string]any{"type": "object"},
		}}},
		MCPToolExecutor:      executor,
		ToolAuthorizer:       NewToolAuthorizer(stubToolUserScopeResolver{scope: port.ToolUserScope{UserActive: true, AllowsTool: true}}),
		ApprovalService:      approvalSvc,
		ChatStore:            resumeChatRepo{conv: &domain.ChatConversation{ID: "conv-alive"}},
		CheckpointStore:      cp,
		TenantRoleResolver:   stubTenantRole{role: "member"},
		TenantModelValidator: &stubTenantModelValidator{},
		Logger:               zap.NewNop(),
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

// systemAssistantProfileRepo 精简 port.AgentRepo 桩：续跑全链测试复用（原定义于
// 已删除的 system_assistant_profile_test.go，保留 port 必需 5 方法）。
type systemAssistantProfileRepo struct {
	cfgs []*domain.AgentConfig
	err  error
}

func (r systemAssistantProfileRepo) Register(_ context.Context, _ *domain.AgentConfig, _ *auditdomain.ResourceChangeAuditEvent, _ []string) error {
	return nil
}
func (r systemAssistantProfileRepo) Get(context.Context, string) (*domain.AgentConfig, bool, error) {
	if r.err != nil {
		return nil, false, r.err
	}
	if len(r.cfgs) == 0 {
		return nil, false, nil
	}
	return r.cfgs[0], true, nil
}
func (r systemAssistantProfileRepo) GetAll(context.Context) ([]*domain.AgentConfig, error) {
	return r.cfgs, r.err
}
func (r systemAssistantProfileRepo) Update(_ context.Context, _ *domain.AgentConfig, _ *auditdomain.ResourceChangeAuditEvent, _ string, _ bool, _ *versioningdomain.Version) error {
	return nil
}

func (r systemAssistantProfileRepo) Rollback(_ context.Context, _ *domain.AgentConfig, _ *auditdomain.ResourceChangeAuditEvent, _, _ string) error {
	return nil
}
func (r systemAssistantProfileRepo) Remove(_ context.Context, _ string, _ *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}

var _ port.AgentRepo = systemAssistantProfileRepo{}

// ── 批量全链：1 approved + 1 rejected → 统一终态续跑，仅 approved 执行 ────────

// 阶段四批量全链：同一 checkpoint 含两个审批（approval-1 approved delete +
// approval-2 rejected archive）。统一终态后续跑一次：合成一条 assistant 消息两条
// tool_call；approved 经 ExecuteApproved CAS 消费并真实执行（claimed==1、
// executor.calls==1），rejected 走终态分支返回友好错误（%w ErrApprovalNotApproved
// + 工具审批未通过）成为工具观察（非 ToolApprovalRequiredError，不中止整图），
// LLM 感知未执行后自行收尾。mixed 批次 terminal=false、runErr==nil → checkpoint
// 以 completed 收尾。断言 rejected 绝不触达执行器、绝不消费批准。
func TestApprovalResume_BatchMixedApprovedAndRejected_ExecutesApprovedOnly(t *testing.T) {
	p1 := resumePayloadTool("e1", "a1", "user-1", "tc1", "delete", map[string]any{"id": "1"})
	p2 := resumePayloadTool("e1", "a1", "user-1", "tc2", "archive", map[string]any{"id": "2"})
	approvalSvc, repo := approvedToolApprovalBatch(t, p1, p2)
	// approval-2 改为 rejected：整批非全 approved，该条按终态处理（不消费、不执行）。
	rejected := repo.rowByID["approval-2"]
	rejected.Status = string(domain.ToolApprovalRejected)
	repo.rowByID["approval-2"] = rejected

	cp := &approvalResumeCheckpointStub{
		cp: &domain.AgentExecutionCheckpoint{
			ExecutionID: "e1", AgentID: "a1", UserID: "user-1",
			Status: domain.ExecStatusWaitingApproval, RunGeneration: 1,
			RuntimeStateJSON: []byte(`{"approval_ids":["approval-1","approval-2"],"approval_id":"approval-1"}`),
		},
	}
	agentRepo := systemAssistantProfileRepo{cfgs: []*domain.AgentConfig{{
		ID: "a1", Name: "Resume Agent", Type: domain.ReActAgent,
		SystemPrompt: "sys", LLMModel: "qwen-plus", MaxIterations: 3,
		MCPToolIDs: []string{"mcp:srv:delete", "mcp:srv:archive"},
	}}}
	llm := &toolPermissionLLM{responses: []port.CapabilityResponse{{Content: "batch finished"}}}
	executor := &recordingMCPExecutor{}
	svc := NewAgentService(AgentServiceDeps{
		Registry:       NewRegistry(agentRepo, zap.NewNop()),
		TenantResolver: tenantResolverFake{gateway: llm},
		MCPTools: fullChainMCPTools{tools: []port.ToolDefinition{
			{Name: "mcp:srv:delete", ProviderType: domain.ProviderTypeMCP, ServerID: "srv", CapabilityID: "delete", InputSchema: map[string]any{"type": "object"}},
			{Name: "mcp:srv:archive", ProviderType: domain.ProviderTypeMCP, ServerID: "srv", CapabilityID: "archive", InputSchema: map[string]any{"type": "object"}},
		}},
		MCPToolExecutor:      executor,
		ToolAuthorizer:       NewToolAuthorizer(stubToolUserScopeResolver{scope: port.ToolUserScope{UserActive: true, AllowsTool: true}}),
		ApprovalService:      approvalSvc,
		ChatStore:            resumeChatRepo{conv: &domain.ChatConversation{ID: "conv-alive"}},
		CheckpointStore:      cp,
		TenantRoleResolver:   stubTenantRole{role: "member"},
		TenantModelValidator: &stubTenantModelValidator{},
		Logger:               zap.NewNop(),
	})

	result, _, err := svc.Execute(context.Background(), "a1",
		ExecRequest{UserID: "user-1", Query: "resume", ConversationID: "conv-alive"},
		ExecMeta{TenantID: "t1", TraceID: "tr1", ExecutionID: "e1"})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "batch finished", result.Output, "LLM 感知 rejected 未执行后正常收尾")
	require.Equal(t, 1, repo.claimed, "仅 approved 的 delete 经 ExecuteApproved CAS 消费，rejected 不得消费")
	require.Equal(t, 1, executor.calls, "仅 delete 真实执行；rejected archive 绝不触达执行器")
	require.Equal(t, p1.Arguments, executor.args, "执行参数必须是已批准载荷的参数")
	require.GreaterOrEqual(t, cp.completed, 1, "mixed 批次 runErr==nil 以 completed 收尾")
	require.Len(t, llm.requests, 1, "SkipNextLLM 跳过首轮 LLM 生成，capGW 仅收到末轮")

	require.Len(t, result.ToolObservations, 2, "delete 成功观察 + archive 友好错误观察各一")
	// rejected archive 以友好错误观察呈现（非 ToolApprovalRequiredError → 不中止整图）。
	var rejectedObs *domain.ToolObservation
	for i := range result.ToolObservations {
		obs := &result.ToolObservations[i]
		if strings.Contains(obs.ErrorMessage, "工具审批未通过") {
			rejectedObs = obs
			break
		}
	}
	require.NotNil(t, rejectedObs, "rejected archive 必须以友好错误观察呈现")
	require.Equal(t, "archive", rejectedObs.CapabilityID)
	require.Equal(t, domain.ToolTraceStatusError, rejectedObs.Status)
}
