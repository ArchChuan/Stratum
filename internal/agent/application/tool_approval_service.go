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
	PolicyVersion            string                               `json:"policy_version"`
	ArgumentsDigest          string                               `json:"arguments_digest"`
	SkillRevisionsDigest     string                               `json:"skill_revisions_digest"`
	MCPRevisionsDigest       string                               `json:"mcp_revisions_digest"`
	KnowledgeRevisionsDigest string                               `json:"knowledge_revisions_digest"`
}

type ToolApprovalService struct {
	repo        port.ToolApprovalRepo
	checkpoints port.CheckpointRepo
	key         [32]byte
	now         func() time.Time
}

func NewToolApprovalService(repo port.ToolApprovalRepo, checkpoints port.CheckpointRepo, key [32]byte) *ToolApprovalService {
	return &ToolApprovalService{repo: repo, checkpoints: checkpoints, key: key, now: time.Now}
}

func (s *ToolApprovalService) Request(ctx context.Context, payload ToolApprovalPayload) (string, error) {
	if payload.DecisionID == "" {
		payload.DecisionID = uuid.NewString()
	}
	var err error
	payload.ArgumentsDigest, err = CanonicalToolArgumentsDigest(payload.Arguments)
	if err != nil {
		return "", fmt.Errorf("digest tool approval arguments: %w", err)
	}
	payload.SkillRevisionsDigest, err = canonicalSkillRevisionsDigest(payload.PinnedSkillRevisions)
	if err != nil {
		return "", fmt.Errorf("digest tool approval skill revisions: %w", err)
	}
	payload.MCPRevisionsDigest, err = canonicalMCPRevisionsDigest(payload.PinnedMCPRevisions)
	if err != nil {
		return "", fmt.Errorf("digest tool approval MCP revisions: %w", err)
	}
	payload.KnowledgeRevisionsDigest, err = canonicalKnowledgeRevisionsDigest(payload.PinnedKnowledgeRevisions)
	if err != nil {
		return "", fmt.Errorf("digest tool approval Knowledge revisions: %w", err)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal approval payload: %w", err)
	}
	encrypted, err := pkgcrypto.Encrypt(s.key, string(raw))
	if err != nil {
		return "", err
	}
	// 修复 review B1/M1：Create 必须显式落库 subject_kind 与 conversation_id（INSERT 显式列时
	// DDL DEFAULT 不生效）；当前创建路径固定 mcp_tool，Task 2 引入 payload.SubjectKind 后
	// 在此处先行 ValidateSubjectKind 再写入。
	expires := s.now().Add(30 * time.Minute)
	id, err := s.repo.Create(ctx, payload.TenantID, domain.ToolApproval{
		DecisionID:  payload.DecisionID,
		ExecutionID: payload.ExecutionID, TraceID: payload.TraceID, AgentID: payload.AgentID, UserID: payload.UserID,
		ToolCallID: payload.ToolCallID, ServerID: payload.ServerID, ToolName: payload.ToolName, RiskLevel: string(payload.RiskLevel),
		ArgumentsDigest: payload.ArgumentsDigest, SkillRevisionsDigest: payload.SkillRevisionsDigest,
		MCPRevisionsDigest:       payload.MCPRevisionsDigest,
		KnowledgeRevisionsDigest: payload.KnowledgeRevisionsDigest,
		PolicyVersion:            payload.PolicyVersion,
		SubjectKind:              domain.SubjectKindMCPTool,
		ConversationID:           payload.ConversationID,
		EncryptedPayload:         encrypted, Status: "pending", ExpiresAt: expires,
	})
	if err != nil {
		return "", fmt.Errorf("create tool approval: %w", err)
	}
	if s.checkpoints != nil {
		runtime, _ := json.Marshal(map[string]string{"approval_id": id})
		pending, _ := json.Marshal([]map[string]string{{"approval_id": id}})
		err = s.checkpoints.Upsert(ctx, payload.TenantID, domain.AgentExecutionCheckpoint{
			ExecutionID: payload.ExecutionID, TraceID: payload.TraceID, AgentID: payload.AgentID, UserID: payload.UserID,
			ConversationID: payload.ConversationID,
			CurrentNode:    "tool_approval", PendingToolCallsJSON: pending, RuntimeStateJSON: runtime,
			Status: "waiting_approval", ResumeReason: "destructive_tool_approval", ExpiresAt: expires,
		})
		if err != nil {
			return "", fmt.Errorf("persist tool approval checkpoint: %w", err)
		}
	}
	return id, nil
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
	if row.Status != string(domain.ToolApprovalApproved) {
		return ToolApprovalPayload{}, ErrApprovalNotApproved
	}
	plain, err := pkgcrypto.Decrypt(s.key, row.EncryptedPayload)
	if err != nil {
		return ToolApprovalPayload{}, err
	}
	var payload ToolApprovalPayload
	if err := json.Unmarshal([]byte(plain), &payload); err != nil {
		return ToolApprovalPayload{}, fmt.Errorf("decode approval payload: %w", err)
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
	return s.repo.Decide(ctx, tenantID, id, decision, actor, reason, s.now())
}

func (s *ToolApprovalService) MarkExecuted(ctx context.Context, tenantID, id string) error {
	return s.repo.MarkExecuted(ctx, tenantID, id)
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
// member 仅返回本人发起的审批；admin/owner（或未知角色，fail closed 取最小权限）仅 admin/owner 可看全量。
func (s *ToolApprovalService) ListPending(ctx context.Context, tenantID, actorID, roleClass string) ([]domain.ToolApproval, error) {
	if roleClass == "" {
		roleClass = "member"
	}
	userID := actorID
	if roleClass == "admin" || roleClass == "owner" {
		userID = ""
	}
	return s.repo.ListPending(ctx, tenantID, userID)
}
