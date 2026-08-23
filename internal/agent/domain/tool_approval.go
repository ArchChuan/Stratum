package domain

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrApprovalNotFound         = errors.New("tool approval not found")
	ErrApprovalAlreadyDecided   = errors.New("tool approval already decided")
	ErrApprovalAlreadyExecuted  = errors.New("tool approval already executed")
	ErrApprovalSelfDecision     = errors.New("tool approval self decision not allowed")
	ErrApprovalAssigneeMismatch = errors.New("tool approval assigned approver mismatch")
	ErrApprovalAssigneeInvalid  = errors.New("tool approval assigned approver is not an admin or owner")
	ErrApprovalRoleDenied       = errors.New("tool approval requires admin or owner role")
	ErrApprovalPolicyChanged    = errors.New("tool approval policy changed")
	ErrApprovalTargetGone       = errors.New("tool approval target is gone")
	ErrApprovalConversationGone = errors.New("tool approval conversation is gone")
	ErrApprovalInvalidated      = errors.New("tool approval invalidated")
	// ErrTooManyPendingApprovals 单用户未过期 pending 审批数超上限（存储 DoS 防护）。
	ErrTooManyPendingApprovals = errors.New("too many pending tool approvals")
)

type ToolApprovalStatus string

// SubjectKind 标识审批作用对象类型（D3 泛化：MCP 工具 / 评测动作 / MCP 策略 / MCP 服务器配置）。
const (
	SubjectKindMCPTool          = "mcp_tool"
	SubjectKindEvaluationAction = "evaluation_action"
	SubjectKindMCPPolicy        = "mcp_policy"
	SubjectKindMCPServer        = "mcp_server"
)

// ValidateSubjectKind 校验审批 subject 类型；空值视为 mcp_tool（兼容存量调用）。
func ValidateSubjectKind(kind string) error {
	switch kind {
	case "", SubjectKindMCPTool, SubjectKindEvaluationAction, SubjectKindMCPPolicy, SubjectKindMCPServer:
		return nil
	}
	return fmt.Errorf("invalid tool approval subject kind: %s", kind)
}

const (
	ToolApprovalPending        ToolApprovalStatus = "pending"
	ToolApprovalApproved       ToolApprovalStatus = "approved"
	ToolApprovalRejected       ToolApprovalStatus = "rejected"
	ToolApprovalExpired        ToolApprovalStatus = "expired"
	ToolApprovalExecuting      ToolApprovalStatus = "executing"
	ToolApprovalExecuted       ToolApprovalStatus = "executed"
	ToolApprovalOutcomeUnknown ToolApprovalStatus = "unknown_outcome"
	// 失效终态（D9）：发起人撤销/会话删除级联、执行上下文销毁、审批语义失效；终态不可再 decide/resume。
	ToolApprovalCancelled   ToolApprovalStatus = "cancelled"
	ToolApprovalVoided      ToolApprovalStatus = "voided"
	ToolApprovalInvalidated ToolApprovalStatus = "invalidated"
)

// toolApprovalTransitions 定义合法状态转移（终态不可转出；查表降低圈复杂度）。
var toolApprovalTransitions = map[ToolApprovalStatus][]ToolApprovalStatus{
	ToolApprovalPending: {
		ToolApprovalApproved, ToolApprovalRejected, ToolApprovalExpired, ToolApprovalCancelled,
	},
	ToolApprovalApproved: {
		ToolApprovalExecuting, ToolApprovalVoided, ToolApprovalInvalidated,
	},
	ToolApprovalExecuting: {
		ToolApprovalExecuted, ToolApprovalApproved, ToolApprovalOutcomeUnknown, ToolApprovalInvalidated,
	},
}

func ValidateToolApprovalTransition(from, to ToolApprovalStatus) error {
	for _, next := range toolApprovalTransitions[from] {
		if next == to {
			return nil
		}
	}
	return fmt.Errorf("invalid tool approval transition: %s -> %s", from, to)
}

type ToolApproval struct {
	ID string `json:"id"`
	// DecisionID/ExecutionID/TraceID/ToolCallID 加 omitempty：member 历史 DTO 清零后
	// 序列化不输出（SECURITY-MEDIUM-3 剔除恢复键与内部追踪字段），admin/owner 正常输出。
	DecisionID               string `json:"decision_id,omitempty"`
	ExecutionID              string `json:"execution_id,omitempty"`
	TraceID                  string `json:"trace_id,omitempty"`
	AgentID                  string `json:"agent_id"`
	UserID                   string `json:"user_id"`
	ToolCallID               string `json:"tool_call_id,omitempty"`
	ServerID                 string `json:"server_id"`
	ToolName                 string `json:"tool_name"`
	RiskLevel                string `json:"risk_level"`
	ArgumentsDigest          string `json:"arguments_digest"`
	SkillRevisionsDigest     string `json:"skill_revisions_digest"`
	MCPRevisionsDigest       string `json:"mcp_revisions_digest"`
	KnowledgeRevisionsDigest string `json:"knowledge_revisions_digest"`
	PolicyVersion            string `json:"policy_version"`
	SubjectKind              string `json:"subject_kind"`
	AssignedApprover         string `json:"assigned_approver,omitempty"`
	InvalidationReason       string `json:"invalidation_reason,omitempty"`
	ConversationID           string `json:"conversation_id,omitempty"`
	EncryptedPayload         string `json:"-"`
	Status                   string `json:"status"`
	DecidedBy                string `json:"decided_by,omitempty"`
	// 展示昵称（display_name > github_login > raw id），由 service 层批量解析填充，
	// 禁止 raw user_id 直接展示给用户（用户要求：发起人/处理人用昵称）。
	UserDisplayName      string     `json:"user_display_name,omitempty"`
	AssignedApproverName string     `json:"assigned_approver_name,omitempty"`
	DecidedByName        string     `json:"decided_by_name,omitempty"`
	DecisionReason       string     `json:"decision_reason,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	DecidedAt            *time.Time `json:"decided_at,omitempty"`
	ExecutedAt           *time.Time `json:"executed_at,omitempty"`
	ExpiresAt            time.Time  `json:"expires_at"`
}
