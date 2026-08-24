package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// resumeChatRepo 是 ResumeToolApproval 会话存在性校验的最小 ChatStore 桩：
// GetConversation 返回可脚本化的 conv/err，其余方法 no-op。
type resumeChatRepo struct {
	conv    *domain.ChatConversation
	convErr error
}

func (r resumeChatRepo) CreateConversation(context.Context, string, string, string, string) (*domain.ChatConversation, error) {
	return nil, nil
}
func (r resumeChatRepo) GetConversation(context.Context, string, string) (*domain.ChatConversation, error) {
	return r.conv, r.convErr
}
func (r resumeChatRepo) ListConversations(context.Context, string, string, string) ([]*domain.ChatConversation, error) {
	return nil, nil
}
func (r resumeChatRepo) RenameConversation(context.Context, string, string, string, string) error {
	return nil
}
func (r resumeChatRepo) DeleteConversation(context.Context, string, string, string) error { return nil }
func (r resumeChatRepo) AddMessage(context.Context, string, *domain.ChatMessage) error    { return nil }
func (r resumeChatRepo) ListMessages(context.Context, string, string, string) ([]*domain.ChatMessage, error) {
	return nil, nil
}
func (r resumeChatRepo) CleanupExpired(context.Context, string) error { return nil }
func (r resumeChatRepo) DeleteByAgent(context.Context, string, string) error {
	return nil
}

// resumePolicyStub 脚本化 MCPToolPolicyResolver：返回预设 risk/err。
type resumePolicyStub struct {
	risk port.ToolRiskLevel
	err  error
}

func (r resumePolicyStub) ResolveMCPToolRisk(context.Context, string, string, string) (port.ToolRiskLevel, error) {
	return r.risk, r.err
}

// resumeExecutorStub 仅用于满足 ResumeToolApproval 的 MCPToolExecutor 非 nil 前置检查
// （恢复层校验在 executor 之前短路，此桩不会被执行）。
type resumeExecutorStub struct{}

func (resumeExecutorStub) ExecuteMCPTool(context.Context, string, string, map[string]any) (port.MCPToolResult, error) {
	return port.MCPToolResult{}, nil
}

// resumeAgentRepo 是 happy-path 恢复测试的 AgentRepo 桩：Get 恒返回 not found，
// 使 Registry.Get 在恢复层校验通过后以 ErrNotFound 终止（不触达 executor）。
type resumeAgentRepo struct{}

func (resumeAgentRepo) Register(context.Context, *domain.AgentConfig, *auditdomain.ResourceChangeAuditEvent, []string) error {
	return nil
}
func (resumeAgentRepo) Get(context.Context, string) (*domain.AgentConfig, bool, error) {
	return nil, false, nil
}
func (resumeAgentRepo) GetSystemAssistant(context.Context) (*domain.AgentConfig, bool, error) {
	return nil, false, nil
}
func (resumeAgentRepo) GetAll(context.Context) ([]*domain.AgentConfig, error) {
	return nil, nil
}
func (resumeAgentRepo) Remove(context.Context, string, *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
func (resumeAgentRepo) Update(context.Context, *domain.AgentConfig, *auditdomain.ResourceChangeAuditEvent, string, bool) error {
	return nil
}
func (resumeAgentRepo) UpdateSystemAssistantModel(context.Context, string, string, int, int, *auditdomain.ResourceChangeAuditEvent) (*domain.AgentConfig, error) {
	return nil, nil
}
func (resumeAgentRepo) UpdateSystemAssistantAll(context.Context, string, string, int, int, int, map[string]any, *auditdomain.ResourceChangeAuditEvent) (*domain.AgentConfig, error) {
	return nil, nil
}

// approvedToolApproval 构造一个已批准、未过期、绑定字段一致的审批（复用 Request 加密链路），
// 返回 approvalSvc 与 mock repo——ApprovedPayload 可成功解密并校验 binding。
func approvedToolApproval(t *testing.T, payload ToolApprovalPayload) (*ToolApprovalService, *approvalRepoFake) {
	t.Helper()
	repo := &approvalRepoFake{}
	approvalSvc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test-key"))
	if _, err := approvalSvc.Request(context.Background(), payload); err != nil {
		t.Fatalf("create approved approval fixture: %v", err)
	}
	repo.row.Status = string(domain.ToolApprovalApproved)
	repo.row.ExpiresAt = time.Now().Add(time.Minute)
	return approvalSvc, repo
}

// resumeValidationService 组装 ResumeToolApproval 依赖。registry 可为 nil——恢复层
// 校验先于 Registry.Get；仅 happy-path 测试需要注入 registry 以走完全程。
func resumeValidationService(approvalSvc *ToolApprovalService, chat ChatStore, policy port.MCPToolPolicyResolver, registry *Registry) *AgentService {
	return NewAgentService(AgentServiceDeps{
		Registry:        registry,
		ApprovalService: approvalSvc,
		ChatStore:       chat,
		MCPToolPolicy:   policy,
		MCPToolExecutor: resumeExecutorStub{},
		Logger:          zap.NewNop(),
	})
}

// D9 恢复层校验：会话已删除 → Void(conversation_deleted) + ErrApprovalConversationGone。
// Registry 为 nil 是刻意的：校验先于 Registry.Get，若顺序漂移测试会 panic 暴露。
func TestResumeToolApprovalConversationGone(t *testing.T) {
	approvalSvc, repo := approvedToolApproval(t, ToolApprovalPayload{
		TenantID: "t1", ExecutionID: "e1", AgentID: "a1", UserID: "u1",
		ToolCallID: "tc1", ServerID: "srv", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		ConversationID: "conv-gone", Query: "resume", Arguments: map[string]any{"id": "1"},
	})
	svc := resumeValidationService(approvalSvc, resumeChatRepo{convErr: domain.ErrNotFound}, resumePolicyStub{}, nil)

	_, _, err := svc.ResumeToolApproval(context.Background(), "t1", "u1", "approval-1")
	require.ErrorIs(t, err, domain.ErrApprovalConversationGone)
	require.Equal(t, []string{"conversation_deleted"}, repo.voidReasons)
	require.Empty(t, repo.invalidateReasons, "会话删除校验短路，不应走到策略重查")
}

// D9 恢复层校验：策略重查发现当前 risk 与审批时不一致 → Invalidate(policy_changed)
// + ErrApprovalPolicyChanged。
func TestResumeToolApprovalPolicyChanged(t *testing.T) {
	approvalSvc, repo := approvedToolApproval(t, ToolApprovalPayload{
		TenantID: "t1", ExecutionID: "e1", AgentID: "a1", UserID: "u1",
		ToolCallID: "tc1", ServerID: "srv", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		ConversationID: "conv-alive", Query: "resume", Arguments: map[string]any{"id": "1"},
	})
	svc := resumeValidationService(approvalSvc,
		resumeChatRepo{conv: &domain.ChatConversation{ID: "conv-alive"}},
		resumePolicyStub{risk: port.ToolRiskRead}, nil)

	_, _, err := svc.ResumeToolApproval(context.Background(), "t1", "u1", "approval-1")
	require.ErrorIs(t, err, domain.ErrApprovalPolicyChanged)
	require.Equal(t, []string{"policy_changed"}, repo.invalidateReasons)
	require.Empty(t, repo.voidReasons, "策略重查失败走 Invalidate，不应 Void")
}

// 策略解析器报错：unresolved 不等于变更——fail closed 拒绝恢复（原错误返回）但
// 不 Invalidate，避免 resolver 瞬态故障永久销毁有效审批并写入假审计 reason。
func TestResumeToolApprovalPolicyResolveErrorFailClosed(t *testing.T) {
	approvalSvc, repo := approvedToolApproval(t, ToolApprovalPayload{
		TenantID: "t1", ExecutionID: "e1", AgentID: "a1", UserID: "u1",
		ToolCallID: "tc1", ServerID: "srv", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		ConversationID: "conv-alive", Query: "resume", Arguments: map[string]any{"id": "1"},
	})
	svc := resumeValidationService(approvalSvc,
		resumeChatRepo{conv: &domain.ChatConversation{ID: "conv-alive"}},
		resumePolicyStub{err: domain.ErrNotFound}, nil)

	_, _, err := svc.ResumeToolApproval(context.Background(), "t1", "u1", "approval-1")
	require.ErrorIs(t, err, domain.ErrNotFound, "resolver 错误应原样传播（fail closed）")
	require.NotErrorIs(t, err, domain.ErrApprovalPolicyChanged)
	require.Empty(t, repo.invalidateReasons, "unresolved 不 Invalidate")
	require.Empty(t, repo.voidReasons)
}

// 过期兜底：ApprovedPayload 返回 ErrApprovalExpired → Invalidate(expired) 规范化
// reason 标记，主错误仍返回（CAS 失败按终态忽略）。
func TestResumeToolApprovalExpiredInvalidates(t *testing.T) {
	approvalSvc, repo := approvedToolApproval(t, ToolApprovalPayload{
		TenantID: "t1", ExecutionID: "e1", AgentID: "a1", UserID: "u1",
		ToolCallID: "tc1", ServerID: "srv", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		ConversationID: "conv-alive", Query: "resume", Arguments: map[string]any{"id": "1"},
	})
	repo.row.ExpiresAt = time.Now().Add(-time.Minute) // 已过期
	svc := resumeValidationService(approvalSvc, resumeChatRepo{}, resumePolicyStub{}, nil)

	_, _, err := svc.ResumeToolApproval(context.Background(), "t1", "u1", "approval-1")
	require.ErrorIs(t, err, ErrApprovalExpired)
	require.Equal(t, []string{"expired"}, repo.invalidateReasons)
	require.Empty(t, repo.voidReasons)
}

// 终结动作硬失败：Void 真实失败（非 CAS 终态）必须 Join 暴露，不吞错——主因
// （会话不存在）与终结失败一并返回，调用方可观测。
func TestResumeToolApprovalVoidFailureJoins(t *testing.T) {
	approvalSvc, repo := approvedToolApproval(t, ToolApprovalPayload{
		TenantID: "t1", ExecutionID: "e1", AgentID: "a1", UserID: "u1",
		ToolCallID: "tc1", ServerID: "srv", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		ConversationID: "conv-gone", Query: "resume", Arguments: map[string]any{"id": "1"},
	})
	dbDown := errors.New("db down")
	repo.voidErr = dbDown
	svc := resumeValidationService(approvalSvc, resumeChatRepo{convErr: domain.ErrNotFound}, resumePolicyStub{}, nil)

	_, _, err := svc.ResumeToolApproval(context.Background(), "t1", "u1", "approval-1")
	require.ErrorIs(t, err, domain.ErrNotFound)
	require.ErrorIs(t, err, dbDown)
	require.Equal(t, []string{"conversation_deleted"}, repo.voidReasons)
}

// 策略变更 Invalidance 硬失败同样 Join 暴露。
func TestResumeToolApprovalInvalidateFailureJoins(t *testing.T) {
	approvalSvc, repo := approvedToolApproval(t, ToolApprovalPayload{
		TenantID: "t1", ExecutionID: "e1", AgentID: "a1", UserID: "u1",
		ToolCallID: "tc1", ServerID: "srv", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		ConversationID: "conv-alive", Query: "resume", Arguments: map[string]any{"id": "1"},
	})
	dbDown := errors.New("db down")
	repo.invalidateErr = dbDown
	svc := resumeValidationService(approvalSvc,
		resumeChatRepo{conv: &domain.ChatConversation{ID: "conv-alive"}},
		resumePolicyStub{risk: port.ToolRiskRead}, nil)

	_, _, err := svc.ResumeToolApproval(context.Background(), "t1", "u1", "approval-1")
	require.ErrorIs(t, err, dbDown)
	require.Equal(t, []string{"policy_changed"}, repo.invalidateReasons)
}

// 非 MCP 审批（evaluation_action/mcp_policy/mcp_server）无 MCP tool risk 语义：
// 策略重查必须跳过，即使 resolver 对无关 server/tool 名返回不同等级也不能
// Invalidate 有效审批（防误毁 + 防误导审计 reason）。走正常恢复链终止。
func TestResumeToolApprovalNonMCPSubjectSkipsPolicyCheck(t *testing.T) {
	approvalSvc, repo := approvedToolApproval(t, ToolApprovalPayload{
		TenantID: "t1", ExecutionID: "e1", AgentID: "a1", UserID: "u1",
		ToolCallID: "tc1", ServerID: "srv", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		ConversationID: "conv-alive", Query: "resume", Arguments: map[string]any{"id": "1"},
		SubjectKind: domain.SubjectKindEvaluationAction,
	})
	registry := NewRegistry(resumeAgentRepo{}, BuiltinSystemAssistantProfileSource(), zap.NewNop())
	svc := resumeValidationService(approvalSvc,
		resumeChatRepo{conv: &domain.ChatConversation{ID: "conv-alive"}},
		resumePolicyStub{risk: port.ToolRiskRead}, registry)

	_, _, err := svc.ResumeToolApproval(context.Background(), "t1", "u1", "approval-1")
	require.ErrorIs(t, err, ErrNotFound, "非 MCP 审批跳过策略重查后走正常恢复链")
	require.NotErrorIs(t, err, domain.ErrApprovalPolicyChanged)
	require.Empty(t, repo.invalidateReasons, "SubjectKind 门控后不得误 Invalidate")
	require.Empty(t, repo.voidReasons)
}

// resolver 返回空风险（无错误）：与 unresolved 同语义——fail closed 拒绝恢复但
// 不 Invalidate，避免空结果被当作"已解析"后误毁有效审批。
func TestResumeToolApprovalEmptyRiskFailClosed(t *testing.T) {
	approvalSvc, repo := approvedToolApproval(t, ToolApprovalPayload{
		TenantID: "t1", ExecutionID: "e1", AgentID: "a1", UserID: "u1",
		ToolCallID: "tc1", ServerID: "srv", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		ConversationID: "conv-alive", Query: "resume", Arguments: map[string]any{"id": "1"},
	})
	svc := resumeValidationService(approvalSvc,
		resumeChatRepo{conv: &domain.ChatConversation{ID: "conv-alive"}},
		resumePolicyStub{risk: ""}, nil)

	_, _, err := svc.ResumeToolApproval(context.Background(), "t1", "u1", "approval-1")
	require.Error(t, err, "空风险应 fail closed")
	require.NotErrorIs(t, err, domain.ErrApprovalPolicyChanged)
	require.Empty(t, repo.invalidateReasons, "空风险不 Invalidate")
	require.Empty(t, repo.voidReasons)
}

// 策略重查一致 → 不失效审批；继续恢复流程，最终在 Registry.Get（agent 不存在）
// 以 ErrNotFound 终止——证明恢复层校验只在条件失败时短路。
func TestResumeToolApprovalPolicyUnchangedKeepsApproval(t *testing.T) {
	approvalSvc, repo := approvedToolApproval(t, ToolApprovalPayload{
		TenantID: "t1", ExecutionID: "e1", AgentID: "a1", UserID: "u1",
		ToolCallID: "tc1", ServerID: "srv", ToolName: "get", RiskLevel: port.ToolRiskRead,
		ConversationID: "conv-alive", Query: "resume", Arguments: map[string]any{"id": "1"},
	})
	registry := NewRegistry(resumeAgentRepo{}, BuiltinSystemAssistantProfileSource(), zap.NewNop())
	svc := resumeValidationService(approvalSvc,
		resumeChatRepo{conv: &domain.ChatConversation{ID: "conv-alive"}},
		resumePolicyStub{risk: port.ToolRiskRead}, registry)

	_, _, err := svc.ResumeToolApproval(context.Background(), "t1", "u1", "approval-1")
	require.ErrorIs(t, err, ErrNotFound, "校验通过后走正常恢复链，不应返回分类错误")
	require.NotErrorIs(t, err, domain.ErrApprovalPolicyChanged)
	require.NotErrorIs(t, err, domain.ErrApprovalConversationGone)
	require.Empty(t, repo.voidReasons)
	require.Empty(t, repo.invalidateReasons)
}
