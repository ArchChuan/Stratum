package application

// M2 审批续跑单测：resolveApprovalResume 越权归属（SECURITY-HIGH）、
// buildApprovalResumeOptions guard 命中/回退、claimApprovalResume 分代抢占、
// finishApprovalResume 回滚/终态条件（SECURITY-MEDIUM-2）。
//
// 覆盖的安全语义：
//   - 发起人放行但 checkpoint 归属不一致（双保险）→ ErrApprovalRoleDenied；
//   - 非发起人 member 续跑 → ErrApprovalRoleDenied（fail-closed，关闭存在性 oracle）；
//   - 非发起人 admin/owner → 放行；
//   - guard 命中批准载荷 → ExecuteApproved 单次消费；不一致调用回退正常授权/审批路径；
//   - 抢占后运行失败且未消费批准 → UpdateStatusFrom(running→waiting_approval) 可重试；
//     已消费 → Terminate(failed) 保留对账历史；断线/审批等待错误保留现状。

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// approvalResumeCheckpointStub 是审批续跑链路的 CheckpointRepo 桩：GetLatest 可脚本化
// 返回 waiting_approval checkpoint，并记录 AdvanceRunGeneration/UpdateStatusFrom/
// Terminate/MarkCompleted 调用供断言。
type approvalResumeCheckpointStub struct {
	cp        *domain.AgentExecutionCheckpoint
	err       error
	genErr    error // AdvanceRunGeneration CAS 失败注入
	statusErr error // UpdateStatusFrom CAS 失败注入

	advanceCalls int
	statusCalls  int
	statusFrom   string
	statusTo     string
	terminated   string
	completed    int
}

func (s *approvalResumeCheckpointStub) GetLatest(context.Context, string, string) (*domain.AgentExecutionCheckpoint, error) {
	return s.cp, s.err
}
func (s *approvalResumeCheckpointStub) AdvanceRunGeneration(context.Context, string, string, int) error {
	s.advanceCalls++
	return s.genErr
}
func (s *approvalResumeCheckpointStub) UpdateStatusFrom(_ context.Context, _, _ string, from, to string) error {
	s.statusCalls++
	s.statusFrom = from
	s.statusTo = to
	return s.statusErr
}
func (s *approvalResumeCheckpointStub) Terminate(_ context.Context, _, _ string, status string) error {
	s.terminated = status
	return nil
}
func (s *approvalResumeCheckpointStub) MarkCompleted(context.Context, string, string) error {
	s.completed++
	return nil
}
func (s *approvalResumeCheckpointStub) Upsert(context.Context, string, domain.AgentExecutionCheckpoint) error {
	return nil
}
func (s *approvalResumeCheckpointStub) UpdateStatus(context.Context, string, string, string) error {
	return nil
}
func (s *approvalResumeCheckpointStub) DeleteExpired(context.Context, string) (int64, error) {
	return 0, nil
}
func (s *approvalResumeCheckpointStub) GetLatestActiveByConversation(context.Context, string, string) (*domain.AgentExecutionCheckpoint, error) {
	return nil, nil
}

// resumeOptionAgent 是 buildApprovalResumeOptions 的最小 Agent 桩：只提供
// GetConfig 返回 MCPToolIDs（guard 用它判定 agent 是否允许该工具）。
type resumeOptionAgent struct {
	config *domain.AgentConfig
}

func (a resumeOptionAgent) GetConfig() *domain.AgentConfig { return a.config }
func (resumeOptionAgent) Execute(context.Context, string, ...ExecutionOption) (*AgentResult, error) {
	return nil, nil
}
func (resumeOptionAgent) Reset()               {}
func (resumeOptionAgent) GetMemory() []Message { return nil }

// approvalResumeFixture 组装 resolve/build/finish 链路共享的依赖：已批准未过期的
// 审批（复用 Request 加密链路）+ 可脚本化 checkpoint 桩 + 会话存在 + 策略跳过
// （MCPToolPolicy nil）+ 角色解析器。返回 service 与内部桩供断言。
func approvalResumeFixture(t *testing.T, payload ToolApprovalPayload, role string) (*AgentService, *approvalRepoFake, *approvalResumeCheckpointStub) {
	t.Helper()
	approvalSvc, repo := approvedToolApproval(t, payload)
	cp := &approvalResumeCheckpointStub{
		cp: &domain.AgentExecutionCheckpoint{
			ExecutionID:      payload.ExecutionID,
			AgentID:          payload.AgentID,
			UserID:           payload.UserID,
			Status:           domain.ExecStatusWaitingApproval,
			RunGeneration:    1,
			RuntimeStateJSON: []byte(`{"approval_id":"approval-1"}`),
		},
	}
	svc := NewAgentService(AgentServiceDeps{
		ApprovalService:    approvalSvc,
		CheckpointStore:    cp,
		ChatStore:          resumeChatRepo{conv: &domain.ChatConversation{ID: "conv-alive"}},
		MCPToolExecutor:    resumeExecutorStub{},
		TenantRoleResolver: stubTenantRole{role: role},
		Logger:             zap.NewNop(),
	})
	return svc, repo, cp
}

func resumePayload(execID, agentID, userID string) ToolApprovalPayload {
	p := ToolApprovalPayload{
		TenantID: "t1", ExecutionID: execID, AgentID: agentID, UserID: userID,
		ToolCallID: "tc1", ServerID: "srv", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		ConversationID: "conv-alive", Query: "resume", Arguments: map[string]any{"id": "1"},
	}
	// C2d：guard 命中比较用 canonical digest（与 Request 加密链路同源），手工构造
	// 的载荷必须补齐 ArgumentsDigest，否则 digest 比较不命中。
	d, err := CanonicalToolArgumentsDigest(p.Arguments)
	if err != nil {
		panic(err)
	}
	p.ArgumentsDigest = d
	return p
}

// 发起人续跑自己的执行 → 放行（payload.UserID == actor && checkpoint.UserID 一致）。
func TestResolveApprovalResume_InitiatorAllowed(t *testing.T) {
	svc, _, cp := approvalResumeFixture(t, resumePayload("e1", "a1", "user-1"), "member")

	payload, approvalID, gotCp, _, err := svc.resolveApprovalResume(context.Background(), "t1", "user-1", "e1", "a1")

	require.NoError(t, err)
	require.Equal(t, "approval-1", approvalID)
	require.Equal(t, "user-1", payload.UserID)
	require.Same(t, cp.cp, gotCp)
}

// SECURITY-HIGH：发起人字段一致，但 checkpoint 归属不是本人（双保险）
// → fail-closed 拒绝，即使 payload.UserID == actor。
func TestResolveApprovalResume_InitiatorButCheckpointMismatchDenied(t *testing.T) {
	svc, _, cp := approvalResumeFixture(t, resumePayload("e1", "a1", "user-1"), "member")
	cp.cp.UserID = "attacker" // checkpoint 归属与审批发起人不一致

	_, _, _, _, err := svc.resolveApprovalResume(context.Background(), "t1", "user-1", "e1", "a1")

	require.ErrorIs(t, err, domain.ErrApprovalRoleDenied)
}

// SECURITY-HIGH：member 用他人 execution_id 续跑 → ErrApprovalRoleDenied，
// 关闭存在性 oracle（不泄露审批是否存在）。
func TestResolveApprovalResume_MemberCrossOwnerDenied(t *testing.T) {
	svc, _, _ := approvalResumeFixture(t, resumePayload("e1", "a1", "user-owner"), "member")

	_, _, _, _, err := svc.resolveApprovalResume(context.Background(), "t1", "user-member", "e1", "a1")

	require.ErrorIs(t, err, domain.ErrApprovalRoleDenied)
}

// SECURITY-HIGH：非发起人但 admin → 放行（角色现查，不读 JWT role claim）。
func TestResolveApprovalResume_AdminCrossOwnerAllowed(t *testing.T) {
	svc, _, _ := approvalResumeFixture(t, resumePayload("e1", "a1", "user-owner"), "admin")

	payload, approvalID, gotCp, _, err := svc.resolveApprovalResume(context.Background(), "t1", "admin-1", "e1", "a1")

	require.NoError(t, err)
	require.Equal(t, "approval-1", approvalID)
	require.Equal(t, "user-owner", payload.UserID)
	require.NotNil(t, gotCp)
}

// URL agentID 与批准载荷 AgentID 绑定不匹配 → ErrApprovalBindingMismatch
// （防跨 agent 复用已批准授权）。
func TestResolveApprovalResume_AgentBindingMismatch(t *testing.T) {
	svc, _, _ := approvalResumeFixture(t, resumePayload("e1", "a1", "user-1"), "member")

	_, _, _, _, err := svc.resolveApprovalResume(context.Background(), "t1", "user-1", "e1", "a2")

	require.ErrorIs(t, err, ErrApprovalBindingMismatch)
}

// SECURITY-HIGH（收紧）：载荷缺 agent_id（老数据/异常载荷）时，URL 指定
// agentID 视为不匹配 fail-closed——防缺省字段静默放行跨 agent 复用已批准授权。
func TestValidateApprovalBinding_MissingPayloadAgentIDFailsClosed(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{Logger: zap.NewNop()})
	// 载荷缺 AgentID + URL 指定 agentID → 不匹配（旧逻辑双侧空才放行，收紧为缺省即拒）。
	err := svc.validateApprovalBinding("a1", ToolApprovalPayload{TenantID: "t1", AgentID: ""})
	require.ErrorIs(t, err, ErrApprovalBindingMismatch)
	// URL 未指定 agentID → 不校验（无绑定意图，保持原路径）。
	err = svc.validateApprovalBinding("", ToolApprovalPayload{TenantID: "t1", AgentID: "a1"})
	require.NoError(t, err)
	// 一致 → 放行。
	err = svc.validateApprovalBinding("a1", ToolApprovalPayload{TenantID: "t1", AgentID: "a1"})
	require.NoError(t, err)
}

// 非续跑状态（paused/completed）→ 非审批续跑，返回空 payload（不触碰审批、不越权
// 探测）。running 是 H2① 软续跑候选（见 TestResolveApprovalResume_RunningCheckpointSoftResolves）。
func TestResolveApprovalResume_NonResumableStatusIsEmpty(t *testing.T) {
	svc, _, cp := approvalResumeFixture(t, resumePayload("e1", "a1", "user-1"), "member")
	cp.cp.Status = "paused"

	payload, approvalID, gotCp, _, err := svc.resolveApprovalResume(context.Background(), "t1", "user-1", "e1", "a1")

	require.NoError(t, err)
	require.Empty(t, payload.UserID)
	require.Empty(t, approvalID)
	require.Nil(t, gotCp)
}

// 未批准/已作废的审批：sentinel 错误上抛（transport 据此幂等恢复"等待审批"卡片），
// 不销毁审批行、不越权。
func TestResolveApprovalResume_NotApprovedPropagates(t *testing.T) {
	svc, repo, _ := approvalResumeFixture(t, resumePayload("e1", "a1", "user-1"), "member")
	repo.row.Status = string(domain.ToolApprovalPending) // 仍 pending：续跑竞态

	_, approvalID, gotCp, _, err := svc.resolveApprovalResume(context.Background(), "t1", "user-1", "e1", "a1")

	require.ErrorIs(t, err, ErrApprovalNotApproved)
	require.Equal(t, "approval-1", approvalID)
	require.NotNil(t, gotCp)
}

// H2① 软续跑：checkpoint 为 running（首个续跑抢占后、批准消费前刷新）且审批仍
// approved → 命中解析，返回批准载荷（注入 ApprovalResumePayload 合成 P1 直接
// 执行，不再经 LLM 重新生成 → 不再创建新审批循环）。
func TestResolveApprovalResume_RunningCheckpointSoftResolves(t *testing.T) {
	svc, _, cp := approvalResumeFixture(t, resumePayload("e1", "a1", "user-1"), "member")
	cp.cp.Status = "running"

	payload, approvalID, gotCp, _, err := svc.resolveApprovalResume(context.Background(), "t1", "user-1", "e1", "a1")

	require.NoError(t, err)
	require.Equal(t, "approval-1", approvalID)
	require.Equal(t, "user-1", payload.UserID)
	require.Same(t, cp.cp, gotCp)
}

// H2① running checkpoint 无 approval_id（正常执行中途刷新）→ 非审批续跑，空 payload。
func TestResolveApprovalResume_RunningWithoutApprovalIDIsEmpty(t *testing.T) {
	svc, _, cp := approvalResumeFixture(t, resumePayload("e1", "a1", "user-1"), "member")
	cp.cp.Status = "running"
	cp.cp.RuntimeStateJSON = []byte(`{}`)

	payload, approvalID, gotCp, _, err := svc.resolveApprovalResume(context.Background(), "t1", "user-1", "e1", "a1")

	require.NoError(t, err)
	require.Empty(t, payload.UserID)
	require.Empty(t, approvalID)
	require.Nil(t, gotCp)
}

// H2① running checkpoint + 审批已被并发消费（首个续跑 ClaimExecution 后正在执行）
// → ApprovedPayload fail-closed（ErrApprovalNotApproved），软续跑快速失败，不启动
// 图、不重复执行（单次消费由 ExecuteApproved 的 ClaimExecution CAS 保证）。
func TestResolveApprovalResume_RunningApprovalConsumedFastFails(t *testing.T) {
	svc, repo, cp := approvalResumeFixture(t, resumePayload("e1", "a1", "user-1"), "member")
	cp.cp.Status = "running"
	repo.row.Status = string(domain.ToolApprovalExecuting)

	_, _, gotCp, _, err := svc.resolveApprovalResume(context.Background(), "t1", "user-1", "e1", "a1")

	require.ErrorIs(t, err, ErrApprovalNotApproved)
	require.NotNil(t, gotCp)
}

// 过期审批 → ApprovedPayload 走 handleApprovedPayloadError：Invalidate(expired)
// 规范化 reason 标记，主错误 ErrApprovalExpired 上抛（CAS 失败按终态忽略）。
func TestResolveApprovalResume_ExpiredInvalidates(t *testing.T) {
	svc, repo, _ := approvalResumeFixture(t, resumePayload("e1", "a1", "user-1"), "member")
	repo.row.ExpiresAt = time.Now().Add(-time.Minute)

	_, _, _, _, err := svc.resolveApprovalResume(context.Background(), "t1", "user-1", "e1", "a1")

	require.ErrorIs(t, err, ErrApprovalExpired)
	require.Equal(t, []string{"expired"}, repo.invalidateReasons)
}

// claimApprovalResume：分代 CAS 失败 = 并发续跑已胜出，错误上抛映射 409。
func TestClaimApprovalResume_GenerationCASFailureBlocks(t *testing.T) {
	svc, _, cp := approvalResumeFixture(t, resumePayload("e1", "a1", "user-1"), "member")
	cp.genErr = errors.New("generation mismatch")

	err := svc.claimApprovalResume(context.Background(), "t1", "e1", cp.cp)

	require.Error(t, err)
	require.Contains(t, err.Error(), "another window already resumed")
	require.Zero(t, cp.statusCalls, "分代 CAS 失败不得再写状态 CAS")
}

// H2① maybeResumeApproval 对 running checkpoint 走软续跑：不 AdvanceRunGeneration、
// 不写状态 CAS（首个续跑已抢占），resuming=true 且 req 按批准载荷快照重写。
func TestMaybeResumeApproval_RunningCheckpointSkipsClaim(t *testing.T) {
	svc, _, cp := approvalResumeFixture(t, resumePayload("e1", "a1", "user-1"), "member")
	cp.cp.Status = "running"

	_, _, resuming, _, outReq, _, err := svc.maybeResumeApproval(
		context.Background(), "a1", ExecRequest{Query: "x", UserID: "user-1"}, ExecMeta{TenantID: "t1"}, "e1")

	require.NoError(t, err)
	require.True(t, resuming)
	require.Zero(t, cp.advanceCalls, "running checkpoint 不重复抢占分代")
	require.Zero(t, cp.statusCalls, "running checkpoint 不重复写状态 CAS")
	require.Equal(t, "resume", outReq.Query, "req 按批准载荷快照重写")
}

// waiting_approval checkpoint 仍走完整抢占（既有行为回归，避免误放软续跑）。
func TestMaybeResumeApproval_WaitingCheckpointClaims(t *testing.T) {
	svc, _, cp := approvalResumeFixture(t, resumePayload("e1", "a1", "user-1"), "member")

	_, _, resuming, _, _, _, err := svc.maybeResumeApproval(
		context.Background(), "a1", ExecRequest{Query: "x", UserID: "user-1"}, ExecMeta{TenantID: "t1"}, "e1")

	require.NoError(t, err)
	require.True(t, resuming)
	require.Equal(t, 1, cp.advanceCalls, "waiting_approval 抢占分代")
	require.Equal(t, 1, cp.statusCalls, "waiting_approval 抢占状态")
}

// claimApprovalResume：分代成功但状态 CAS 失败 → 错误上抛（不静默）。
func TestClaimApprovalResume_StatusCASFailurePropagates(t *testing.T) {
	svc, _, cp := approvalResumeFixture(t, resumePayload("e1", "a1", "user-1"), "member")
	cp.statusErr = errors.New("status CAS failed")

	err := svc.claimApprovalResume(context.Background(), "t1", "e1", cp.cp)

	require.Error(t, err)
	require.Equal(t, 1, cp.advanceCalls)
	require.Equal(t, 1, cp.statusCalls)
	require.Equal(t, domain.ExecStatusWaitingApproval, cp.statusFrom)
	require.Equal(t, "running", cp.statusTo)
}

// maybeResumeApproval：命中时抢占并把 req/meta 重写为批准载荷快照（query/发起人/
// 会话），供 assembleOptions 使用。
func TestMaybeResumeApproval_RewritesRequestToApprovalSnapshot(t *testing.T) {
	svc, _, _ := approvalResumeFixture(t, resumePayload("e1", "a1", "user-owner"), "admin")
	req := ExecRequest{Query: "stale", UserID: "user-member", ConversationID: "conv-other"}
	meta := ExecMeta{TenantID: "t1", ExecutionID: "e1"}

	payload, approvalID, resuming, _, outReq, outMeta, err := svc.maybeResumeApproval(
		context.Background(), "a1", req, meta, "e1")

	require.NoError(t, err)
	require.True(t, resuming)
	require.Equal(t, "approval-1", approvalID)
	require.Equal(t, "resume", outReq.Query, "重跑必须以批准载荷的 query 为准")
	require.Equal(t, "user-owner", outReq.UserID, "重跑必须以批准载荷的发起人身份")
	require.Equal(t, "conv-alive", outReq.ConversationID, "重跑必须写回原审批会话")
	require.True(t, outMeta.KnowledgeAssignmentsPinned)
	require.Equal(t, "user-owner", payload.UserID)
}

func resumeOptionService(t *testing.T, payload ToolApprovalPayload) (*AgentService, *approvalRepoFake) {
	t.Helper()
	approvalSvc, repo := approvedToolApproval(t, payload)
	svc := NewAgentService(AgentServiceDeps{
		ApprovalService: approvalSvc,
		MCPToolExecutor: resumeExecutorStub{},
		ToolAuthorizer: NewToolAuthorizer(stubToolUserScopeResolver{
			scope: port.ToolUserScope{UserActive: true, AllowsTool: true},
		}),
		Logger: zap.NewNop(),
	})
	return svc, repo
}

func applyToolOptions(t *testing.T, options []ExecutionOption) (*ExecutionConfig, port.ToolExecutionFn) {
	t.Helper()
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	require.NotNil(t, cfg.ToolExecutionFn, "审批续跑必须注入覆盖式 guard")
	require.Equal(t, "approval-1", cfg.ApprovalResumeID)
	require.Equal(t, "e1", cfg.ExecutionID)
	return cfg, cfg.ToolExecutionFn
}

func matchingToolRequest(payload ToolApprovalPayload) port.ToolExecutionRequest {
	return port.ToolExecutionRequest{
		TenantID: "t1", UserID: payload.UserID, AgentID: payload.AgentID, ToolCallID: "tc1",
		Tool: port.ToolDefinition{
			Name: "mcp:srv:delete", ProviderType: domain.ProviderTypeMCP,
			ServerID: "srv", CapabilityID: "delete",
			InputSchema: map[string]any{"type": "object"},
			Metadata:    map[string]any{"risk_level": "destructive", "policy_resolved": true},
		},
		Arguments:    payload.Arguments,
		AgentToolIDs: []string{"mcp:srv:delete"},
	}
}

// guard 命中批准载荷：server/capability/arguments 与批准一致 → 注入 approvalID
// 走 ExecuteApproved CAS 单次消费，consumed() 翻转为 true，审批行被 claim。
func TestBuildApprovalResumeOptions_GuardHitsExecuteApproved(t *testing.T) {
	payload := resumePayload("e1", "a1", "user-1")
	svc, repo := resumeOptionService(t, payload)
	agent := resumeOptionAgent{config: &domain.AgentConfig{MCPToolIDs: []string{"mcp:srv:delete"}}}

	options, consumed, err := svc.buildApprovalResumeOptions(context.Background(), "t1", agent, payload, "approval-1", false)
	require.NoError(t, err)
	cfg, fn := applyToolOptions(t, options)

	out, err := fn(context.Background(), matchingToolRequest(payload))

	require.NoError(t, err)
	require.True(t, consumed(), "命中批准必须消费")
	require.Equal(t, "approval-1", cfg.ApprovalResumeID)
	require.Equal(t, 1, repo.claimed, "ExecuteApproved 必须先 ClaimExecution 抢占")
	require.NotNil(t, out)
}

// guard 回退：与批准不一致的调用不注入 approvalID → 正常授权/审批路径。
// destructive 工具未批准 → ToolApprovalRequiredError（approvalID 空，RequestApproval
// 未装配则仅报需要审批），consumed() 保持 false。
func TestBuildApprovalResumeOptions_GuardFallsBackToNormalPath(t *testing.T) {
	payload := resumePayload("e1", "a1", "user-1")
	svc, repo := resumeOptionService(t, payload)
	agent := resumeOptionAgent{config: &domain.AgentConfig{MCPToolIDs: []string{"mcp:srv:get"}}}

	options, consumed, err := svc.buildApprovalResumeOptions(context.Background(), "t1", agent, payload, "approval-1", false)
	require.NoError(t, err)
	_, fn := applyToolOptions(t, options)

	req := matchingToolRequest(payload)
	req.Tool.Name = "mcp:srv:get"
	req.Tool.CapabilityID = "get"
	req.Arguments = map[string]any{"id": "other"}
	req.AgentToolIDs = []string{"mcp:srv:get"}
	req.Tool.Metadata["risk_level"] = "read"

	out, err := fn(context.Background(), req)

	require.NoError(t, err)
	require.False(t, consumed(), "不一致调用不得消费原批准")
	require.Zero(t, repo.claimed)
	require.NotNil(t, out, "read 工具走正常允许执行路径")
}

// guard 回退到审批：重跑中出现的其他 destructive 工具仍需要审批（复用正常
// RequestApproval 语义，即使审批续跑上下文内也未装配 requester → 返回需要审批）。
func TestBuildApprovalResumeOptions_GuardFallsBackToApprovalRequest(t *testing.T) {
	payload := resumePayload("e1", "a1", "user-1")
	svc, repo := resumeOptionService(t, payload)
	agent := resumeOptionAgent{config: &domain.AgentConfig{MCPToolIDs: []string{"mcp:srv:delete2"}}}

	options, consumed, err := svc.buildApprovalResumeOptions(context.Background(), "t1", agent, payload, "approval-1", false)
	require.NoError(t, err)
	_, fn := applyToolOptions(t, options)

	req := matchingToolRequest(payload)
	req.Tool.Name = "mcp:srv:delete2"
	req.Tool.CapabilityID = "delete2"
	req.AgentToolIDs = []string{"mcp:srv:delete2"}
	req.Tool.Metadata["risk_level"] = "destructive"

	_, err = fn(context.Background(), req)

	var approvalErr *port.ToolApprovalRequiredError
	require.ErrorAs(t, err, &approvalErr)
	require.False(t, consumed(), "未消费批准")
	require.Zero(t, repo.claimed)
	require.Zero(t, repo.released, "未走 ExecuteApproved 不得触达 release")
}

func resumeFinishService(t *testing.T, cp *approvalResumeCheckpointStub) *AgentService {
	t.Helper()
	return NewAgentService(AgentServiceDeps{
		CheckpointStore: cp,
		Logger:          zap.NewNop(),
	})
}

// 成功：MarkCompleted 收尾（不触碰 waiting_approval 回滚）。
func TestFinishApprovalResume_SuccessCompletes(t *testing.T) {
	cp := &approvalResumeCheckpointStub{}
	svc := resumeFinishService(t, cp)

	err := svc.finishApprovalResume(context.Background(), "t1", "e1", func() bool { return true }, false, nil)

	require.NoError(t, err)
	require.Equal(t, 1, cp.completed)
	require.Zero(t, cp.statusCalls)
	require.Empty(t, cp.terminated)
}

// SECURITY-MEDIUM-2：真实失败且批准未被本轮消费 → UpdateStatusFrom
// (running→waiting_approval) 回滚，发起人可重试同一批准，不丢失原审批。
func TestFinishApprovalResume_UnconsumedFailureRollsBack(t *testing.T) {
	cp := &approvalResumeCheckpointStub{}
	svc := resumeFinishService(t, cp)

	runErr := errors.New("llm downstream failure")
	err := svc.finishApprovalResume(context.Background(), "t1", "e1", func() bool { return false }, false, runErr)

	require.ErrorIs(t, err, runErr)
	require.Equal(t, 1, cp.statusCalls)
	require.Equal(t, "running", cp.statusFrom)
	require.Equal(t, domain.ExecStatusWaitingApproval, cp.statusTo)
	require.Empty(t, cp.terminated, "未消费批准不得写终态")
	require.Zero(t, cp.completed)
}

// 已消费（ExecuteApproved CAS 已把 approved→executed）且失败 → 写终态 failed
// 保留对账历史，不可回滚（批准已消耗）。
func TestFinishApprovalResume_ConsumedFailureTerminates(t *testing.T) {
	cp := &approvalResumeCheckpointStub{}
	svc := resumeFinishService(t, cp)

	runErr := errors.New("llm downstream failure")
	err := svc.finishApprovalResume(context.Background(), "t1", "e1", func() bool { return true }, false, runErr)

	require.ErrorIs(t, err, runErr)
	require.Equal(t, "failed", cp.terminated)
	require.Zero(t, cp.statusCalls, "已消费不得回滚 waiting_approval")
	require.Zero(t, cp.completed)
}

// 断线取消：retainRunningError → 保留 checkpoint 现状，不写终态不回滚
// （刷新/其他设备可经 active-execution 续跑，新鲜度窗口兜底僵尸）。
func TestFinishApprovalResume_ClientCancelPreservesState(t *testing.T) {
	cp := &approvalResumeCheckpointStub{}
	svc := resumeFinishService(t, cp)

	err := svc.finishApprovalResume(context.Background(), "t1", "e1", func() bool { return true }, false, context.Canceled)

	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, cp.statusCalls)
	require.Empty(t, cp.terminated)
	require.Zero(t, cp.completed)
}

// 审批等待错误：checkpoint 已 waiting_approval，terminate 会孤儿化在途审批
// → retainRunningError 保留现状。
func TestFinishApprovalResume_ApprovalPendingPreservesState(t *testing.T) {
	cp := &approvalResumeCheckpointStub{}
	svc := resumeFinishService(t, cp)

	runErr := &port.ToolApprovalRequiredError{ApprovalID: "approval-1"}
	err := svc.finishApprovalResume(context.Background(), "t1", "e1", func() bool { return false }, false, runErr)

	require.ErrorIs(t, err, runErr)
	require.Zero(t, cp.statusCalls)
	require.Empty(t, cp.terminated)
	require.Zero(t, cp.completed)
}

// H2① ErrApprovalAlreadyExecuted（并发窗口已有胜者接管）→ 保留 running：不回滚
// waiting_approval、不写终态，交胜者 MarkCompleted（避免回滚破坏胜者收尾）。
func TestFinishApprovalResume_AlreadyExecutedRetains(t *testing.T) {
	cp := &approvalResumeCheckpointStub{}
	svc := resumeFinishService(t, cp)

	err := svc.finishApprovalResume(
		context.Background(), "t1", "e1", func() bool { return false }, false, domain.ErrApprovalAlreadyExecuted)

	require.ErrorIs(t, err, domain.ErrApprovalAlreadyExecuted)
	require.Zero(t, cp.statusCalls, "已消费不得回滚 waiting_approval")
	require.Empty(t, cp.terminated, "已消费不得写终态 failed")
	require.Zero(t, cp.completed)
}

// 回滚 CAS 本身失败必须 Join 暴露，禁止吞错（恢复层任何终结动作失败都要可观测）。
func TestFinishApprovalResume_RollbackFailureJoins(t *testing.T) {
	cp := &approvalResumeCheckpointStub{statusErr: errors.New("db down")}
	svc := resumeFinishService(t, cp)

	runErr := errors.New("llm downstream failure")
	err := svc.finishApprovalResume(context.Background(), "t1", "e1", func() bool { return false }, false, runErr)

	require.ErrorIs(t, err, runErr)
	require.ErrorIs(t, err, cp.statusErr)
}

// guard 命中时 request 在 guard 内部被重写为批准载荷快照（tenant/user/agent/
// trace/execution/agentToolIDs），保证执行身份与批准时一致。可观测证据：空身份的
// 调用若未重写会被 Authorizer 以 tenant/user 缺失 deny；重写成功则放行并命中
// ExecuteApproved（consumed 翻转 + 审批行被 claim）。
func TestBuildApprovalResumeOptions_SnapshotRewrite(t *testing.T) {
	payload := resumePayload("e1", "a1", "user-1")
	svc, repo := resumeOptionService(t, payload)
	agent := resumeOptionAgent{config: &domain.AgentConfig{MCPToolIDs: []string{"mcp:srv:delete"}}}

	options, consumed, err := svc.buildApprovalResumeOptions(context.Background(), "t1", agent, payload, "approval-1", false)
	require.NoError(t, err)
	_, fn := applyToolOptions(t, options)

	req := matchingToolRequest(payload)
	req.TenantID = "" // 空身份：重写失败则 Authorize 因 tenant 缺失 deny
	req.UserID = ""
	req.AgentID = ""
	req.TraceID = ""

	_, err = fn(context.Background(), req)

	require.NoError(t, err, "空身份调用必须被重写为批准载荷快照，不允许被 Authorize 拒绝")
	require.True(t, consumed(), "重写后 guard 命中批准执行（否则走审批/执行路径不消费）")
	require.Equal(t, 1, repo.claimed, "ExecuteApproved 收到的是批准时身份")
}

// ── 终态续跑全链（已拒绝/已取消 → 工具执行失败 → LLM 收尾） ──
// terminalApprovalFixture 构造终态审批（cancelled/rejected/…）的续跑解析环境：
// waiting_approval checkpoint（含 approval_id）+ 会话存在 + 角色 member。
// 复用 approvedToolApproval 加密链路后改写 row.Status（终态行不做过期门控 H1）。
func terminalApprovalFixture(t *testing.T, payload ToolApprovalPayload, status domain.ToolApprovalStatus, role string) (*AgentService, *approvalRepoFake, *approvalResumeCheckpointStub) {
	t.Helper()
	approvalSvc, repo := approvedToolApproval(t, payload)
	repo.row.Status = string(status)
	cp := &approvalResumeCheckpointStub{
		cp: &domain.AgentExecutionCheckpoint{
			ExecutionID:      payload.ExecutionID,
			AgentID:          payload.AgentID,
			UserID:           payload.UserID,
			Status:           domain.ExecStatusWaitingApproval,
			RunGeneration:    1,
			RuntimeStateJSON: []byte(`{"approval_id":"approval-1"}`),
		},
	}
	svc := NewAgentService(AgentServiceDeps{
		ApprovalService:    approvalSvc,
		CheckpointStore:    cp,
		ChatStore:          resumeChatRepo{conv: &domain.ChatConversation{ID: payload.ConversationID}},
		MCPToolExecutor:    resumeExecutorStub{},
		TenantRoleResolver: stubTenantRole{role: role},
		Logger:             zap.NewNop(),
	})
	return svc, repo, cp
}

// 核心回归：cancelled 审批 + waiting_approval checkpoint → resolveApprovalResume 放行，
// 返回 terminal=true（把"审批未通过"交给 LLM 收尾，主链路不再卡死）。
func TestResolveApprovalResume_TerminalCancelledResumes(t *testing.T) {
	payload := resumePayload("e1", "a1", "user-1")
	svc, _, cp := terminalApprovalFixture(t, payload, domain.ToolApprovalCancelled, "member")

	gotPayload, approvalID, gotCP, terminal, err := svc.resolveApprovalResume(context.Background(), "t1", "user-1", "e1", "a1")

	require.NoError(t, err)
	require.True(t, terminal, "cancelled 审批必须标记终态续跑")
	require.Equal(t, "approval-1", approvalID)
	require.Equal(t, "user-1", gotPayload.UserID, "终态续跑同样复用解密载荷身份")
	require.Same(t, cp.cp, gotCP)
}

func TestResolveApprovalResume_TerminalRejectedResumes(t *testing.T) {
	payload := resumePayload("e1", "a1", "user-1")
	svc, _, _ := terminalApprovalFixture(t, payload, domain.ToolApprovalRejected, "member")

	_, _, _, terminal, err := svc.resolveApprovalResume(context.Background(), "t1", "user-1", "e1", "a1")

	require.NoError(t, err)
	require.True(t, terminal, "rejected 审批同样触发终态续跑")
}

// H1 在 agent_service 层的回归：终态行已过 expires_at 仍放行——堵住"轮询到 rejected
// 恰逢过期"的续跑断链竞态（ApprovedPayload 先报 Expired，终态判定不被其短路）。
func TestResolveApprovalResume_TerminalExpiredStillResumes(t *testing.T) {
	payload := resumePayload("e1", "a1", "user-1")
	svc, repo, _ := terminalApprovalFixture(t, payload, domain.ToolApprovalCancelled, "member")
	repo.row.ExpiresAt = time.Now().Add(-time.Hour) // 终态行已过期

	_, _, _, terminal, err := svc.resolveApprovalResume(context.Background(), "t1", "user-1", "e1", "a1")

	require.NoError(t, err)
	require.True(t, terminal, "已过期终态行仍放行（无过期门控）")
}

// 安全 review 回归：pending 审批绝不放行——ApprovedPayload 报 ErrApprovalNotApproved
// 且 TerminalResumePayload 对 pending 拒绝 → terminal=false，approvalID 仍返回供
// transport 保持 202 等待卡片。误放行 pending 会绕过"审批未通过前必须等待"门控。
func TestResolveApprovalResume_PendingStillBlocks(t *testing.T) {
	payload := resumePayload("e1", "a1", "user-1")
	svc, _, _ := terminalApprovalFixture(t, payload, domain.ToolApprovalPending, "member")

	_, approvalID, _, terminal, err := svc.resolveApprovalResume(context.Background(), "t1", "user-1", "e1", "a1")

	require.ErrorIs(t, err, ErrApprovalNotApproved, "pending 必须保持 202 等待语义")
	require.False(t, terminal, "pending 绝不放行（错误类型兜底会误吞 pending）")
	require.Equal(t, "approval-1", approvalID, "approvalID 仍返回，transport 据此幂等恢复等待卡片")
}

// 终态续跑 options：不设 WithApprovalResume（避免 resumeFromCheckpoint 走"恢复 running"
// 路径重复注入 P1）；guard 命中后 ExecuteApproved 必然报未批准错误，包装为友好文案
// （%w 保留哨兵 + 行为约束），工具不会执行。
func TestBuildApprovalResumeOptions_TerminalRejectsWithoutResumeKey(t *testing.T) {
	payload := resumePayload("e1", "a1", "user-1")
	svc, _ := terminalResumeOptionService(t, payload, domain.ToolApprovalCancelled)
	agent := resumeOptionAgent{config: &domain.AgentConfig{MCPToolIDs: []string{"mcp:srv:delete"}}}

	options, _, err := svc.buildApprovalResumeOptions(context.Background(), "t1", agent, payload, "approval-1", true)
	require.NoError(t, err)
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)
	require.NotNil(t, cfg.ToolExecutionFn, "终态续跑同样注入覆盖式 guard")
	require.Equal(t, "e1", cfg.ExecutionID)
	require.Empty(t, cfg.ApprovalResumeID, "终态模式不得设 WithApprovalResume（否则重复 P1 注入）")

	_, err = cfg.ToolExecutionFn(context.Background(), matchingToolRequest(payload))

	require.ErrorIs(t, err, ErrApprovalNotApproved, "%w 必须保留哨兵")
	require.Contains(t, err.Error(), "工具审批未通过")
	require.Contains(t, err.Error(), "请勿重试该工具")
}

// 友好文案回归：approved 模式（terminal=false）的错误不包装——C2 幂等/恢复卡片
// 语义不受影响，transport 对过期/未批准仍走既有映射。
func TestBuildApprovalResumeOptions_ApprovedModeDoesNotWrap(t *testing.T) {
	payload := resumePayload("e1", "a1", "user-1")
	svc, repo := terminalResumeOptionService(t, payload, domain.ToolApprovalApproved)
	repo.row.ExpiresAt = time.Now().Add(-time.Minute) // approved 但已过期 → ExecuteApproved 报错
	agent := resumeOptionAgent{config: &domain.AgentConfig{MCPToolIDs: []string{"mcp:srv:delete"}}}

	options, _, err := svc.buildApprovalResumeOptions(context.Background(), "t1", agent, payload, "approval-1", false)
	require.NoError(t, err)
	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)

	_, err = cfg.ToolExecutionFn(context.Background(), matchingToolRequest(payload))

	require.ErrorIs(t, err, ErrApprovalExpired, "approved 模式原样透传错误")
	require.NotContains(t, err.Error(), "工具审批未通过", "approved 模式不得包装终态文案")
}

// terminalResumeOptionService 构造终态/任意状态审批的 buildApprovalResumeOptions 环境。
func terminalResumeOptionService(t *testing.T, payload ToolApprovalPayload, status domain.ToolApprovalStatus) (*AgentService, *approvalRepoFake) {
	t.Helper()
	approvalSvc, repo := approvedToolApproval(t, payload)
	repo.row.Status = string(status)
	svc := NewAgentService(AgentServiceDeps{
		ApprovalService: approvalSvc,
		MCPToolExecutor: resumeExecutorStub{},
		ToolAuthorizer: NewToolAuthorizer(stubToolUserScopeResolver{
			scope: port.ToolUserScope{UserActive: true, AllowsTool: true},
		}),
		Logger: zap.NewNop(),
	})
	return svc, repo
}

// 终态收尾：runErr==nil → MarkCompleted 幂等收尾（不触碰 waiting_approval 回滚）。
func TestFinishApprovalResume_TerminalSuccessCompletes(t *testing.T) {
	cp := &approvalResumeCheckpointStub{}
	svc := resumeFinishService(t, cp)

	err := svc.finishApprovalResume(context.Background(), "t1", "e1", func() bool { return false }, true, nil)

	require.NoError(t, err)
	require.Equal(t, 1, cp.completed)
	require.Zero(t, cp.statusCalls)
	require.Empty(t, cp.terminated)
}

// H2 回归：终态失败直接 return——绝不再回滚 waiting_approval（否则前端轮询 cancelled
// →再续跑→再回滚死循环），也绝不二次 Terminate（finalizeReActCheckpoint 已写终态，
// 双写产生 "no active checkpoint" joined 错误）。
func TestFinishApprovalResume_TerminalFailureNoRollback(t *testing.T) {
	cp := &approvalResumeCheckpointStub{}
	svc := resumeFinishService(t, cp)

	runErr := errors.New("llm downstream failure")
	err := svc.finishApprovalResume(context.Background(), "t1", "e1", func() bool { return false }, true, runErr)

	require.ErrorIs(t, err, runErr)
	require.Zero(t, cp.statusCalls, "终态失败不得回滚 waiting_approval")
	require.Empty(t, cp.terminated, "终态失败不得二次 Terminate（双写消除）")
	require.Zero(t, cp.completed)
}
