package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/google/uuid"
)

var (
	ErrApprovalExpired         = errors.New("tool approval expired")
	ErrApprovalNotApproved     = errors.New("tool approval is not approved")
	ErrApprovalOutcomeUnknown  = errors.New("tool approval outcome is unknown")
	ErrApprovedToolNotReplayed = errors.New("approved tool call was not replayed")
	ErrApprovalBindingMismatch = errors.New("tool approval binding mismatch")
)

func approvedToolResumeError(consumed bool, runErr error) error {
	if runErr != nil {
		return runErr
	}
	if !consumed {
		return ErrApprovedToolNotReplayed
	}
	return nil
}

// decryptPayload 解密并解码审批载荷（Decide/ApprovedPayload/ApprovalDetail 共享）。
func decryptPayload(key [32]byte, ciphertext string) (ToolApprovalPayload, error) {
	plain, err := pkgcrypto.Decrypt(key, ciphertext)
	if err != nil {
		return ToolApprovalPayload{}, err
	}
	var payload ToolApprovalPayload
	if err := json.Unmarshal([]byte(plain), &payload); err != nil {
		return ToolApprovalPayload{}, fmt.Errorf("decode approval payload: %w", err)
	}
	return payload, nil
}

type ToolApprovalPayload struct {
	TenantID                 string                               `json:"tenant_id"`
	DecisionID               string                               `json:"decision_id"`
	ExecutionID              string                               `json:"execution_id"`
	TraceID                  string                               `json:"trace_id"`
	AgentID                  string                               `json:"agent_id"`
	UserID                   string                               `json:"user_id"`
	ConversationID           string                               `json:"conversation_id"`
	ToolCallID               string                               `json:"tool_call_id"`
	ServerID                 string                               `json:"server_id"`
	ToolName                 string                               `json:"tool_name"`
	RiskLevel                port.ToolRiskLevel                   `json:"risk_level"`
	Query                    string                               `json:"query"`
	Arguments                map[string]any                       `json:"arguments"`
	PinnedSkillRevisions     map[string]string                    `json:"pinned_skill_revisions,omitempty"`
	PinnedMCPRevisions       map[string]string                    `json:"pinned_mcp_revisions,omitempty"`
	PinnedKnowledgeRevisions map[string]port.KnowledgeRevisionPin `json:"pinned_knowledge_revisions,omitempty"`
	SubjectKind              string                               `json:"subject_kind"`
	AssignedApprover         string                               `json:"assigned_approver,omitempty"`
	PolicyVersion            string                               `json:"policy_version"`
	ArgumentsDigest          string                               `json:"arguments_digest"`
	SkillRevisionsDigest     string                               `json:"skill_revisions_digest"`
	MCPRevisionsDigest       string                               `json:"mcp_revisions_digest"`
	KnowledgeRevisionsDigest string                               `json:"knowledge_revisions_digest"`
}

// ApprovalDetail 审批详情下发（admin/owner 工作台），Payload 为解密并脱敏后的参数。
type ApprovalDetail struct {
	ID                 string         `json:"id"`
	SubjectKind        string         `json:"subject_kind"`
	ToolName           string         `json:"tool_name"`
	ServerID           string         `json:"server_id"`
	RiskLevel          string         `json:"risk_level"`
	Status             string         `json:"status"`
	UserID             string         `json:"user_id"`
	AssignedApprover   string         `json:"assigned_approver,omitempty"`
	InvalidationReason string         `json:"invalidation_reason,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
	ExpiresAt          time.Time      `json:"expires_at"`
	DecidedBy          string         `json:"decided_by,omitempty"`
	DecisionReason     string         `json:"decision_reason,omitempty"`
	Payload            map[string]any `json:"payload,omitempty"` // 解密后脱敏
}

type ToolApprovalService struct {
	repo        port.ToolApprovalRepo
	checkpoints port.CheckpointRepo
	key         [32]byte
	roles       port.TenantRoleResolver
	now         func() time.Time
}

func NewToolApprovalService(repo port.ToolApprovalRepo, checkpoints port.CheckpointRepo, key [32]byte) *ToolApprovalService {
	return &ToolApprovalService{repo: repo, checkpoints: checkpoints, key: key, now: time.Now}
}

// SetTenantRoleResolver 与既有 Service/Skill/MCP/Knowledge 一致的 setter 注入模式，
// 由 wiring injectTenantRoleResolvers 统一装配。
func (s *ToolApprovalService) SetTenantRoleResolver(resolver port.TenantRoleResolver) {
	s.roles = resolver
}

// validateAssignee D8：指定审批人必须本身是 admin/owner（软绑定，落地为工作台 PUT assignee）。
func (s *ToolApprovalService) validateAssignee(ctx context.Context, tenantID, assignee string) error {
	if assignee == "" {
		return nil
	}
	assigneeRole, err := s.resolveRole(ctx, tenantID, assignee)
	if err != nil {
		return err
	}
	if assigneeRole != "admin" && assigneeRole != "owner" {
		return domain.ErrApprovalAssigneeInvalid
	}
	return nil
}

// computePayloadDigests 计算四类 pin digest（Request 专用，集中错误处理）。
func computePayloadDigests(payload ToolApprovalPayload) (ToolApprovalPayload, error) {
	var err error
	payload.ArgumentsDigest, err = CanonicalToolArgumentsDigest(payload.Arguments)
	if err != nil {
		return payload, fmt.Errorf("digest tool approval arguments: %w", err)
	}
	payload.SkillRevisionsDigest, err = canonicalSkillRevisionsDigest(payload.PinnedSkillRevisions)
	if err != nil {
		return payload, fmt.Errorf("digest tool approval skill revisions: %w", err)
	}
	payload.MCPRevisionsDigest, err = canonicalMCPRevisionsDigest(payload.PinnedMCPRevisions)
	if err != nil {
		return payload, fmt.Errorf("digest tool approval MCP revisions: %w", err)
	}
	payload.KnowledgeRevisionsDigest, err = canonicalKnowledgeRevisionsDigest(payload.PinnedKnowledgeRevisions)
	if err != nil {
		return payload, fmt.Errorf("digest tool approval Knowledge revisions: %w", err)
	}
	return payload, nil
}

func (s *ToolApprovalService) Request(ctx context.Context, payload ToolApprovalPayload) (string, error) {
	// D3：subject 泛化——空值视为 mcp_tool（兼容存量调用）。
	if payload.SubjectKind == "" {
		payload.SubjectKind = domain.SubjectKindMCPTool
	}
	if err := domain.ValidateSubjectKind(payload.SubjectKind); err != nil {
		return "", err
	}
	if err := s.enforcePendingQuota(ctx, payload); err != nil {
		return "", err
	}
	// D8：指定审批人必须是 admin/owner（软绑定，落地为工作台 PUT assignee）。
	if err := s.validateAssignee(ctx, payload.TenantID, payload.AssignedApprover); err != nil {
		return "", err
	}
	if payload.DecisionID == "" {
		payload.DecisionID = uuid.NewString()
	}
	payload, err := computePayloadDigests(payload)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal approval payload: %w", err)
	}
	encrypted, err := pkgcrypto.Encrypt(s.key, string(raw))
	if err != nil {
		return "", err
	}
	// B1 修复延续：Create 显式落库 subject_kind/assigned_approver/conversation_id（INSERT 显式列时
	// DDL DEFAULT 不生效），SubjectKind 已在上方 ValidateSubjectKind 校验。
	expires := s.now().Add(30 * time.Minute)
	id, err := s.repo.Create(ctx, payload.TenantID, domain.ToolApproval{
		DecisionID:  payload.DecisionID,
		ExecutionID: payload.ExecutionID, TraceID: payload.TraceID, AgentID: payload.AgentID, UserID: payload.UserID,
		ToolCallID: payload.ToolCallID, ServerID: payload.ServerID, ToolName: payload.ToolName, RiskLevel: string(payload.RiskLevel),
		ArgumentsDigest: payload.ArgumentsDigest, SkillRevisionsDigest: payload.SkillRevisionsDigest,
		MCPRevisionsDigest:       payload.MCPRevisionsDigest,
		KnowledgeRevisionsDigest: payload.KnowledgeRevisionsDigest,
		PolicyVersion:            payload.PolicyVersion,
		SubjectKind:              payload.SubjectKind,
		AssignedApprover:         payload.AssignedApprover,
		ConversationID:           payload.ConversationID,
		EncryptedPayload:         encrypted, Status: "pending", ExpiresAt: expires,
	})
	if err != nil {
		return "", fmt.Errorf("create tool approval: %w", err)
	}
	if err := s.createApprovalCheckpoint(ctx, payload, id, expires); err != nil {
		return "", err
	}
	return id, nil
}

// enforcePendingQuota 防单用户无限创建未过期 pending 审批（存储 DoS 防护，D4 放宽后引入）。
func (s *ToolApprovalService) enforcePendingQuota(ctx context.Context, payload ToolApprovalPayload) error {
	pending, err := s.repo.ListPending(ctx, payload.TenantID, payload.UserID)
	if err != nil {
		return fmt.Errorf("count pending tool approvals: %w", err)
	}
	if len(pending) >= constants.MaxPendingApprovalsPerActor {
		return domain.ErrTooManyPendingApprovals
	}
	return nil
}

// createApprovalCheckpoint 断点恢复仅 MCP 工具审批需要（D3：评测/策略/服务器配置审批无 agent 恢复语义）。
func (s *ToolApprovalService) createApprovalCheckpoint(ctx context.Context, payload ToolApprovalPayload, id string, expires time.Time) error {
	if s.checkpoints == nil || payload.SubjectKind != domain.SubjectKindMCPTool {
		return nil
	}
	runtime, _ := json.Marshal(map[string]string{"approval_id": id})
	pending, _ := json.Marshal([]map[string]string{{"approval_id": id}})
	if err := s.checkpoints.Upsert(ctx, payload.TenantID, domain.AgentExecutionCheckpoint{
		ExecutionID: payload.ExecutionID, TraceID: payload.TraceID, AgentID: payload.AgentID, UserID: payload.UserID,
		ConversationID: payload.ConversationID,
		CurrentNode:    "tool_approval", PendingToolCallsJSON: pending, RuntimeStateJSON: runtime,
		Status: "waiting_approval", ResumeReason: "destructive_tool_approval", ExpiresAt: expires,
	}); err != nil {
		return fmt.Errorf("persist tool approval checkpoint: %w", err)
	}
	return nil
}

func (s *ToolApprovalService) ApprovedPayload(ctx context.Context, tenantID, approvalID string) (ToolApprovalPayload, error) {
	row, err := s.repo.Get(ctx, tenantID, approvalID)
	if err != nil {
		return ToolApprovalPayload{}, err
	}
	if !row.ExpiresAt.After(s.now()) {
		return ToolApprovalPayload{}, ErrApprovalExpired
	}
	if row.Status == string(domain.ToolApprovalOutcomeUnknown) {
		return ToolApprovalPayload{}, ErrApprovalOutcomeUnknown
	}
	// D9 终态：invalidated 语义失效独立报错；voided/cancelled 归入未批准。
	if row.Status == string(domain.ToolApprovalInvalidated) {
		return ToolApprovalPayload{}, domain.ErrApprovalInvalidated
	}
	if row.Status != string(domain.ToolApprovalApproved) {
		return ToolApprovalPayload{}, ErrApprovalNotApproved
	}
	payload, err := decryptPayload(s.key, row.EncryptedPayload)
	if err != nil {
		return ToolApprovalPayload{}, err
	}
	if mismatches := toolApprovalBindingMismatches(tenantID, row, payload); len(mismatches) > 0 {
		return ToolApprovalPayload{}, fmt.Errorf("%w: %s", ErrApprovalBindingMismatch, strings.Join(mismatches, ","))
	}
	return payload, nil
}

func toolApprovalBindingMismatches(tenantID string, row domain.ToolApproval, payload ToolApprovalPayload) []string {
	argumentsDigest, argumentsErr := CanonicalToolArgumentsDigest(payload.Arguments)
	skillDigest, skillErr := canonicalSkillRevisionsDigest(payload.PinnedSkillRevisions)
	mcpDigest, mcpErr := canonicalMCPRevisionsDigest(payload.PinnedMCPRevisions)
	knowledgeDigest, knowledgeErr := canonicalKnowledgeRevisionsDigest(payload.PinnedKnowledgeRevisions)
	mismatches := make([]string, 0, 16)
	check := func(name string, matches bool) {
		if !matches {
			mismatches = append(mismatches, name)
		}
	}
	check("arguments_digest", argumentsErr == nil && row.ArgumentsDigest == payload.ArgumentsDigest &&
		row.ArgumentsDigest == argumentsDigest)
	check("decision_id", row.DecisionID == payload.DecisionID)
	check("execution_id", row.ExecutionID == payload.ExecutionID)
	check("knowledge_revisions_digest", knowledgeErr == nil &&
		row.KnowledgeRevisionsDigest == payload.KnowledgeRevisionsDigest && row.KnowledgeRevisionsDigest == knowledgeDigest)
	check("mcp_revisions_digest", mcpErr == nil && row.MCPRevisionsDigest == payload.MCPRevisionsDigest &&
		row.MCPRevisionsDigest == mcpDigest)
	check("subject_kind", row.SubjectKind == payload.SubjectKind)
	check("policy_version", row.PolicyVersion == payload.PolicyVersion)
	check("skill_revisions_digest", skillErr == nil && row.SkillRevisionsDigest == payload.SkillRevisionsDigest &&
		row.SkillRevisionsDigest == skillDigest)
	check("tenant_id", payload.TenantID == tenantID)
	check("trace_id", row.TraceID == payload.TraceID)
	check("agent_id", row.AgentID == payload.AgentID)
	check("user_id", row.UserID == payload.UserID)
	check("tool_call_id", row.ToolCallID == payload.ToolCallID)
	check("server_id", row.ServerID == payload.ServerID)
	check("tool_name", row.ToolName == payload.ToolName)
	check("risk_level", row.RiskLevel == string(payload.RiskLevel))
	return mismatches
}

func (s *ToolApprovalService) Decide(ctx context.Context, tenantID, id, decision, actor, reason string) error {
	if decision != "approved" && decision != "rejected" {
		return errors.New("invalid approval decision")
	}
	// D10 层层校验：角色（fail closed）→ 状态 → 自审批 → 指定审批人匹配，全部通过才 CAS。
	role, err := s.resolveRole(ctx, tenantID, actor)
	if err != nil {
		return err
	}
	if role != "admin" && role != "owner" {
		return domain.ErrApprovalRoleDenied
	}
	// 状态/过期/解密集中校验（pendingPayload），状态检查先于过期：已决定行报 409 避免误导 410。
	payload, err := s.pendingPayload(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if payload.UserID == actor {
		return domain.ErrApprovalSelfDecision
	}
	// D8 软绑定：assignee 仅优先级提示（排序/通知），全部 admin/owner 可处理，
	// 不匹配不阻塞——避免指定审批人缺席时审批死锁（Escalation Policy 语义）。
	return s.repo.Decide(ctx, tenantID, id, decision, actor, reason, s.now())
}

// pendingPayload 读取并校验 pending 审批（状态 → 过期 → 解密），Decide 专用。
func (s *ToolApprovalService) pendingPayload(ctx context.Context, tenantID, id string) (ToolApprovalPayload, error) {
	row, err := s.repo.Get(ctx, tenantID, id)
	if err != nil {
		return ToolApprovalPayload{}, err
	}
	// 状态检查先于过期检查：已决定行（含已过期）报 409，避免误导性 410（review minor）。
	if row.Status != string(domain.ToolApprovalPending) {
		return ToolApprovalPayload{}, domain.ErrApprovalAlreadyDecided
	}
	if !row.ExpiresAt.After(s.now()) {
		return ToolApprovalPayload{}, ErrApprovalExpired
	}
	return decryptPayload(s.key, row.EncryptedPayload)
}

// resolveRole fail closed：resolver 缺失或解析失败都拒绝，禁止默认角色放行。
func (s *ToolApprovalService) resolveRole(ctx context.Context, tenantID, userID string) (string, error) {
	if s.roles == nil {
		return "", errors.New("tool approval role resolver unavailable")
	}
	return s.roles.ResolveTenantRole(ctx, tenantID, userID)
}

func (s *ToolApprovalService) MarkExecuted(ctx context.Context, tenantID, id string) error {
	return s.repo.MarkExecuted(ctx, tenantID, id)
}

// Void 终结已批准审批（D9：会话删除级联保留可对账历史）。CAS 0 行折叠为
// ErrApprovalAlreadyExecuted，由调用方按终态忽略。
func (s *ToolApprovalService) Void(ctx context.Context, tenantID, id, reason string) error {
	return s.repo.Void(ctx, tenantID, id, reason)
}

// Invalidate 失效 approved/executing 审批（D9：策略变更等语义失效）。
func (s *ToolApprovalService) Invalidate(ctx context.Context, tenantID, id, reason string) error {
	return s.repo.Invalidate(ctx, tenantID, id, reason)
}

func (s *ToolApprovalService) ExecuteApproved(ctx context.Context, tenantID, id, serverID, toolName string, args map[string]any, executor port.MCPToolExecutor) (port.MCPToolResult, error) {
	payload, err := s.ApprovedPayload(ctx, tenantID, id)
	if err != nil {
		return port.MCPToolResult{}, err
	}
	if payload.ServerID != serverID || payload.ToolName != toolName || !reflect.DeepEqual(payload.Arguments, args) {
		return port.MCPToolResult{}, errors.New("approved tool call does not match pinned request")
	}
	if err := s.repo.ClaimExecution(ctx, tenantID, id); err != nil {
		return port.MCPToolResult{}, fmt.Errorf("claim tool approval execution: %w", err)
	}
	var output port.MCPToolResult
	if revisionID := payload.PinnedMCPRevisions[serverID]; revisionID != "" {
		revisionExecutor, ok := executor.(port.MCPRevisionToolExecutor)
		if !ok {
			err = &port.MCPToolExecutionError{
				Outcome: port.ToolExecutionOutcomeNotSent,
				Err:     errors.New("MCP revision executor not configured"),
			}
		} else {
			output, err = revisionExecutor.ExecuteMCPToolRevision(
				ctx, serverID, toolName, revisionID, payload.RiskLevel, args,
			)
		}
	} else {
		output, err = executor.ExecuteMCPTool(ctx, serverID, toolName, args)
	}
	if err != nil {
		var executionErr *port.MCPToolExecutionError
		if errors.As(err, &executionErr) &&
			(executionErr.Outcome == port.ToolExecutionOutcomeNotSent ||
				executionErr.Outcome == port.ToolExecutionOutcomeDefiniteFailure) {
			if releaseErr := s.repo.ReleaseExecution(ctx, tenantID, id); releaseErr != nil {
				return port.MCPToolResult{}, errors.Join(err, fmt.Errorf("release tool approval execution: %w", releaseErr))
			}
			return port.MCPToolResult{}, err
		}
		if unknownErr := s.repo.MarkOutcomeUnknown(ctx, tenantID, id); unknownErr != nil {
			return port.MCPToolResult{}, errors.Join(err, fmt.Errorf("mark tool approval outcome unknown: %w", unknownErr))
		}
		return port.MCPToolResult{}, err
	}
	if err := s.repo.MarkExecuted(ctx, tenantID, id); err != nil {
		return port.MCPToolResult{}, fmt.Errorf("mark tool approval executed: %w", err)
	}
	return output, nil
}

// ListPending 按调用者身份过滤（修复 review blocking：member 横向越权）。
// 角色一律由 resolver 现查（单事实源，不信任调用方传入的角色字符串）：
// member/未知角色仅返回本人发起的审批（最小权限），admin/owner 全量。
func (s *ToolApprovalService) ListPending(ctx context.Context, tenantID, actor string) ([]domain.ToolApproval, error) {
	role, err := s.resolveRole(ctx, tenantID, actor)
	if err != nil {
		return nil, err
	}
	userID := actor
	if role == "admin" || role == "owner" {
		userID = ""
	}
	return s.repo.ListPending(ctx, tenantID, userID)
}

// ListHistory 仅 admin/owner 可查全租户历史（member 拒绝，fail closed）。
func (s *ToolApprovalService) ListHistory(ctx context.Context, tenantID string, page, pageSize int, actor string) ([]domain.ToolApproval, int, error) {
	role, err := s.resolveRole(ctx, tenantID, actor)
	if err != nil {
		return nil, 0, err
	}
	if role != "admin" && role != "owner" {
		return nil, 0, domain.ErrApprovalRoleDenied
	}
	return s.repo.ListHistory(ctx, tenantID, page, pageSize)
}

// ApprovalDetail 仅 admin/owner 可看；payload 解密后脱敏下发，禁止暴露凭据字段。
func (s *ToolApprovalService) ApprovalDetail(ctx context.Context, tenantID, id, actor string) (ApprovalDetail, error) {
	role, err := s.resolveRole(ctx, tenantID, actor)
	if err != nil {
		return ApprovalDetail{}, err
	}
	if role != "admin" && role != "owner" {
		return ApprovalDetail{}, domain.ErrApprovalRoleDenied
	}
	row, err := s.repo.Get(ctx, tenantID, id)
	if err != nil {
		return ApprovalDetail{}, err
	}
	detail := ApprovalDetail{
		ID: row.ID, SubjectKind: row.SubjectKind, ToolName: row.ToolName, ServerID: row.ServerID,
		RiskLevel: row.RiskLevel, Status: row.Status, UserID: row.UserID,
		AssignedApprover: row.AssignedApprover, InvalidationReason: row.InvalidationReason,
		CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt, DecidedBy: row.DecidedBy,
		DecisionReason: row.DecisionReason,
	}
	if row.EncryptedPayload != "" {
		plain, err := pkgcrypto.Decrypt(s.key, row.EncryptedPayload)
		if err != nil {
			return ApprovalDetail{}, err
		}
		var payload ToolApprovalPayload
		if err := json.Unmarshal([]byte(plain), &payload); err != nil {
			return ApprovalDetail{}, fmt.Errorf("decode approval payload: %w", err)
		}
		detail.Payload = RedactSensitivePayload(payload.Arguments)
	}
	return detail, nil
}

// SetAssignee 仅 admin/owner 可改指定审批人（actor 现查角色）；新审批人必须本身是 admin/owner（D8）。
func (s *ToolApprovalService) SetAssignee(ctx context.Context, tenantID, id, assignee, actor string) error {
	role, err := s.resolveRole(ctx, tenantID, actor)
	if err != nil {
		return err
	}
	if role != "admin" && role != "owner" {
		return domain.ErrApprovalRoleDenied
	}
	assigneeRole, err := s.resolveRole(ctx, tenantID, assignee)
	if err != nil {
		return err
	}
	if assigneeRole != "admin" && assigneeRole != "owner" {
		return domain.ErrApprovalAssigneeInvalid
	}
	// 前置 Get：不存在的审批返回 ErrApprovalNotFound（404），避免 0-row CAS 折叠成 AlreadyDecided（review minor）。
	if _, err := s.repo.Get(ctx, tenantID, id); err != nil {
		return err
	}
	return s.repo.UpdateAssignee(ctx, tenantID, id, assignee)
}

// ExecuteApprovedAction 通用执行：CAS 单次消费 + subject 分发执行器（D3/D4/D5）。
// 执行者角色由 resolver 现查（单事实源，fail closed）：仅 admin/owner 可执行，
// 不信任 JWT role claim 的陈旧窗口（review security）。预执行失败
// （ApprovalActionNotExecutedError）释放回 approved 可重试；产生副作用后的失败标记
// unknown_outcome；claim 后 Get 失败同样标记 unknown（避免卡死 executing）。
func (s *ToolApprovalService) ExecuteApprovedAction(ctx context.Context, tenantID, id, actor string, executor port.ApprovalActionExecutor) (map[string]any, error) {
	role, err := s.resolveRole(ctx, tenantID, actor)
	if err != nil {
		return nil, err
	}
	if role != "admin" && role != "owner" {
		return nil, domain.ErrApprovalRoleDenied
	}
	if executor == nil {
		return nil, errors.New("approval action executor not configured")
	}
	payload, err := s.ApprovedPayload(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if err := s.repo.ClaimExecution(ctx, tenantID, id); err != nil {
		return nil, fmt.Errorf("claim tool approval execution: %w", err)
	}
	row, getErr := s.repo.Get(ctx, tenantID, id)
	if getErr != nil {
		// claim 后 Get 失败：无法区分行消失与瞬态失败。fail-closed 标记 unknown（CAS 防双执行；
		// 若行仍在，executor 未运行即被 burn 属可用性代价，admin 可 Invalidate 收尾——review minor 接受）。
		return nil, s.markUnknownAfter(ctx, tenantID, id, getErr)
	}
	output, execErr := executor.ExecuteApprovalAction(ctx, port.ApprovalActionRequest{
		TenantID: tenantID, SubjectKind: payload.SubjectKind, Arguments: payload.Arguments,
		ActorID: payload.UserID, DecidedBy: row.DecidedBy,
	})
	if execErr != nil {
		return nil, s.finalizeActionFailure(ctx, tenantID, id, execErr)
	}
	if err := s.repo.MarkExecuted(ctx, tenantID, id); err != nil {
		// 副作用已发生，重试不安全——标记 unknown_outcome 避免卡死 executing（与 execErr 分支同语义）。
		return nil, s.markUnknownAfter(ctx, tenantID, id, fmt.Errorf("mark tool approval executed: %w", err))
	}
	return output, nil
}

// finalizeActionFailure 执行失败收尾：预执行失败（NotExecuted）释放回 approved 可重试；
// 副作用后失败标记 unknown_outcome。
func (s *ToolApprovalService) finalizeActionFailure(ctx context.Context, tenantID, id string, execErr error) error {
	var notExecuted *port.ApprovalActionNotExecutedError
	if errors.As(execErr, &notExecuted) {
		if releaseErr := s.repo.ReleaseExecution(ctx, tenantID, id); releaseErr != nil {
			return errors.Join(execErr, fmt.Errorf("release tool approval execution: %w", releaseErr))
		}
		return execErr
	}
	return s.markUnknownAfter(ctx, tenantID, id, execErr)
}

// markUnknownAfter 标记 unknown_outcome；标记失败时 Join 返回（原错误保留，不吞错）。
func (s *ToolApprovalService) markUnknownAfter(ctx context.Context, tenantID, id string, cause error) error {
	if unknownErr := s.repo.MarkOutcomeUnknown(ctx, tenantID, id); unknownErr != nil {
		return errors.Join(cause, fmt.Errorf("mark tool approval outcome unknown: %w", unknownErr))
	}
	return cause
}
