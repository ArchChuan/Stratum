package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
)

var (
	ErrApprovalExpired         = errors.New("tool approval expired")
	ErrApprovalNotApproved     = errors.New("tool approval is not approved")
	ErrApprovedToolNotReplayed = errors.New("approved tool call was not replayed")
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
	TenantID             string             `json:"tenant_id"`
	ExecutionID          string             `json:"execution_id"`
	TraceID              string             `json:"trace_id"`
	AgentID              string             `json:"agent_id"`
	UserID               string             `json:"user_id"`
	ConversationID       string             `json:"conversation_id"`
	ToolCallID           string             `json:"tool_call_id"`
	ServerID             string             `json:"server_id"`
	ToolName             string             `json:"tool_name"`
	RiskLevel            port.ToolRiskLevel `json:"risk_level"`
	Query                string             `json:"query"`
	Arguments            map[string]any     `json:"arguments"`
	PinnedSkillRevisions map[string]string  `json:"pinned_skill_revisions,omitempty"`
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
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal approval payload: %w", err)
	}
	encrypted, err := pkgcrypto.Encrypt(s.key, string(raw))
	if err != nil {
		return "", err
	}
	expires := s.now().Add(30 * time.Minute)
	id, err := s.repo.Create(ctx, payload.TenantID, domain.ToolApproval{
		ExecutionID: payload.ExecutionID, TraceID: payload.TraceID, AgentID: payload.AgentID, UserID: payload.UserID,
		ToolCallID: payload.ToolCallID, ServerID: payload.ServerID, ToolName: payload.ToolName, RiskLevel: string(payload.RiskLevel),
		EncryptedPayload: encrypted, Status: "pending", ExpiresAt: expires,
	})
	if err != nil {
		return "", err
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
	}
	return id, err
}

func (s *ToolApprovalService) ApprovedPayload(ctx context.Context, tenantID, approvalID string) (ToolApprovalPayload, error) {
	row, err := s.repo.Get(ctx, tenantID, approvalID)
	if err != nil {
		return ToolApprovalPayload{}, err
	}
	if !row.ExpiresAt.After(s.now()) {
		return ToolApprovalPayload{}, ErrApprovalExpired
	}
	if row.Status != "approved" {
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
	return payload, nil
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

func (s *ToolApprovalService) ExecuteApproved(ctx context.Context, tenantID, id, serverID, toolName string, args map[string]any, executor port.MCPToolExecutor) (any, error) {
	payload, err := s.ApprovedPayload(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if payload.ServerID != serverID || payload.ToolName != toolName || !reflect.DeepEqual(payload.Arguments, args) {
		return nil, errors.New("approved tool call does not match pinned request")
	}
	if err := s.repo.ClaimExecution(ctx, tenantID, id); err != nil {
		return nil, err
	}
	output, err := executor.ExecuteMCPTool(ctx, serverID, toolName, args)
	if err != nil {
		_ = s.repo.ReleaseExecution(ctx, tenantID, id)
		return nil, err
	}
	if err := s.repo.MarkExecuted(ctx, tenantID, id); err != nil {
		return nil, err
	}
	return output, nil
}
func (s *ToolApprovalService) ListPending(ctx context.Context, tenantID string) ([]domain.ToolApproval, error) {
	return s.repo.ListPending(ctx, tenantID)
}
