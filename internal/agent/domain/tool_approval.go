package domain

import (
	"errors"
	"time"
)

var (
	ErrApprovalNotFound        = errors.New("tool approval not found")
	ErrApprovalAlreadyDecided  = errors.New("tool approval already decided")
	ErrApprovalAlreadyExecuted = errors.New("tool approval already executed")
)

type ToolApproval struct {
	ID                   string     `json:"id"`
	DecisionID           string     `json:"decision_id"`
	ExecutionID          string     `json:"execution_id"`
	TraceID              string     `json:"trace_id"`
	AgentID              string     `json:"agent_id"`
	UserID               string     `json:"user_id"`
	ToolCallID           string     `json:"tool_call_id"`
	ServerID             string     `json:"server_id"`
	ToolName             string     `json:"tool_name"`
	RiskLevel            string     `json:"risk_level"`
	ArgumentsDigest      string     `json:"arguments_digest"`
	SkillRevisionsDigest string     `json:"skill_revisions_digest"`
	PolicyVersion        string     `json:"policy_version"`
	EncryptedPayload     string     `json:"-"`
	Status               string     `json:"status"`
	DecidedBy            string     `json:"decided_by,omitempty"`
	DecisionReason       string     `json:"decision_reason,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	DecidedAt            *time.Time `json:"decided_at,omitempty"`
	ExecutedAt           *time.Time `json:"executed_at,omitempty"`
	ExpiresAt            time.Time  `json:"expires_at"`
}
